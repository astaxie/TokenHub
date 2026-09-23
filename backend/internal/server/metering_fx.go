package server

import (
	"encoding/json"
	"net/http"
	"time"

	"gorm.io/gorm"
	"tokenhub/backend/internal/metering"
)

type meteringExchangeRate struct {
	ID            string    `json:"id"`
	Currency      string    `json:"currency"`
	Rate          string    `json:"rate"`
	Source        string    `json:"source"`
	EffectiveFrom time.Time `json:"effective_from"`
}

func (s *Server) handleMeteringExchangeRate(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	var rate meteringExchangeRate
	if err := s.decodeJSON(w, r, &rate); err != nil {
		writeError(w, r, err)
		return
	}
	store, ok := s.store.(interface {
		PublishMeteringExchangeRate(meteringExchangeRate) (meteringExchangeRate, error)
	})
	if !ok {
		writeError(w, r, NewHTTPError(503, "metering_unavailable", "Metering persistence is unavailable"))
		return
	}
	saved, err := store.PublishMeteringExchangeRate(rate)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "publish", "billing_exchange_rate", saved.ID, "", saved)
	writeJSON(w, 201, saved)
}
func (s *GormStore) PublishMeteringExchangeRate(rate meteringExchangeRate) (meteringExchangeRate, error) {
	if rate.Currency == "USD" || rate.Source == "" {
		return rate, NewHTTPError(400, "invalid_exchange_rate", "A non-USD currency and source are required")
	}
	parsed, err := metering.Decimal(rate.Rate)
	if err != nil || parsed.Sign() <= 0 {
		return rate, NewHTTPError(400, "invalid_exchange_rate", "Rate must be a positive decimal")
	}
	if _, err := metering.Price(metering.Rates{}, metering.Units{}, rate.Currency, rate.Rate); err != nil {
		return rate, NewHTTPError(400, "invalid_exchange_rate", err.Error())
	}
	now, err := s.databaseNow(s.db)
	if err != nil {
		return rate, err
	}
	if rate.EffectiveFrom.IsZero() {
		rate.EffectiveFrom = now
	}
	if rate.EffectiveFrom.Before(now.Add(-time.Minute)) {
		return rate, NewHTTPError(400, "retroactive_exchange_rate", "Cannot publish retroactive exchange rates")
	}
	rate.ID = NewID("fx")
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "metering_fx", rate.Currency); err != nil {
			return err
		}
		return saveMeteringEntry(tx, rate.ID, "exchange_rate", rate.Currency, rate, time.Now().UTC())
	})
	return rate, err
}
func loadMeteringFX(tx *gorm.DB, currency string, at time.Time) (*meteringExchangeRate, error) {
	var rows []meteringEntry
	if err := tx.Where("kind = ? AND scope = ?", "exchange_rate", currency).Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	var selected *meteringExchangeRate
	for _, row := range rows {
		var rate meteringExchangeRate
		if err := json.Unmarshal([]byte(row.Payload), &rate); err != nil {
			return nil, err
		}
		if at.Before(rate.EffectiveFrom) {
			continue
		}
		if selected == nil || rate.EffectiveFrom.After(selected.EffectiveFrom) {
			copy := rate
			selected = &copy
		}
	}
	return selected, nil
}

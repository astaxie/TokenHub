package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"tokenhub/backend/internal/metering"
)

func (s *Server) registerMeteringRoutes() {
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/billing/exchange-rates", s.handleMeteringExchangeRate, s.adminMethodNotAllowed("billing", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/billing/evidence/{request_id}", s.handleMeteringEvidence, s.adminMethodNotAllowed("billing", http.MethodGet))
	s.registerMethodRoutes("/api/admin/billing/rate-cards", func(methods string) http.HandlerFunc { return s.adminMethodNotAllowed("billing", methods) },
		methodRoute{Method: http.MethodGet, Handler: s.handleMeteringRateCards}, methodRoute{Method: http.MethodPost, Handler: s.handleMeteringRateCards})
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/billing/preview", s.handleMeteringPreview, s.adminMethodNotAllowed("billing", http.MethodPost))
}

func (s *Server) handleMeteringRateCards(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	store, ok := s.store.(interface {
		PublishMeteringCard(meteringRateCard) (meteringRateCard, error)
		ListMeteringCards() ([]meteringRateCard, error)
	})
	if !ok {
		writeError(w, r, NewHTTPError(503, "metering_unavailable", "Metering persistence is unavailable"))
		return
	}
	if r.Method == http.MethodGet {
		cards, err := store.ListMeteringCards()
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"data": cards, "mode": "shadow"})
		return
	}
	var card meteringRateCard
	if err := s.decodeJSON(w, r, &card); err != nil {
		writeError(w, r, err)
		return
	}
	if err := card.validate(); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_rate_card", err.Error()))
		return
	}
	saved, err := store.PublishMeteringCard(card)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "publish", "billing_rate_card", saved.ID, "", saved)
	writeJSON(w, 201, map[string]any{"data": saved, "mode": "shadow"})
}

func (s *Server) handleMeteringPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "billing", r.Method); !ok {
		return
	}
	var request struct {
		Card         meteringRateCard `json:"card"`
		At           time.Time        `json:"at"`
		Usage        Usage            `json:"usage"`
		ExchangeRate string           `json:"exchange_rate"`
	}
	if err := s.decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	if err := request.Card.validate(); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_rate_card", err.Error()))
		return
	}
	if request.At.IsZero() {
		request.At = time.Now().UTC()
	}
	units, err := meteringUnits(request.Usage)
	if err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_usage", err.Error()))
		return
	}
	snapshot := request.Card.at(request.At)
	charge, err := metering.Price(snapshot.Rates, units, snapshot.Currency, request.ExchangeRate)
	if err != nil {
		writeError(w, r, NewHTTPError(400, "pricing_incomplete", err.Error()))
		return
	}
	writeJSON(w, 200, map[string]any{"snapshot": snapshot, "charge": charge})
}

func (s *GormStore) PublishMeteringCard(card meteringRateCard) (meteringRateCard, error) {
	if err := card.validate(); err != nil {
		return card, err
	}
	now, clockErr := s.databaseNow(s.db)
	if clockErr != nil {
		return card, clockErr
	}
	immediate := card.EffectiveFrom.IsZero()
	card.Revision = 1
	card.ID = NewID("rate")
	if card.EffectiveFrom.IsZero() {
		card.EffectiveFrom = now
	}
	if card.EffectiveFrom.Before(now.Add(-time.Minute)) {
		return card, NewHTTPError(400, "retroactive_rate_card", "Cannot publish retroactive prices")
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "metering_rate", card.Kind+":"+card.Target); err != nil {
			return err
		}
		var rows []meteringEntry
		if err := tx.Where("kind = ? AND scope = ?", "rate_card", card.Kind+":"+card.Target).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			var existing meteringRateCard
			if err := json.Unmarshal([]byte(row.Payload), &existing); err != nil {
				return err
			}
			if existing.Revision >= card.Revision {
				card.Revision = existing.Revision + 1
			}
			if !immediate && existing.EffectiveFrom.Equal(card.EffectiveFrom) {
				return NewHTTPError(409, "rate_card_conflict", "An existing version has the same effective time")
			}
		}
		return saveMeteringEntry(tx, card.ID, "rate_card", card.Kind+":"+card.Target, card, now)
	})
	return card, err
}
func (s *GormStore) ListMeteringCards() ([]meteringRateCard, error) {
	var rows []meteringEntry
	if err := s.db.Where("kind = ?", "rate_card").Order("created_at DESC").Limit(500).Find(&rows).Error; err != nil {
		return nil, err
	}
	cards := make([]meteringRateCard, 0, len(rows))
	for _, row := range rows {
		var card meteringRateCard
		if err := json.Unmarshal([]byte(row.Payload), &card); err != nil {
			return nil, fmt.Errorf("decode rate card %s: %w", strings.TrimSpace(row.ID), err)
		}
		cards = append(cards, card)
	}
	return cards, nil
}

func (s *Server) handleMeteringEvidence(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "billing", r.Method); !ok {
		return
	}
	store, ok := s.store.(interface {
		MeteringEvidence(string) ([]meteringEvidenceRow, error)
	})
	if !ok {
		writeError(w, r, NewHTTPError(503, "metering_unavailable", "Metering persistence is unavailable"))
		return
	}
	rows, err := store.MeteringEvidence(r.PathValue("request_id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if len(rows) == 0 {
		writeError(w, r, NewHTTPError(404, "metering_evidence_not_found", "Billing evidence not found"))
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows, "mode": "shadow"})
}

type meteringEvidenceRow struct {
	Kind string          `json:"kind"`
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data"`
}

func (s *GormStore) MeteringEvidence(requestID string) ([]meteringEvidenceRow, error) {
	var entries []meteringEntry
	if err := s.db.Where("scope = ? AND kind IN ?", requestID, []string{"admission", "attempt_prepared", "shadow_settlement"}).Order("created_at ASC, id ASC").Limit(500).Find(&entries).Error; err != nil {
		return nil, err
	}
	rows := make([]meteringEvidenceRow, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, meteringEvidenceRow{Kind: entry.Kind, At: entry.CreatedAt, Data: json.RawMessage(entry.Payload)})
	}
	return rows, nil
}

package server

import (
	"context"
	"encoding/json"
	"time"
)

type allowanceAlertDelivery struct {
	AlertID   string `json:"alert_id"`
	ChannelID string `json:"channel_id"`
}

// A durable claim precedes delivery. A crash or unknown remote outcome never
// triggers an automatic duplicate message; the existing alert console can retry.
func (s *Server) deliverAllowanceAlerts(ctx context.Context) {
	store, ok := s.store.(*GormStore)
	if !ok {
		return
	}
	var rows []providerAllowanceEvent
	if store.db.WithContext(ctx).Where("kind = ? AND state = ?", "alert_delivery", "queued").Order("created_at").Limit(100).Find(&rows).Error != nil {
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		var payload allowanceAlertDelivery
		if json.Unmarshal([]byte(row.Details), &payload) != nil {
			continue
		}
		claimed := store.db.WithContext(ctx).Model(&providerAllowanceEvent{}).Where("id = ? AND state = ?", row.ID, "queued").Updates(map[string]any{"state": "pending", "updated_at": time.Now().UTC()})
		if claimed.Error != nil || claimed.RowsAffected != 1 {
			continue
		}
		cfg, err := store.providerAllowanceConfig(ctx, row.ResourceID)
		selected := false
		for _, id := range cfg.Policy.Alerts.ChannelIDs {
			if id == payload.ChannelID {
				selected = true
			}
		}
		if err != nil || !cfg.Policy.Alerts.Enabled || !selected {
			_ = store.db.WithContext(ctx).Model(&row).Update("state", "cancelled").Error
			continue
		}
		deliveryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		delivery, err := s.deliverAlert(deliveryCtx, payload.AlertID, payload.ChannelID)
		cancel()
		state := "delivered"
		if err != nil {
			state = "failed"
		} else if delivery.Status != "success" && delivery.Status != "sent" {
			state = delivery.Status
		}
		_ = store.db.WithContext(ctx).Model(&row).Updates(map[string]any{"state": state, "updated_at": time.Now().UTC()}).Error
	}
}

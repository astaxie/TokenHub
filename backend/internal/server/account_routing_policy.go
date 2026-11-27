package server

import (
	"net/http"
	"strings"

	"gorm.io/gorm"
	"tokenhub/backend/internal/accountrouting"
)

const accountRoutingMetadataKey = "tokenhub_account_strategy"

func modelAccountStrategy(model Model) accountrouting.Strategy {
	strategy := accountrouting.Strategy(model.Metadata[accountRoutingMetadataKey])
	if !accountrouting.Valid(strategy) {
		return accountrouting.Default
	}
	return strategy
}

func saveAccountRoutingPolicy(tx *gorm.DB, model *Model, value *string) error {
	if value == nil {
		return nil
	}
	strategy := accountrouting.Strategy(strings.TrimSpace(*value))
	if !accountrouting.Valid(strategy) {
		return NewHTTPError(http.StatusBadRequest, "invalid_account_strategy", "Unknown account routing strategy")
	}
	// Read metadata after the semantic policy update in the same transaction.
	var current Model
	if err := tx.First(&current, "name = ?", model.Name).Error; err != nil {
		return err
	}
	if current.Metadata == nil {
		current.Metadata = map[string]string{}
	}
	if strategy == accountrouting.Default {
		delete(current.Metadata, accountRoutingMetadataKey)
	} else {
		current.Metadata[accountRoutingMetadataKey] = string(strategy)
	}
	return tx.Model(&current).Select("Metadata").Updates(&current).Error
}

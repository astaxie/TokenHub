package server

import (
	"math"
	"strings"

	"gorm.io/gorm"
)

// Neumaier summation retains the low-order part of a binary64 amount when
// contributions of very different sizes are added and later corrected.
type compensatedOverviewAmount struct{ sum, correction float64 }

func (a *compensatedOverviewAmount) add(value float64) {
	next := a.sum + value
	if math.Abs(a.sum) >= math.Abs(value) {
		a.correction += (a.sum - next) + value
	} else {
		a.correction += (value - next) + a.sum
	}
	a.sum = next
}

// The row set is bounded by the backfill batch. Counts and tokens are grouped
// in SQL; SQLite amounts retain both components instead of losing small
// contributions inside SUM before a subsequent deletion.
func projectSQLiteBatchAmounts(tx *gorm.DB, ids []string) error {
	var rows []struct {
		ProjectID, APIKeyID, AttributedUserID, ModelName, ProviderID, ProviderResourceID string
		CostUSD                                                                          float64
	}
	if err := tx.Table("usage_records").Select("project_id", "api_key_id", "attributed_user_id", "model_name", "provider_id", "provider_resource_id", "cost_usd").Where("id IN ?", ids).Order("id").Scan(&rows).Error; err != nil {
		return err
	}
	tuples := make(map[[6]string]compensatedOverviewAmount)
	var global compensatedOverviewAmount
	for _, row := range rows {
		key := [6]string{row.ProjectID, row.APIKeyID, row.AttributedUserID, row.ModelName, row.ProviderID, row.ProviderResourceID}
		amount := tuples[key]
		amount.add(row.CostUSD)
		tuples[key] = amount
		global.add(row.CostUSD)
	}
	if err := addSQLiteProjectedAmount(tx, "global", [6]string{}, global); err != nil {
		return err
	}
	for key, amount := range tuples {
		if err := addSQLiteProjectedAmount(tx, "tuple", key, amount); err != nil {
			return err
		}
	}
	return nil
}

func addSQLiteProjectedAmount(tx *gorm.DB, scope string, key [6]string, amount compensatedOverviewAmount) error {
	if math.IsNaN(amount.sum) || math.IsInf(amount.sum, 0) || math.IsNaN(amount.correction) || math.IsInf(amount.correction, 0) {
		return tx.Exec("UPDATE overview_projection_progress SET overflowed = 1 WHERE source = 'usage_records'").Error
	}
	columns := "scope, source, shard, " + strings.Join(overviewDimensionColumns, ", ") + ", " + strings.Join(overviewMetricColumns, ", ")
	args := []any{scope, "usage_records", int64(0)}
	for _, dimension := range key {
		args = append(args, dimension)
	}
	for _, metric := range overviewMetricColumns {
		var value any = int64(0)
		if metric == "estimated_cost_usd" {
			value = amount.sum
		}
		if metric == "cost_compensation" {
			value = amount.correction
		}
		args = append(args, value)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(args)), ", ")
	statement := "INSERT INTO overview_totals (" + columns + ") VALUES (" + placeholders + ") ON CONFLICT (scope, source, " + strings.Join(overviewDimensionColumns, ", ") + ", shard) DO UPDATE SET " + strings.Join(overviewMetricUpdates("sqlite"), ", ")
	return tx.Exec(statement, args...).Error
}

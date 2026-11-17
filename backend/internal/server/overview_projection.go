package server

import (
	"context"
	"fmt"
	"gorm.io/gorm/clause"
	"log"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
	"tokenhub/backend/internal/dbschema"
)

type OverviewProgress struct {
	useRaw    bool
	Ready     bool  `json:"ready"`
	Processed int64 `json:"processed"`
}

// Lifetime tuples preserve OR permissions without counting overlapping grants twice.
func (s *GormStore) QueryOverviewSummary(ctx context.Context, query UsageSummaryQuery) (summary UsageSummary, progress OverviewProgress, err error) {
	err = s.withAnalyticsReadSnapshot(ctx, func(tx *gorm.DB) error {
		snapshot := *s
		snapshot.analyticsDB = tx
		var queryErr error
		summary, progress, queryErr = snapshot.queryOverviewSummarySnapshot(ctx, query)
		return queryErr
	})
	return summary, progress, err
}

func (s *GormStore) queryOverviewSummarySnapshot(ctx context.Context, query UsageSummaryQuery) (UsageSummary, OverviewProgress, error) {
	progress, err := s.overviewProgress(ctx)
	if err != nil || !progress.Ready {
		return UsageSummary{}, progress, err
	}
	if s.dbDriver == "sqlite" && progress.useRaw {
		summary, err := s.querySQLiteOverviewRawSummary(ctx, query)
		return summary, progress, err
	}
	var result UsageSummary
	for _, source := range []struct {
		name  string
		scope UsageSummaryScope
	}{
		{"usage_records", query.UsageRecords}, {"request_logs", query.RequestLogs},
	} {
		if !source.scope.CreatedAtFrom.IsZero() || !source.scope.CreatedAtBefore.IsZero() {
			return UsageSummary{}, progress, fmt.Errorf("overview summary requires a lifetime scope")
		}
		scoped := s.analyticsDB.WithContext(ctx).Table("overview_totals").Where("source = ?", source.name)
		if source.scope.Global {
			scoped = scoped.Where("scope = ?", "global")
		} else {
			scoped = scoped.Where("scope = ?", "tuple")
			scoped, err = applyUsageSummaryScope(scoped, s.dbDriver, source.scope)
			if err != nil {
				return UsageSummary{}, progress, err
			}
		}
		var part UsageSummary
		if err := scoped.Select(`COALESCE(SUM(request_count), 0) AS request_count,
            COALESCE(SUM(usage_record_count), 0) AS usage_record_count,
            COALESCE(SUM(input_tokens), 0) AS input_tokens,
            COALESCE(SUM(cached_input_tokens), 0) AS cached_input_tokens,
            COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens,
            COALESCE(SUM(output_tokens), 0) AS output_tokens,
            COALESCE(SUM(reasoning_tokens), 0) AS reasoning_tokens,
            COALESCE(SUM(total_tokens), 0) AS total_tokens,
            COALESCE(SUM(estimated_cost_usd + cost_compensation), 0) AS estimated_cost_usd,
            COALESCE(SUM(errors), 0) AS errors`).Scan(&part).Error; err != nil {
			return UsageSummary{}, progress, err
		}
		result.RequestCount += part.RequestCount
		result.UsageRecordCount += part.UsageRecordCount
		result.InputTokens += part.InputTokens
		result.CachedInputTokens += part.CachedInputTokens
		result.CacheWriteTokens += part.CacheWriteTokens
		result.OutputTokens += part.OutputTokens
		result.ReasoningTokens += part.ReasoningTokens
		result.TotalTokens += part.TotalTokens
		result.EstimatedCostUSD += part.EstimatedCostUSD
		result.Errors += part.Errors
	}
	return result, progress, nil
}

func (s *GormStore) overviewProgress(ctx context.Context) (OverviewProgress, error) {
	progress := OverviewProgress{Ready: true}
	var sources []struct {
		Source     string
		Processed  int64
		Ready      int
		Overflowed int
	}
	if err := s.analyticsDB.WithContext(ctx).Table("overview_projection_progress").Find(&sources).Error; err != nil {
		return progress, err
	}
	if len(sources) != 2 {
		return progress, fmt.Errorf("overview progress state is incomplete")
	}
	for _, source := range sources {
		progress.Processed += source.Processed
		progress.useRaw = progress.useRaw || source.Overflowed != 0
		if source.Ready != 0 {
			continue
		}
		if source.Source != "usage_records" && source.Source != "request_logs" {
			return progress, fmt.Errorf("unknown overview projection source")
		}
		var pending []string
		if err := s.analyticsDB.WithContext(ctx).Table(source.Source).Select("id").Where("overview_projected = 0").Limit(1).Scan(&pending).Error; err != nil {
			return progress, err
		}
		if len(pending) > 0 {
			progress.Ready = false
		}
	}
	return progress, nil
}

func (s *GormStore) overviewBackfillExecutor(options ...dbschema.BackfillOption) (*dbschema.BackfillExecutor, error) {
	db, err := s.db.DB()
	if err != nil {
		return nil, err
	}
	registry := make([]dbschema.Backfill, 0, 2)
	for _, table := range []string{"usage_records", "request_logs"} {
		registry = append(registry, dbschema.Backfill{
			ID: "overview-v1-" + table, Mode: dbschema.BackfillOnline, BatchLimit: 1000,
			RunBatch: func(ctx context.Context, _ dbschema.Execer, limit int) (int64, error) {
				return s.backfillOverviewBatch(ctx, table, limit)
			},
		})
	}
	return dbschema.NewBackfillExecutor(db, dbschema.Dialect(s.dbDriver), registry, options...)
}

func (s *GormStore) backfillOverviewBatch(ctx context.Context, table string, limit int) (int64, error) {
	var remaining int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Acquire SQLite's write reservation before establishing a read
		// snapshot. A deferred read-to-write upgrade cannot wait for a peer.
		if s.dbDriver == "sqlite" {
			if err := tx.Exec("UPDATE overview_projection_progress SET processed = processed WHERE source = ?", table).Error; err != nil {
				return err
			}
		}
		var ids []string
		pending := tx.Table(table).Select("id").Where("overview_projected = 0").Order("id").Limit(limit)
		if s.dbDriver == "postgres" {
			pending = pending.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := pending.Scan(&ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return tx.Exec("UPDATE overview_projection_progress SET ready = 1 WHERE source = ?", table).Error
		}
		if s.dbDriver == "sqlite" && table == "usage_records" {
			if err := markSQLiteBatchPrecisionRisk(tx, ids); err != nil {
				return err
			}
		}
		values := overviewRowMetricValues(table, table)
		aggregates := make([]string, len(overviewMetricColumns))
		updates := overviewMetricUpdates(s.dbDriver)
		for i, column := range overviewMetricColumns {
			expression := values[column]
			if s.dbDriver == "sqlite" && (column == "estimated_cost_usd" || column == "cost_compensation") {
				expression = "0"
			}
			if expression == "" {
				expression = "0"
			}
			aggregates[i] = "COALESCE(SUM(" + expression + "), 0)"
		}
		columns := "scope, source, " + strings.Join(overviewDimensionColumns, ", ") + ", " + strings.Join(overviewMetricColumns, ", ")
		metrics := strings.Join(aggregates, ", ")
		for _, scope := range []string{"global", "tuple"} {
			dimensionValues := make([]string, len(overviewDimensionColumns))
			for i := range dimensionValues {
				dimensionValues[i] = "''"
			}
			dimensions := strings.Join(dimensionValues, ", ")
			group := ""
			if scope == "tuple" {
				for i, column := range overviewDimensionColumns {
					dimensionValues[i] = "COALESCE(" + column + ", '')"
				}
				dimensions = strings.Join(dimensionValues, ", ")
				group = " GROUP BY " + dimensions
			}
			statement := fmt.Sprintf("INSERT INTO overview_totals (%s) SELECT ?, ?, %s, %s FROM %s WHERE id IN ?%s ON CONFLICT (scope, source, project_id, api_key_id, attributed_user_id, model_name, provider_id, provider_resource_id) DO UPDATE SET %s", columns, dimensions, metrics, table, group, strings.Join(updates, ", "))
			if err := tx.Exec(statement, scope, table, ids).Error; err != nil {
				return err
			}
		}
		if s.dbDriver == "sqlite" && table == "usage_records" {
			if err := projectSQLiteBatchAmounts(tx, ids); err != nil {
				return err
			}
		}
		if s.dbDriver == "sqlite" {
			if err := tx.Exec(overviewSQLiteOverflowSQL(table)).Error; err != nil {
				return err
			}
		}
		// Marker 2 identifies a batch already added by the grouped statements.
		// Row triggers skip 0 -> 2 but maintain every subsequent update/delete.
		if err := tx.Table(table).Where("id IN ?", ids).UpdateColumn("overview_projected", 2).Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE overview_projection_progress SET processed = processed + ? WHERE source = ?", len(ids), table).Error; err != nil {
			return err
		}

		// The pending index is a durable cursor even across deletes and arbitrary IDs.
		remaining = 1
		return nil
	})
	if err != nil && s.dbDriver == "sqlite" && strings.Contains(err.Error(), "integer overflow") {
		// SQLite SUM can overflow before NUMERIC affinity promotes a result.
		// Row triggers preserve the source and let an oversized API total fail
		// independently when it is read into the int64 response contract.
		return s.backfillOverviewOverflowBatch(ctx, table, limit)
	}
	return remaining, err
}

func (s *GormStore) backfillOverviewOverflowBatch(ctx context.Context, table string, limit int) (remaining int64, err error) {
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Exec(fmt.Sprintf("UPDATE %s SET overview_projected = 1 WHERE id IN (SELECT id FROM %s WHERE overview_projected = 0 ORDER BY id LIMIT ?)", table, table), limit)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return tx.Exec("UPDATE overview_projection_progress SET ready = 1 WHERE source = ?", table).Error
		}
		remaining = 1
		return tx.Exec("UPDATE overview_projection_progress SET processed = processed + ? WHERE source = ?", result.RowsAffected, table).Error
	})
	return remaining, err
}

func (s *Server) runOverviewBackfill(ctx context.Context) {
	store, ok := s.store.(*GormStore)
	if !ok {
		return
	}
	executor, err := store.overviewBackfillExecutor()
	if err != nil {
		log.Printf("[tokenhub] initialize overview backfill failed")
		return
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = executor.ReleaseOnlineLeases(releaseCtx)
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		batchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		progress, err := executor.RunOnlineBatch(batchCtx)
		cancel()
		if ctx.Err() != nil || isClosedDatabaseError(err) {
			return
		}
		if err != nil {
			log.Printf("[tokenhub] overview backfill will retry")
		}
		if err == nil && len(progress) == 0 {
			states, statusErr := executor.Status(ctx)
			complete := statusErr == nil
			for _, state := range states {
				if state.Mode == dbschema.BackfillOnline && state.State != dbschema.BackfillStateComplete {
					complete = false
				}
			}
			if complete {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func markSQLiteBatchPrecisionRisk(tx *gorm.DB, ids []string) error {
	var costs struct{ Previous, Delta, MinAbs, MaxAbs float64 }
	err := tx.Raw(`SELECT COALESCE((SELECT estimated_cost_usd FROM overview_totals WHERE scope = 'global' AND source = 'usage_records'), 0) AS previous,
        COALESCE(SUM(cost_usd), 0) AS delta,
        COALESCE(MIN(CASE WHEN cost_usd <> 0 THEN ABS(cost_usd) END), 0) AS min_abs,
        COALESCE(MAX(ABS(cost_usd)), 0) AS max_abs
        FROM usage_records WHERE id IN ?`, ids).Scan(&costs).Error
	if err != nil {
		return err
	}
	lostIncoming := costs.Delta != 0 && costs.Previous+costs.Delta == costs.Previous
	lostPrevious := math.Abs(((costs.Previous+costs.Delta)-costs.Delta)-costs.Previous) > math.Abs(costs.Previous)*8.881784197001252e-16
	mixedMagnitudes := costs.MinAbs > 0 && costs.MaxAbs+costs.MinAbs == costs.MaxAbs
	if lostIncoming || lostPrevious || mixedMagnitudes || math.IsInf(costs.Delta, 0) || math.IsNaN(costs.Delta) {
		return tx.Exec("UPDATE overview_projection_progress SET overflowed = 1 WHERE source = 'usage_records'").Error
	}
	return nil
}

func (s *GormStore) querySQLiteOverviewRawSummary(ctx context.Context, query UsageSummaryQuery) (UsageSummary, error) {
	summary, err := s.QueryUsageSummary(ctx, query)
	if err != nil {
		return UsageSummary{}, err
	}
	scoped, err := applyUsageSummaryScope(s.analyticsDB.WithContext(ctx).Model(&UsageRecord{}), s.dbDriver, query.UsageRecords)
	if err != nil {
		return UsageSummary{}, err
	}
	var amount struct {
		Total   *float64
		NonNull int64
	}
	if err := scoped.Select("SUM(cost_usd) AS total, COUNT(cost_usd) AS non_null").Scan(&amount).Error; err != nil {
		return UsageSummary{}, err
	}
	// SQLite may return NULL for an indeterminate floating SUM. Distinguish
	// that condition from an empty or entirely NULL source set.
	if amount.NonNull > 0 && (amount.Total == nil || math.IsInf(*amount.Total, 0) || math.IsNaN(*amount.Total)) {
		return UsageSummary{}, fmt.Errorf("overview amount is outside the response range")
	}
	if amount.Total != nil {
		summary.EstimatedCostUSD = *amount.Total
	}
	return summary, nil
}

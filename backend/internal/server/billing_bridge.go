package server

import (
	"context"
	"errors"
	"time"

	"tokenhub/backend/internal/billing"
)

// billingStoreBridge is a temporary adapter between the W03 domain seam and
// the server-owned persistence model. W04 will replace it with a persistence
// package implementation and remove the server model entirely.
type billingStoreBridge struct {
	store BillingStore
}

var _ billing.Store = (*billingStoreBridge)(nil)

func (b *billingStoreBridge) CreateBillingConnector(connector billing.Connector) (billing.Connector, error) {
	result, err := b.store.CreateBillingConnector(serverBillingConnector(connector))
	return domainBillingConnector(result), domainBillingStoreError(err)
}

func (b *billingStoreBridge) ListBillingConnectors() []billing.Connector {
	return domainBillingConnectors(b.store.ListBillingConnectors())
}

func (b *billingStoreBridge) GetBillingConnector(id string, includeCredentials bool) (billing.Connector, error) {
	result, err := b.store.GetBillingConnector(id, includeCredentials)
	return domainBillingConnector(result), domainBillingStoreError(err)
}

func (b *billingStoreBridge) UpdateBillingConnector(id string, connector billing.Connector) (billing.Connector, error) {
	result, err := b.store.UpdateBillingConnector(id, serverBillingConnector(connector))
	return domainBillingConnector(result), domainBillingStoreError(err)
}

func (b *billingStoreBridge) DeleteBillingConnector(id string) error {
	return domainBillingStoreError(b.store.DeleteBillingConnector(id))
}

func (b *billingStoreBridge) StartBillingSyncRun(run billing.SyncRun) (billing.SyncRun, error) {
	result, err := b.store.StartBillingSyncRun(serverBillingSyncRun(run))
	return domainBillingSyncRun(result), domainBillingStoreError(err)
}

func (b *billingStoreBridge) SaveBillingPage(connectorID, checkpoint string, records []billing.Record) (int, int, error) {
	inserted, updated, err := b.store.SaveBillingPage(connectorID, checkpoint, serverBillingRecords(records))
	return inserted, updated, domainBillingStoreError(err)
}

func (b *billingStoreBridge) FinishBillingSyncRun(run billing.SyncRun) (billing.SyncRun, error) {
	result, err := b.store.FinishBillingSyncRun(serverBillingSyncRun(run))
	return domainBillingSyncRun(result), domainBillingStoreError(err)
}

func (b *billingStoreBridge) ListBillingRecords(connectorID string, limit int) []billing.Record {
	return domainBillingRecords(b.store.ListBillingRecords(connectorID, limit))
}

func (b *billingStoreBridge) ListBillingSyncRuns(connectorID string, limit int) []billing.SyncRun {
	return domainBillingSyncRuns(b.store.ListBillingSyncRuns(connectorID, limit))
}

func (b *billingStoreBridge) ListDueBillingConnectors(now time.Time, limit int) []billing.Connector {
	return domainBillingConnectors(b.store.ListDueBillingConnectors(now, limit))
}

func (b *billingStoreBridge) RecordScheduledBillingAudit(run billing.SyncRun) {
	b.store.RecordScheduledBillingAudit(serverBillingSyncRun(run))
}

type billingAdapterBridge struct {
	adapter BillingAdapter
}

var _ billing.Adapter = (*billingAdapterBridge)(nil)

func (b *billingAdapterBridge) Fetch(ctx context.Context, connector billing.Connector, request billing.FetchRequest) (billing.FetchPage, error) {
	page, err := b.adapter.Fetch(ctx, serverBillingConnector(connector), serverBillingFetchRequest(request))
	if err != nil {
		return billing.FetchPage{}, domainBillingAdapterError(err)
	}
	return billing.FetchPage{Records: domainBillingRecords(page.Records), NextCursor: page.NextCursor}, nil
}

func domainBillingAdapterError(err error) error {
	var upstream *billingUpstreamError
	if errors.As(err, &upstream) {
		return billing.NewAdapterFailure(err, billing.ErrorUpstream,
			defaultString(upstream.code, "billing_upstream_error"),
			defaultString(upstream.message, "Billing source request failed"),
			upstream.retryable, upstream.retryAfter)
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		kind := billing.ErrorInvalidInput
		if httpErr.Status >= 500 {
			kind = billing.ErrorUpstream
		} else if httpErr.Status == 404 {
			kind = billing.ErrorNotFound
		} else if httpErr.Status == 409 {
			kind = billing.ErrorConflict
		} else if httpErr.Status == 429 {
			kind = billing.ErrorRateLimited
		}
		return billing.NewAdapterFailure(err, kind, httpErr.Code, httpErr.Message, httpErr.Status >= 500, 0)
	}
	return err
}

func domainBillingStoreError(err error) error {
	if err == nil {
		return nil
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		return err
	}
	kind := billing.ErrorInvalidInput
	switch {
	case httpErr.Status >= 500:
		kind = billing.ErrorUpstream
	case httpErr.Status == 404:
		kind = billing.ErrorNotFound
	case httpErr.Status == 409:
		kind = billing.ErrorConflict
	case httpErr.Status == 429:
		kind = billing.ErrorRateLimited
	}
	return billing.WrapError(err, kind, httpErr.Code, httpErr.Message)
}

func domainBillingConnector(connector BillingConnector) billing.Connector {
	return billing.Connector{
		ID: connector.ID, Name: connector.Name, Type: connector.Type, BaseURL: connector.BaseURL,
		Status: connector.Status, ScheduleIntervalMinutes: connector.ScheduleIntervalMinutes,
		Config: billingCloneStringMap(connector.Config), CredentialCiphertext: connector.CredentialCiphertext,
		Credentials: billingCloneStringMap(connector.Credentials), CredentialsConfigured: connector.CredentialsConfigured,
		CredentialFields: append([]string(nil), connector.CredentialFields...), Checkpoint: connector.Checkpoint,
		LastSyncedThrough: cloneTimePointer(connector.LastSyncedThrough), LastSyncStatus: connector.LastSyncStatus,
		LastSyncMessage: connector.LastSyncMessage, LastSyncAt: cloneTimePointer(connector.LastSyncAt),
		NextSyncAt: cloneTimePointer(connector.NextSyncAt), CreatedAt: connector.CreatedAt, UpdatedAt: connector.UpdatedAt,
	}
}

func serverBillingConnector(connector billing.Connector) BillingConnector {
	return BillingConnector{
		ID: connector.ID, Name: connector.Name, Type: connector.Type, BaseURL: connector.BaseURL,
		Status: connector.Status, ScheduleIntervalMinutes: connector.ScheduleIntervalMinutes,
		Config: billingCloneStringMap(connector.Config), CredentialCiphertext: connector.CredentialCiphertext,
		Credentials: billingCloneStringMap(connector.Credentials), CredentialsConfigured: connector.CredentialsConfigured,
		CredentialFields: append([]string(nil), connector.CredentialFields...), Checkpoint: connector.Checkpoint,
		LastSyncedThrough: cloneTimePointer(connector.LastSyncedThrough), LastSyncStatus: connector.LastSyncStatus,
		LastSyncMessage: connector.LastSyncMessage, LastSyncAt: cloneTimePointer(connector.LastSyncAt),
		NextSyncAt: cloneTimePointer(connector.NextSyncAt), CreatedAt: connector.CreatedAt, UpdatedAt: connector.UpdatedAt,
	}
}

func domainBillingConnectors(connectors []BillingConnector) []billing.Connector {
	result := make([]billing.Connector, len(connectors))
	for index, connector := range connectors {
		result[index] = domainBillingConnector(connector)
	}
	return result
}

func domainBillingRecord(record BillingRecord) billing.Record {
	return billing.Record{
		ID: record.ID, ConnectorID: record.ConnectorID, ExternalID: record.ExternalID, SourceType: record.SourceType,
		AccountID: record.AccountID, Service: record.Service, Product: record.Product, Model: record.Model,
		Currency: record.Currency, GrossAmount: record.GrossAmount, DiscountAmount: record.DiscountAmount,
		TaxAmount: record.TaxAmount, RefundAmount: record.RefundAmount, NetAmount: record.NetAmount,
		UsageQuantity: record.UsageQuantity, UsageUnit: record.UsageUnit, UsageStartAt: record.UsageStartAt,
		UsageEndAt: record.UsageEndAt, SourceTimezone: record.SourceTimezone, BillingPeriod: record.BillingPeriod,
		ExternalRequestID: record.ExternalRequestID, RawSnapshotID: record.RawSnapshotID,
		Metadata: billingCloneStringMap(record.Metadata), RawPayload: record.RawPayload,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func serverBillingRecord(record billing.Record) BillingRecord {
	return BillingRecord{
		ID: record.ID, ConnectorID: record.ConnectorID, ExternalID: record.ExternalID, SourceType: record.SourceType,
		AccountID: record.AccountID, Service: record.Service, Product: record.Product, Model: record.Model,
		Currency: record.Currency, GrossAmount: record.GrossAmount, DiscountAmount: record.DiscountAmount,
		TaxAmount: record.TaxAmount, RefundAmount: record.RefundAmount, NetAmount: record.NetAmount,
		UsageQuantity: record.UsageQuantity, UsageUnit: record.UsageUnit, UsageStartAt: record.UsageStartAt,
		UsageEndAt: record.UsageEndAt, SourceTimezone: record.SourceTimezone, BillingPeriod: record.BillingPeriod,
		ExternalRequestID: record.ExternalRequestID, RawSnapshotID: record.RawSnapshotID,
		Metadata: billingCloneStringMap(record.Metadata), RawPayload: record.RawPayload,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func domainBillingRecords(records []BillingRecord) []billing.Record {
	result := make([]billing.Record, len(records))
	for index, record := range records {
		result[index] = domainBillingRecord(record)
	}
	return result
}

func serverBillingRecords(records []billing.Record) []BillingRecord {
	result := make([]BillingRecord, len(records))
	for index, record := range records {
		result[index] = serverBillingRecord(record)
	}
	return result
}

func domainBillingSyncRun(run BillingSyncRun) billing.SyncRun {
	return billing.SyncRun{
		ID: run.ID, ConnectorID: run.ConnectorID, Trigger: run.Trigger, Status: run.Status,
		RangeStart: run.RangeStart, RangeEnd: run.RangeEnd, CursorStart: run.CursorStart, CursorEnd: run.CursorEnd,
		PagesFetched: run.PagesFetched, Attempts: run.Attempts, RecordsSeen: run.RecordsSeen,
		RecordsInserted: run.RecordsInserted, RecordsUpdated: run.RecordsUpdated, ErrorCode: run.ErrorCode,
		ErrorMessage: run.ErrorMessage, StartedAt: run.StartedAt, FinishedAt: cloneTimePointer(run.FinishedAt),
	}
}

func serverBillingSyncRun(run billing.SyncRun) BillingSyncRun {
	return BillingSyncRun{
		ID: run.ID, ConnectorID: run.ConnectorID, Trigger: run.Trigger, Status: run.Status,
		RangeStart: run.RangeStart, RangeEnd: run.RangeEnd, CursorStart: run.CursorStart, CursorEnd: run.CursorEnd,
		PagesFetched: run.PagesFetched, Attempts: run.Attempts, RecordsSeen: run.RecordsSeen,
		RecordsInserted: run.RecordsInserted, RecordsUpdated: run.RecordsUpdated, ErrorCode: run.ErrorCode,
		ErrorMessage: run.ErrorMessage, StartedAt: run.StartedAt, FinishedAt: cloneTimePointer(run.FinishedAt),
	}
}

func domainBillingSyncRuns(runs []BillingSyncRun) []billing.SyncRun {
	result := make([]billing.SyncRun, len(runs))
	for index, run := range runs {
		result[index] = domainBillingSyncRun(run)
	}
	return result
}

func serverBillingFetchRequest(request billing.FetchRequest) BillingFetchRequest {
	return BillingFetchRequest{From: request.From, To: request.To, Cursor: request.Cursor, PageSize: request.PageSize}
}

func billingCloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

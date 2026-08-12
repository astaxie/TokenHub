package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type AliyunBillingAdapter struct {
	Client *http.Client
	Now    func() time.Time
}

func (a AliyunBillingAdapter) Fetch(ctx context.Context, connector BillingConnector, request BillingFetchRequest) (BillingFetchPage, error) {
	accessKeyID := strings.TrimSpace(connector.Credentials["access_key_id"])
	accessKeySecret := strings.TrimSpace(connector.Credentials["access_key_secret"])
	if accessKeyID == "" || accessKeySecret == "" {
		return BillingFetchPage{}, NewHTTPError(http.StatusBadRequest, "billing_credentials_required", "Aliyun access_key_id and access_key_secret credentials are required")
	}
	locationName := defaultString(connector.Config["source_timezone"], "Asia/Shanghai")
	location, err := time.LoadLocation(locationName)
	if err != nil {
		return BillingFetchPage{}, NewHTTPError(http.StatusBadRequest, "invalid_billing_timezone", "Billing connector source_timezone is invalid")
	}
	cycle, page, err := aliyunBillingCursor(request.Cursor, request.From, location)
	if err != nil {
		return BillingFetchPage{}, err
	}

	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}
	values := url.Values{
		"Format":           {"JSON"},
		"Version":          {"2017-12-14"},
		"AccessKeyId":      {accessKeyID},
		"SignatureMethod":  {"HMAC-SHA1"},
		"Timestamp":        {now.Format("2006-01-02T15:04:05Z")},
		"SignatureVersion": {"1.0"},
		"SignatureNonce":   {NewID("billing")},
		"Action":           {"QueryInstanceBill"},
		"BillingCycle":     {cycle},
		"PageNum":          {strconv.Itoa(page)},
		"PageSize":         {strconv.Itoa(request.PageSize)},
		"IsBillingItem":    {"true"},
	}
	if productCode := strings.TrimSpace(connector.Config["product_code"]); productCode != "" {
		values.Set("ProductCode", productCode)
	}
	values.Set("Signature", aliyunRPCSignature(values, accessKeySecret))

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, connector.BaseURL, strings.NewReader(values.Encode()))
	if err != nil {
		return BillingFetchPage{}, err
	}
	httpRequest.Header.Set("content-type", "application/x-www-form-urlencoded")
	httpRequest.Header.Set("accept", "application/json")
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return BillingFetchPage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return BillingFetchPage{}, billingResponseError(response)
	}

	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return BillingFetchPage{}, &billingUpstreamError{status: http.StatusBadGateway, code: "billing_upstream_invalid_response", message: "Aliyun returned invalid JSON"}
	}
	if code := stringValue(firstValue(payload, "Code", "code")); code != "" && !strings.EqualFold(code, "Success") {
		return BillingFetchPage{}, &billingUpstreamError{status: http.StatusBadGateway, code: "billing_upstream_rejected", message: "Aliyun rejected the billing request"}
	}
	if success, exists := firstValue(payload, "Success", "success").(bool); exists && !success {
		return BillingFetchPage{}, &billingUpstreamError{status: http.StatusBadGateway, code: "billing_upstream_rejected", message: "Aliyun rejected the billing request"}
	}
	items, total := aliyunBillingItems(payload)
	records := make([]BillingRecord, 0, len(items))
	for _, item := range items {
		record, err := normalizeAliyunBillingRecord(item, connector, location)
		if err != nil {
			return BillingFetchPage{}, err
		}
		if !billingRecordStartsInRange(record, request.From, request.To) {
			continue
		}
		records = append(records, record)
	}

	nextCursor := ""
	if total > 0 && page*request.PageSize < total {
		nextCursor = fmt.Sprintf("%s:%d", cycle, page+1)
	} else {
		cycleTime, _ := time.ParseInLocation("2006-01", cycle, location)
		nextCycle := cycleTime.AddDate(0, 1, 0)
		endCycle := request.To.In(location).Add(-time.Nanosecond).Format("2006-01")
		if nextCycle.Format("2006-01") <= endCycle {
			nextCursor = nextCycle.Format("2006-01") + ":1"
		}
	}
	return BillingFetchPage{Records: records, NextCursor: nextCursor}, nil
}

func aliyunRPCSignature(values map[string][]string, accessKeySecret string) string {
	canonical := aliyunCanonicalQuery(values)
	stringToSign := "POST&%2F&" + aliyunPercentEncode(canonical)
	mac := hmac.New(sha1.New, []byte(accessKeySecret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func aliyunCanonicalQuery(values map[string][]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "Signature" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		items := append([]string(nil), values[key]...)
		sort.Strings(items)
		for _, value := range items {
			parts = append(parts, aliyunPercentEncode(key)+"="+aliyunPercentEncode(value))
		}
	}
	return strings.Join(parts, "&")
}

func aliyunPercentEncode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	return encoded
}

func aliyunBillingCursor(cursor string, from time.Time, location *time.Location) (string, int, error) {
	if strings.TrimSpace(cursor) == "" {
		return from.In(location).Format("2006-01"), 1, nil
	}
	parts := strings.Split(cursor, ":")
	if len(parts) != 2 {
		return "", 0, NewHTTPError(http.StatusBadRequest, "invalid_billing_checkpoint", "Aliyun billing checkpoint is invalid")
	}
	if _, err := time.ParseInLocation("2006-01", parts[0], location); err != nil {
		return "", 0, NewHTTPError(http.StatusBadRequest, "invalid_billing_checkpoint", "Aliyun billing checkpoint is invalid")
	}
	page, err := strconv.Atoi(parts[1])
	if err != nil || page <= 0 {
		return "", 0, NewHTTPError(http.StatusBadRequest, "invalid_billing_checkpoint", "Aliyun billing checkpoint is invalid")
	}
	return parts[0], page, nil
}

func aliyunBillingItems(payload map[string]any) ([]map[string]any, int) {
	data, _ := firstValue(payload, "Data", "data").(map[string]any)
	container, _ := firstValue(data, "Items", "items").(map[string]any)
	rawItems, _ := firstValue(container, "Item", "item").([]any)
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		if item, ok := raw.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items, int(numberValue(firstValue(data, "TotalCount", "total_count", "total")))
}

func normalizeAliyunBillingRecord(item map[string]any, connector BillingConnector, location *time.Location) (BillingRecord, error) {
	raw, err := json.Marshal(item)
	if err != nil {
		return BillingRecord{}, err
	}
	usageStart, err := billingTimeValue(firstValue(item, "UsageStartTime", "usage_start_time", "BillingDate"), location)
	if err != nil {
		return BillingRecord{}, NewHTTPError(http.StatusBadGateway, "billing_record_invalid_time", "Aliyun billing record has an invalid usage start time")
	}
	usageEnd, err := billingTimeValue(firstValue(item, "UsageEndTime", "usage_end_time", "BillingDate"), location)
	if err != nil {
		usageEnd = usageStart
	}
	gross, err := billingDecimalValue(firstValue(item, "PretaxGrossAmount", "pretax_gross_amount", "PretaxAmount"))
	if err != nil {
		return BillingRecord{}, err
	}
	discount, err := billingDecimalAdd(
		firstValue(item, "InvoiceDiscount", "invoice_discount"),
		firstValue(item, "DeductedByCoupons", "deducted_by_coupons"),
		firstValue(item, "DeductedByCashCoupons", "deducted_by_cash_coupons"),
		firstValue(item, "DeductedByPrepaidCard", "deducted_by_prepaid_card"),
	)
	if err != nil {
		return BillingRecord{}, err
	}
	tax, err := billingDecimalValue(firstValue(item, "TaxAmount", "tax_amount", "Tax"))
	if err != nil {
		return BillingRecord{}, err
	}
	refund, err := billingDecimalValue(firstValue(item, "RefundAmount", "refund_amount"))
	if err != nil {
		return BillingRecord{}, err
	}
	paymentValue := firstValue(item, "PaymentAmount", "payment_amount")
	net, err := billingDecimalValue(paymentValue)
	if err != nil {
		return BillingRecord{}, err
	}
	if paymentValue == nil || stringValue(paymentValue) == "" {
		net = billingDecimalCalculate(gross, discount, tax, refund)
	}
	if strings.HasPrefix(net, "-") && refund == "0" {
		refund = strings.TrimPrefix(net, "-")
	}
	billingPeriod := stringValue(firstValue(item, "BillingCycle", "billing_cycle"))
	if billingPeriod == "" {
		billingPeriod = usageStart.In(location).Format("2006-01")
	}
	externalID := aliyunBillingExternalID(item, billingPeriod, usageStart, usageEnd)
	metadata := map[string]string{}
	if instanceID := stringValue(firstValue(item, "InstanceID", "instance_id")); instanceID != "" {
		metadata["instance_id"] = instanceID
	}
	return BillingRecord{
		ExternalID:        externalID,
		SourceType:        BillingConnectorAliyun,
		AccountID:         stringValue(firstValue(item, "BillAccountID", "bill_account_id", "OwnerID")),
		Service:           stringValue(firstValue(item, "ProductCode", "product_code")),
		Product:           stringValue(firstValue(item, "ProductName", "product_name", "ProductDetail")),
		Model:             stringValue(firstValue(item, "BillingItemCode", "billing_item_code", "BillingItem")),
		Currency:          strings.ToUpper(defaultString(stringValue(firstValue(item, "Currency", "currency")), defaultString(connector.Config["currency"], "CNY"))),
		GrossAmount:       gross,
		DiscountAmount:    discount,
		TaxAmount:         tax,
		RefundAmount:      refund,
		NetAmount:         net,
		UsageStartAt:      usageStart.UTC(),
		UsageEndAt:        usageEnd.UTC(),
		SourceTimezone:    location.String(),
		BillingPeriod:     billingPeriod,
		ExternalRequestID: stringValue(firstValue(item, "RequestId", "RequestID", "request_id")),
		Metadata:          metadata,
		RawPayload:        string(raw),
	}, nil
}

func billingRecordStartsInRange(record BillingRecord, from time.Time, to time.Time) bool {
	if !from.IsZero() && record.UsageStartAt.Before(from.UTC()) {
		return false
	}
	return to.IsZero() || !record.UsageStartAt.After(to.UTC())
}

func aliyunBillingExternalID(item map[string]any, billingPeriod string, usageStart time.Time, usageEnd time.Time) string {
	if externalID := stringValue(firstValue(item, "RecordID", "record_id", "BillID", "bill_id")); externalID != "" {
		return externalID
	}
	// QueryInstanceBill does not expose a line-item ID. Hash only immutable
	// dimensions so later amount corrections update the existing record.
	dimensions := []string{
		billingPeriod,
		stringValue(firstValue(item, "BillAccountID", "bill_account_id", "OwnerID")),
		stringValue(firstValue(item, "InstanceID", "instance_id")),
		stringValue(firstValue(item, "ProductCode", "product_code")),
		stringValue(firstValue(item, "ProductType", "product_type")),
		stringValue(firstValue(item, "SubscriptionType", "subscription_type")),
		stringValue(firstValue(item, "BillingItemCode", "billing_item_code", "BillingItem")),
		stringValue(firstValue(item, "Region", "region")),
		usageStart.UTC().Format(time.RFC3339Nano),
		usageEnd.UTC().Format(time.RFC3339Nano),
		stringValue(firstValue(item, "UsageUnit", "usage_unit")),
	}
	sum := sha256.Sum256([]byte(strings.Join(dimensions, "\x1f")))
	return fmt.Sprintf("aliyun:%x", sum[:])
}

func billingDecimalValue(value any) (string, error) {
	text := stringValue(value)
	if text == "" {
		return "0", nil
	}
	ratio, ok := new(big.Rat).SetString(text)
	if !ok {
		return "", NewHTTPError(http.StatusBadGateway, "billing_upstream_invalid_response", fmt.Sprintf("Invalid decimal amount from billing upstream: %q", text))
	}
	return billingRatText(ratio), nil
}

// billingDecimalAdd sums discount components. A value that is present but
// cannot be parsed is an upstream contract violation: silently skipping it
// would understate the discount, so it is rejected instead.
func billingDecimalAdd(values ...any) (string, error) {
	total := new(big.Rat)
	for _, value := range values {
		text := strings.TrimSpace(defaultString(stringValue(value), "0"))
		if text == "" {
			continue
		}
		parsed, ok := new(big.Rat).SetString(text)
		if !ok {
			return "", NewHTTPError(http.StatusBadGateway, "billing_upstream_invalid_response", fmt.Sprintf("Invalid decimal amount from billing upstream: %q", text))
		}
		total.Add(total, parsed)
	}
	return billingRatText(total), nil
}

func billingDecimalCalculate(gross string, discount string, tax string, refund string) string {
	result, _ := new(big.Rat).SetString(defaultString(gross, "0"))
	if result == nil {
		result = new(big.Rat)
	}
	for _, operation := range []struct {
		value string
		sign  int
	}{{discount, -1}, {tax, 1}, {refund, -1}} {
		value, ok := new(big.Rat).SetString(defaultString(operation.value, "0"))
		if !ok {
			continue
		}
		if operation.sign < 0 {
			result.Sub(result, value)
		} else {
			result.Add(result, value)
		}
	}
	return billingRatText(result)
}

func billingRatText(value *big.Rat) string {
	if value == nil || value.Sign() == 0 {
		return "0"
	}
	text := value.FloatString(12)
	return strings.TrimRight(strings.TrimRight(text, "0"), ".")
}

package server

import (
	"net/http"
	"sort"
	"strings"
)

const (
	providerHeaderMaxCount      = 32
	providerHeaderNameMaxBytes  = 128
	providerHeaderValueMaxBytes = 4 << 10
	providerHeadersMaxBytes     = 16 << 10
	providerHeaderMask          = "••••••••"
)

var reservedProviderHeaders = map[string]bool{
	"authorization":               true,
	"api-key":                     true,
	"x-api-key":                   true,
	"x-goog-api-key":              true,
	"openai-organization":         true,
	"openai-project":              true,
	"x-tokenhub-upstream-account": true,
	"anthropic-version":           true,
	"anthropic-beta":              true,
	"content-type":                true,
	"content-length":              true,
	"host":                        true,
	"connection":                  true,
	"cookie":                      true,
	"cookie2":                     true,
	"expect":                      true,
	"accept-encoding":             true,
	"forwarded":                   true,
	"keep-alive":                  true,
	"proxy-authenticate":          true,
	"proxy-authorization":         true,
	"proxy-connection":            true,
	"te":                          true,
	"trailer":                     true,
	"transfer-encoding":           true,
	"upgrade":                     true,
	"via":                         true,
	"x-forwarded-for":             true,
	"x-forwarded-host":            true,
	"x-forwarded-port":            true,
	"x-forwarded-proto":           true,
	"x-real-ip":                   true,
}

func normalizeProviderHeaders(headers map[string]string) (map[string]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	if len(headers) > providerHeaderMaxCount {
		return nil, NewHTTPError(http.StatusBadRequest, "provider_headers_too_many", "Too many custom request headers")
	}
	normalized := make(map[string]string, len(headers))
	seen := make(map[string]bool, len(headers))
	totalBytes := 0
	for rawName, value := range headers {
		name := strings.TrimSpace(rawName)
		if name == "" || len(name) > providerHeaderNameMaxBytes || !validHTTPHeaderName(name) {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_header_name_invalid", "Custom request header name is invalid")
		}
		lowerName := strings.ToLower(name)
		if reservedProviderHeaders[lowerName] {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_header_reserved", "Custom request header is managed by TokenHub and cannot be overridden")
		}
		canonicalName := http.CanonicalHeaderKey(name)
		if seen[canonicalName] {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_header_duplicate", "Custom request header names must be unique ignoring case")
		}
		seen[canonicalName] = true
		if value == "" {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_header_value_required", "Custom request header value cannot be empty")
		}
		if !validHTTPHeaderValue(value) {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_header_value_invalid", "Custom request header value contains invalid control characters")
		}
		if len(value) > providerHeaderValueMaxBytes {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_header_value_too_long", "Custom request header value is too long")
		}
		totalBytes += len(canonicalName) + len(value)
		if totalBytes > providerHeadersMaxBytes {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_headers_too_large", "Custom request headers exceed the total size limit")
		}
		normalized[name] = value
	}
	return normalized, nil
}

func validHTTPHeaderName(name string) bool {
	for index := 0; index < len(name); index++ {
		character := name[index]
		if ('a' <= character && character <= 'z') || ('A' <= character && character <= 'Z') ||
			('0' <= character && character <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func validHTTPHeaderValue(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '\t' || character >= 0x20 && character != 0x7f {
			continue
		}
		return false
	}
	return true
}

func mergeProviderHeaders(providerHeaders, resourceHeaders map[string]string) map[string]string {
	if len(providerHeaders) == 0 && len(resourceHeaders) == 0 {
		return nil
	}
	merged := make(map[string]string, len(providerHeaders)+len(resourceHeaders))
	for name, value := range providerHeaders {
		merged[http.CanonicalHeaderKey(name)] = value
	}
	for name, value := range resourceHeaders {
		merged[http.CanonicalHeaderKey(name)] = value
	}
	return merged
}

func mergeProviderSensitiveHeaders(
	providerHeaders map[string]string,
	providerSensitive []string,
	resourceHeaders map[string]string,
	resourceSensitive []string,
) []string {
	sensitive := make(map[string]bool)
	providerSet := sensitiveProviderHeaderSet(providerSensitive)
	resourceSet := sensitiveProviderHeaderSet(resourceSensitive)
	for name := range providerHeaders {
		canonicalName := http.CanonicalHeaderKey(name)
		if providerSet[canonicalName] {
			sensitive[canonicalName] = true
		}
	}
	for name := range resourceHeaders {
		canonicalName := http.CanonicalHeaderKey(name)
		delete(sensitive, canonicalName)
		if resourceSet[canonicalName] {
			sensitive[canonicalName] = true
		}
	}
	result := make([]string, 0, len(sensitive))
	for name := range sensitive {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func applyProviderHeaders(target http.Header, headers map[string]string) {
	for name, value := range headers {
		if reservedProviderHeaders[strings.ToLower(strings.TrimSpace(name))] {
			continue
		}
		target.Set(name, value)
	}
}

func normalizedSensitiveProviderHeaders(names []string, headers map[string]string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(names))
	normalized := make([]string, 0, len(names))
	for _, rawName := range names {
		name := http.CanonicalHeaderKey(strings.TrimSpace(rawName))
		if _, ok := providerHeaderValue(headers, name); !ok {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_sensitive_header_missing", "Sensitive header names must reference a configured custom request header")
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		normalized = append(normalized, name)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func providerHeaderValue(headers map[string]string, name string) (string, bool) {
	canonicalName := http.CanonicalHeaderKey(name)
	for candidate, value := range headers {
		if http.CanonicalHeaderKey(candidate) == canonicalName {
			return value, true
		}
	}
	return "", false
}

func providerHeaderValidationErrors(headers map[string]string) []string {
	if _, err := normalizeProviderHeaders(headers); err != nil {
		return []string{AsHTTPError(err).Code}
	}
	return nil
}

func providerHeaderValidationErrorsForType(providerType string, headers map[string]string) []string {
	if err := validateProviderHeaderSupport(providerType, headers); err != nil {
		return []string{AsHTTPError(err).Code}
	}
	return providerHeaderValidationErrors(headers)
}

func usableProviderHeaders(providerType string, headers map[string]string) map[string]string {
	if err := validateProviderHeaderSupport(providerType, headers); err != nil {
		return nil
	}
	normalized, err := normalizeProviderHeaders(headers)
	if err != nil {
		return nil
	}
	return normalized
}

func validateProviderHeaderConfig(provider *Provider) error {
	if err := validateProviderHeaderSupport(provider.Type, provider.Headers); err != nil {
		return err
	}
	headers, err := normalizeProviderHeaders(provider.Headers)
	if err != nil {
		return err
	}
	sensitive, err := normalizedSensitiveProviderHeaders(provider.SensitiveHeaders, headers)
	if err != nil {
		return err
	}
	provider.Headers = headers
	provider.SensitiveHeaders = sensitive
	return nil
}

// ValidateProviderHeaderConfigForWrite exposes the same validation used by the
// Admin API to trusted internal writers such as the migration sink.
func ValidateProviderHeaderConfigForWrite(provider *Provider) error {
	return validateProviderHeaderConfig(provider)
}

func validateEffectiveProviderHeaders(providerType string, providerHeaders, resourceHeaders map[string]string) error {
	if err := validateProviderHeaderSupport(providerType, mergeProviderHeaders(providerHeaders, resourceHeaders)); err != nil {
		return err
	}
	return validateMergedProviderHeaderLimits(providerHeaders, resourceHeaders)
}

func validateMergedProviderHeaderLimits(providerHeaders, resourceHeaders map[string]string) error {
	providerNormalized, err := normalizeProviderHeaders(providerHeaders)
	if err != nil {
		return err
	}
	resourceNormalized, err := normalizeProviderHeaders(resourceHeaders)
	if err != nil {
		return err
	}
	merged := mergeProviderHeaders(providerNormalized, resourceNormalized)
	_, err = normalizeProviderHeaders(merged)
	return err
}

// ValidateProviderHeaderSupportForWrite lets trusted internal writers enforce
// adapter boundaries before calling the lower-level store bootstrap methods.
func ValidateProviderHeaderSupportForWrite(providerType string, headers map[string]string) error {
	return validateProviderHeaderSupport(providerType, headers)
}

func effectiveProviderResourceConfig(provider Provider, resource *ProviderResource) Provider {
	providerHeaders := usableProviderHeaders(provider.Type, provider.Headers)
	if resource == nil {
		provider.Headers = providerHeaders
		provider.SensitiveHeaders = mergeProviderSensitiveHeaders(providerHeaders, provider.SensitiveHeaders, nil, nil)
		return provider
	}
	if resource.BaseURL != "" {
		provider.BaseURL = resource.BaseURL
	}
	if resource.APIKey != "" {
		provider.APIKey = resource.APIKey
	}
	resourceHeaders := usableProviderHeaders(provider.Type, resource.Headers)
	mergedHeaders := mergeProviderHeaders(providerHeaders, resourceHeaders)
	provider.Headers = usableProviderHeaders(provider.Type, mergedHeaders)
	if len(provider.Headers) == 0 && len(mergedHeaders) > 0 {
		provider.SensitiveHeaders = nil
	} else {
		provider.SensitiveHeaders = mergeProviderSensitiveHeaders(providerHeaders, provider.SensitiveHeaders, resourceHeaders, resource.SensitiveHeaders)
	}
	if len(resource.Options) > 0 {
		options := make(map[string]string, len(provider.Options)+len(resource.Options))
		for key, value := range provider.Options {
			options[key] = value
		}
		for key, value := range resource.Options {
			options[key] = value
		}
		provider.Options = options
	}
	return provider
}

func validateProviderHeaderSupport(providerType string, headers map[string]string) error {
	if len(headers) == 0 {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case ProviderAzureOpenAI, ProviderOpenAICodex:
		return NewHTTPError(http.StatusBadRequest, "provider_headers_unsupported", "This Provider adapter manages its own client identity and does not support custom request headers")
	default:
		return nil
	}
}

func sensitiveProviderHeaderSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[http.CanonicalHeaderKey(name)] = true
	}
	return set
}

func retainStoredSensitiveProviderHeaders(
	headers map[string]string,
	sensitive []string,
	existingHeaders map[string]string,
	existingSensitive []string,
) map[string]string {
	retained := make(map[string]string, len(headers))
	requestedSet := sensitiveProviderHeaderSet(sensitive)
	existingSet := sensitiveProviderHeaderSet(existingSensitive)
	for name, value := range headers {
		canonicalName := http.CanonicalHeaderKey(name)
		if (value == "" || value == providerHeaderMask) && requestedSet[canonicalName] && existingSet[canonicalName] {
			if existingValue, ok := providerHeaderValue(existingHeaders, canonicalName); ok {
				value = existingValue
			}
		}
		retained[name] = value
	}
	return retained
}

func maskedProviderHeaders(headers map[string]string, sensitive []string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	masked := make(map[string]string, len(headers))
	sensitiveSet := sensitiveProviderHeaderSet(sensitive)
	for name, value := range headers {
		if sensitiveSet[http.CanonicalHeaderKey(name)] {
			value = providerHeaderMask
		}
		masked[name] = value
	}
	return masked
}

func auditProviderHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	masked := make(map[string]string, len(headers))
	for name := range headers {
		masked[name] = providerHeaderMask
	}
	return masked
}

func auditProvider(provider Provider) Provider {
	provider.Headers = auditProviderHeaders(provider.Headers)
	return provider
}

func auditProviderCreateResult(result ProviderCreateResult) ProviderCreateResult {
	result.Provider = auditProvider(result.Provider)
	return result
}

func auditProviderResource(resource ProviderResource) ProviderResource {
	resource.Headers = auditProviderHeaders(resource.Headers)
	return resource
}

func auditProviderResourceBulkResult(result ProviderResourceBulkResult) ProviderResourceBulkResult {
	for index := range result.Resources {
		result.Resources[index] = auditProviderResource(result.Resources[index])
	}
	return result
}

func auditProviderResourceImportResult(result ProviderResourceImportResult) ProviderResourceImportResult {
	for index := range result.Resources {
		result.Resources[index] = auditProviderResource(result.Resources[index])
	}
	return result
}

func (s *GormStore) protectProviderHeaders(
	headers map[string]string,
	sensitive []string,
	existingHeaders map[string]string,
	existingSensitive []string,
) (map[string]string, []string, error) {
	if headers == nil {
		return nil, nil, nil
	}
	canonical := make(map[string]string, len(headers))
	seenNames := make(map[string]bool, len(headers))
	for rawName, value := range headers {
		name := strings.TrimSpace(rawName)
		if name == "" || len(name) > providerHeaderNameMaxBytes || !validHTTPHeaderName(name) {
			return nil, nil, NewHTTPError(http.StatusBadRequest, "provider_header_name_invalid", "Custom request header name is invalid")
		}
		if reservedProviderHeaders[strings.ToLower(name)] {
			return nil, nil, NewHTTPError(http.StatusBadRequest, "provider_header_reserved", "Custom request header is managed by TokenHub and cannot be overridden")
		}
		canonicalName := http.CanonicalHeaderKey(name)
		if seenNames[canonicalName] {
			return nil, nil, NewHTTPError(http.StatusBadRequest, "provider_header_duplicate", "Custom request header names must be unique ignoring case")
		}
		seenNames[canonicalName] = true
		canonical[name] = value
	}

	if sensitive == nil && existingHeaders != nil {
		existingSet := sensitiveProviderHeaderSet(existingSensitive)
		for name := range canonical {
			if existingSet[http.CanonicalHeaderKey(name)] {
				sensitive = append(sensitive, name)
			}
		}
	}
	sensitiveNames, err := normalizedSensitiveProviderHeaders(sensitive, canonical)
	if err != nil {
		return nil, nil, err
	}
	existingSet := sensitiveProviderHeaderSet(existingSensitive)
	requestedSet := sensitiveProviderHeaderSet(sensitiveNames)
	for name, value := range canonical {
		canonicalName := http.CanonicalHeaderKey(name)
		if value != "" && value != providerHeaderMask {
			continue
		}
		if !requestedSet[canonicalName] || !existingSet[canonicalName] {
			return nil, nil, NewHTTPError(http.StatusBadRequest, "provider_header_value_required", "Custom request header value cannot be empty")
		}
		oldValue, exists := providerHeaderValue(existingHeaders, canonicalName)
		if !exists {
			return nil, nil, NewHTTPError(http.StatusBadRequest, "provider_header_value_required", "Custom request header value cannot be empty")
		}
		canonical[name] = s.decryptSecret(oldValue)
	}
	canonical, err = normalizeProviderHeaders(canonical)
	if err != nil {
		return nil, nil, err
	}
	for name := range canonical {
		if requestedSet[http.CanonicalHeaderKey(name)] {
			canonical[name] = s.encryptSecret(canonical[name])
		}
	}
	return canonical, sensitiveNames, nil
}

func (s *GormStore) revealProviderHeaders(headers map[string]string, sensitive []string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	revealed := make(map[string]string, len(headers))
	sensitiveSet := sensitiveProviderHeaderSet(sensitive)
	for name, value := range headers {
		if sensitiveSet[http.CanonicalHeaderKey(name)] {
			value = s.decryptSecret(value)
		}
		revealed[name] = value
	}
	return revealed
}

func (s *GormStore) revealProviderHeaderConfig(headers map[string]string, sensitive []string) (map[string]string, []string) {
	revealed := s.revealProviderHeaders(headers, sensitive)
	return revealed, providerHeaderValidationErrors(revealed)
}

func (s *GormStore) maskProviderHeaderConfig(provider *Provider) {
	provider.Headers, provider.HeaderValidationErrors = s.revealProviderHeaderConfig(provider.Headers, provider.SensitiveHeaders)
	provider.HeaderValidationErrors = providerHeaderValidationErrorsForType(provider.Type, provider.Headers)
	provider.Headers = maskedProviderHeaders(provider.Headers, provider.SensitiveHeaders)
}

func (s *GormStore) maskProviderResourceHeaderConfig(resource *ProviderResource) {
	resource.Headers, resource.HeaderValidationErrors = s.revealProviderHeaderConfig(resource.Headers, resource.SensitiveHeaders)
	var provider Provider
	if err := s.db.First(&provider, "id = ?", resource.ProviderID).Error; err == nil {
		if validationErr := validateEffectiveProviderHeaders(provider.Type, s.revealProviderHeaders(provider.Headers, provider.SensitiveHeaders), resource.Headers); validationErr != nil {
			resource.HeaderValidationErrors = []string{AsHTTPError(validationErr).Code}
		}
	}
	resource.Headers = maskedProviderHeaders(resource.Headers, resource.SensitiveHeaders)
}

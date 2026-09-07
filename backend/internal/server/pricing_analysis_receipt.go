package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
)

type pricingAnalysisDecision struct {
	ActorID              string              `json:"actor_id"`
	CurrentFingerprint   string              `json:"current_fingerprint"`
	CandidateFingerprint string              `json:"candidate_fingerprint"`
	Report               pricingImpactReport `json:"report"`
}

func (s *GormStore) signPricingAnalysis(decision pricingAnalysisDecision) (string, error) {
	if s.secretKey == "" {
		return "", NewHTTPError(503, "pricing_signing_unavailable", "Pricing analysis signing is not configured")
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		return "", err
	}
	if len(raw) > 1024*1024 {
		return "", NewHTTPError(400, "pricing_basis_too_large", "Narrow the analysis scope")
	}
	mac := hmac.New(sha256.New, []byte(s.secretKey))
	_, _ = mac.Write([]byte("tokenhub-pricing-analysis-v1\x00"))
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (s *GormStore) readPricingAnalysis(token string, actor AdminUser, current, candidate Model) (*pricingAnalysisDecision, error) {
	invalid := func() (*pricingAnalysisDecision, error) {
		return nil, NewHTTPError(409, "pricing_analysis_changed", "The pricing basis changed or the analysis receipt is invalid; analyze again")
	}
	if s.secretKey == "" || len(token) > 2*1024*1024 {
		return invalid()
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return invalid()
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return invalid()
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return invalid()
	}
	mac := hmac.New(sha256.New, []byte(s.secretKey))
	_, _ = mac.Write([]byte("tokenhub-pricing-analysis-v1\x00"))
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return invalid()
	}
	var result pricingAnalysisDecision
	if json.Unmarshal(raw, &result) != nil || result.ActorID != actor.ID || result.Report.Model != current.Name || result.CurrentFingerprint != pricingFingerprint(current) || result.CandidateFingerprint != pricingFingerprint(candidate) {
		return invalid()
	}
	result.Report.Receipt = ""
	return &result, nil
}

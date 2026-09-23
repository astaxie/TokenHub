package bundle

import (
	"encoding/json"
	"testing"
	"time"
)

func TestValidateBundle(t *testing.T) {
	bundle := &CanonicalMigrationBundle{
		SchemaVersion: SchemaVersion,
		Source: Source{
			Adapter:        "litellm",
			AdapterVersion: "1.60.0",
		},
		GeneratedAt: time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC),
	}

	if err := Validate(bundle); err != nil {
		t.Fatalf("validate bundle: %v", err)
	}
}

func TestValidateJSONRejectsMissingFields(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := ValidateJSON(payload); err == nil {
		t.Fatal("expected schema validation to fail")
	}
}

func TestValidateJSONRejectsUnknownFields(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
		"source": map[string]any{
			"adapter":         "litellm",
			"adapter_version": "1.60.0",
		},
		"generated_at": "2026-07-23T10:00:00Z",
		"unexpected":   true,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := ValidateJSON(payload); err == nil {
		t.Fatal("expected unknown field validation to fail")
	}
}

func TestValidateJSONRejectsInvalidSecretRef(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
		"source": map[string]any{
			"adapter":         "litellm",
			"adapter_version": "1.60.0",
		},
		"generated_at": "2026-07-23T10:00:00Z",
		"providers": []map[string]any{{
			"external_ref": map[string]any{"system": "litellm", "id": "provider/openai"},
			"spec":         map[string]any{"name": "OpenAI", "type": "openai_compatible"},
			"api_key_secret": map[string]any{
				"wrong": "OPENAI_API_KEY",
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := ValidateJSON(payload); err == nil {
		t.Fatal("expected invalid secret ref validation to fail")
	}
}

func TestValidateJSONAcceptsProviderHeaderSecretRefs(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
		"source": map[string]any{
			"adapter":         "tokenhub",
			"adapter_version": "1.2.0",
		},
		"generated_at": "2026-08-11T00:00:00Z",
		"providers": []map[string]any{{
			"external_ref": map[string]any{"system": "tokenhub", "id": "provider/openai"},
			"spec":         map[string]any{"name": "OpenAI", "type": "openai_compatible", "status": "active"},
			"header_secrets": map[string]any{
				"X-Tenant": map[string]any{"$secretRef": "PROVIDER_TENANT"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := ValidateJSON(payload); err != nil {
		t.Fatalf("validate header secret refs: %v", err)
	}
}

func TestValidateJSONRejectsInlineSensitiveProviderHeaders(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
		"source": map[string]any{
			"adapter":         "tokenhub",
			"adapter_version": "1.2.0",
		},
		"generated_at": "2026-08-11T00:00:00Z",
		"providers": []map[string]any{{
			"external_ref": map[string]any{"system": "tokenhub", "id": "provider/openai"},
			"spec": map[string]any{
				"name":              "OpenAI",
				"type":              "openai_compatible",
				"status":            "active",
				"headers":           map[string]string{"X-Tenant": "plaintext-secret"},
				"sensitive_headers": []string{"X-Tenant"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := ValidateJSON(payload); err == nil {
		t.Fatal("expected inline sensitive headers to be rejected")
	}
}

func TestValidateJSONRejectsProviderSpecUnknownFields(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
		"source": map[string]any{
			"adapter":         "litellm",
			"adapter_version": "1.60.0",
		},
		"generated_at": "2026-07-23T10:00:00Z",
		"providers": []map[string]any{{
			"external_ref": map[string]any{"system": "litellm", "id": "provider/openai"},
			"spec": map[string]any{
				"name":       "OpenAI",
				"type":       "openai_compatible",
				"status":     "active",
				"unexpected": true,
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := ValidateJSON(payload); err == nil {
		t.Fatal("expected provider spec unknown field validation to fail")
	}
}

func TestValidateJSONAcceptsMinuteRateLimits(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
		"source": map[string]any{
			"adapter":         "tokenhub",
			"adapter_version": "1.0.0",
		},
		"generated_at": "2026-08-01T10:00:00Z",
		"api_keys": []map[string]any{{
			"external_ref": map[string]any{"system": "tokenhub", "id": "key/limited"},
			"spec": map[string]any{
				"name":            "Limited Key",
				"rate_limit_rpm":  int64(60),
				"token_limit_tpm": int64(10_000),
			},
		}},
		"quota_policies": []map[string]any{{
			"external_ref": map[string]any{"system": "tokenhub", "id": "policy/project"},
			"name":         "Project limits",
			"limits": map[string]any{
				"rate_limit_rpm":  int64(600),
				"token_limit_tpm": int64(100_000),
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := ValidateJSON(payload); err != nil {
		t.Fatalf("minute rate limits should validate: %v", err)
	}
}

func TestValidateJSONAcceptsModelAccessModesAndRouteTags(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
		"source": map[string]any{
			"adapter": "tokenhub", "adapter_version": "1.1.0",
		},
		"generated_at": "2026-08-02T01:00:00Z",
		"projects": []map[string]any{{
			"external_ref": map[string]any{"system": "tokenhub", "id": "project/internal"},
			"spec": map[string]any{
				"name": "Internal", "status": "active", "model_access_mode": "restricted", "allowed_models": []string{"internal-model"},
			},
		}},
		"api_keys": []map[string]any{{
			"external_ref": map[string]any{"system": "tokenhub", "id": "key/internal"},
			"spec": map[string]any{
				"name": "Internal", "status": "active", "model_access_mode": "restricted", "allowed_models": []string{},
			},
		}},
		"routes": []map[string]any{{
			"external_ref": map[string]any{"system": "tokenhub", "id": "route/internal"},
			"spec": map[string]any{
				"provider_model": "internal-model", "status": "active", "tags": []string{"internal", "compliant"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := ValidateJSON(payload); err != nil {
		t.Fatalf("model access modes and route tags should validate: %v", err)
	}
}

func TestValidateJSONRejectsInvalidQuotaPolicyMinuteLimits(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "negative", value: int64(-1)},
		{name: "overflow", value: json.Number("18446744073709551616")},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{
				"schema_version": SchemaVersion,
				"source": map[string]any{
					"adapter":         "tokenhub",
					"adapter_version": "1.0.0",
				},
				"generated_at": "2026-08-01T10:00:00Z",
				"quota_policies": []map[string]any{{
					"external_ref": map[string]any{"system": "tokenhub", "id": "policy/project"},
					"name":         "Project limits",
					"limits":       map[string]any{"rate_limit_rpm": test.value},
				}},
			})
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}
			if err := ValidateJSON(payload); err == nil {
				t.Fatalf("expected %s quota policy minute limit validation to fail", test.name)
			}
		})
	}
}

func TestValidateJSONRejectsNestedAPIKeyMinuteRateLimits(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
		"source": map[string]any{
			"adapter":         "tokenhub",
			"adapter_version": "1.0.0",
		},
		"generated_at": "2026-08-01T10:00:00Z",
		"api_keys": []map[string]any{{
			"external_ref": map[string]any{"system": "tokenhub", "id": "key/limited"},
			"spec": map[string]any{
				"name": "Limited Key",
				"limits": map[string]any{
					"rate_limit_rpm":  int64(60),
					"token_limit_tpm": int64(10_000),
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := ValidateJSON(payload); err == nil {
		t.Fatal("expected nested API key minute rate limit validation to fail")
	}
}

func TestValidateJSONRejectsUserMissingRequiredFields(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
		"source": map[string]any{
			"adapter":         "litellm",
			"adapter_version": "1.60.0",
		},
		"generated_at": "2026-07-23T10:00:00Z",
		"users": []map[string]any{{
			"external_ref": map[string]any{"system": "litellm", "id": "user/admin"},
			"spec": map[string]any{
				"username": "admin",
				"status":   "active",
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := ValidateJSON(payload); err == nil {
		t.Fatal("expected user required field validation to fail")
	}
}

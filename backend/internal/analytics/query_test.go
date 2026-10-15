package analytics

import (
	"context"
	"errors"
	"testing"
	"time"
)

type credentialStoreStub struct {
	created Credential
}

func (s *credentialStoreStub) CreateCredential(_ context.Context, credential Credential, _ string) (Credential, string, error) {
	s.created = credential
	return credential, "secret", nil
}
func (s *credentialStoreStub) ListCredentials(context.Context) ([]Credential, error) { return nil, nil }
func (s *credentialStoreStub) RevokeCredential(context.Context, string) (Credential, error) {
	return Credential{}, nil
}
func (s *credentialStoreStub) ValidateCredential(context.Context, string) (Credential, error) {
	return Credential{}, nil
}

type tokenCostStoreStub struct{}

func (tokenCostStoreStub) QueryTokenCostPage(context.Context, Query) (Page, error) {
	return Page{Checkpoint: 4}, nil
}

func TestValidateCredentialScope(t *testing.T) {
	for _, test := range []struct {
		name      string
		scope     string
		projectID string
		wantErr   error
	}{
		{name: "organization", scope: ScopeOrganization},
		{name: "project", scope: ScopeProject, projectID: "project-1"},
		{name: "missing project", scope: ScopeProject, wantErr: ErrMissingProject},
		{name: "unknown", scope: "tenant", wantErr: ErrInvalidScope},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateCredentialScope(test.scope, test.projectID); err != test.wantErr {
				t.Fatalf("ValidateCredentialScope() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestServiceNormalizesAndValidatesInputs(t *testing.T) {
	credentials := &credentialStoreStub{}
	service := NewService(credentials, tokenCostStoreStub{})
	created, _, err := service.CreateCredential(context.Background(), Credential{
		Name: "  reporting  ", ScopeType: ScopeOrganization, ProjectID: "ignored",
	}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "reporting" || credentials.created.ProjectID != "" {
		t.Fatalf("credential normalization = %#v", created)
	}
	if _, _, err := service.CreateCredential(context.Background(), Credential{ScopeType: ScopeOrganization}, "secret"); !errors.Is(err, ErrMissingName) {
		t.Fatalf("missing name error = %v", err)
	}
}

func TestServiceValidatesQueryBeforePersistence(t *testing.T) {
	service := NewService(&credentialStoreStub{}, tokenCostStoreStub{})
	_, err := service.QueryTokenCostPage(context.Background(), Query{Granularity: "request", Limit: 1})
	if !errors.Is(err, ErrInvalidTimeRange) {
		t.Fatalf("invalid query error = %v", err)
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := service.QueryTokenCostPage(context.Background(), Query{From: from, To: from.Add(time.Hour), Granularity: "request", Limit: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestCursorValidation(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cursor := Cursor{
		Version: CursorVersion, From: now, Through: now.Add(time.Hour),
		Kind: "request", AfterAt: now, AfterID: "request-1",
		Query: CursorQuery{Granularity: "request"}, Checkpoint: 10,
	}
	if err := ValidateCursor(cursor); err != nil {
		t.Fatal(err)
	}
	cursor.Checkpoint = 1
	cursor.AfterCheckpoint = 2
	if err := ValidateCursor(cursor); err == nil {
		t.Fatal("ValidateCursor accepted an invalid checkpoint range")
	}
}

func TestDedupeKeySeparatesIncrementalWindows(t *testing.T) {
	query := Query{Granularity: "day", GroupBy: []string{"project"}, Incremental: true}
	row := Row{Bucket: "2026-01-02", ProjectID: "project-1", Status: StatusSuccess}
	first := DedupeKey(row, query)
	query.AfterSequence = 10
	second := DedupeKey(row, query)
	if first == second {
		t.Fatal("incremental windows share a dedupe key")
	}
}

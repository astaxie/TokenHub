package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/dbschema"
)

// Responses turn state lets Anthropic-backed routes honour
// previous_response_id. Anthropic has no server-side conversation store, so
// the gateway keeps the request and output items of every stored turn here and
// splices them into the next request before conversion.
const (
	// responsesTurnStateTTL matches the upstream Responses retention window.
	responsesTurnStateTTL = 30 * 24 * time.Hour
	// responsesTurnStateMaxBytes bounds one stored turn; larger conversations
	// must resend their own context.
	responsesTurnStateMaxBytes = 4 << 20
)

type responsesTurnState struct {
	ID           string `gorm:"primaryKey"`
	ProjectID    string `gorm:"index"`
	Model        string
	Instructions string
	InputJSON    string
	OutputJSON   string
	ExpiresAt    int64
}

func (responsesTurnState) TableName() string {
	return "responses_turn_states"
}

// responsesTurnStateMigration reserves version 12 for the payload-chunk ledger
// PR (#439); this table therefore claims the next free slot.
func responsesTurnStateMigration() dbschema.Migration {
	return dbschema.Migration{Version: 13, Name: "add-responses-turn-states", Statements: []string{
		`CREATE TABLE IF NOT EXISTS responses_turn_states (id text PRIMARY KEY, project_id text NOT NULL, model text NOT NULL, instructions text NOT NULL DEFAULT '', input_json text NOT NULL, output_json text NOT NULL, expires_at bigint NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_responses_turn_states_expiry ON responses_turn_states (expires_at)`,
	}, StatementBudget: 10}
}

type responsesTurnStore interface {
	LoadResponsesTurn(context.Context, string) (responsesTurnState, bool, error)
	SaveResponsesTurn(context.Context, responsesTurnState) error
}

func (s *GormStore) LoadResponsesTurn(ctx context.Context, id string) (responsesTurnState, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return responsesTurnState{}, false, nil
	}
	var state responsesTurnState
	err := s.db.WithContext(ctx).Where("id = ? AND expires_at > ?", id, time.Now().Unix()).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return responsesTurnState{}, false, nil
	}
	return state, err == nil, err
}

func (s *GormStore) SaveResponsesTurn(ctx context.Context, state responsesTurnState) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Bound each cleanup batch; expiration never requires an unbounded scan.
		if err := tx.Exec("DELETE FROM responses_turn_states WHERE id IN (SELECT id FROM responses_turn_states WHERE expires_at <= ? LIMIT 100)", time.Now().Unix()).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"project_id", "model", "instructions", "input_json", "output_json", "expires_at"}),
		}).Create(&state).Error
	})
}

// responsesTurnItems decodes one stored JSON array of Responses input items.
func responsesTurnItems(raw string) ([]map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, err
	}
	return items, nil
}

// expandResponsesPreviousTurn splices the stored turn referenced by
// previous_response_id in front of the current input, so an Anthropic-backed
// route can hold multi-turn Responses conversations even though the upstream
// keeps no state. Missing references fail loudly instead of silently losing
// the conversation prefix.
func (s *Server) expandResponsesPreviousTurn(ctx context.Context, req *ResponsesRequest) error {
	raw, ok := req.raw["previous_response_id"]
	if !ok || len(raw) == 0 {
		return nil
	}
	var referencedID string
	if err := json.Unmarshal(raw, &referencedID); err != nil {
		return NewHTTPError(http.StatusBadRequest, "invalid_previous_response_id",
			"previous_response_id must be a string")
	}
	referencedID = strings.TrimSpace(referencedID)
	if referencedID == "" {
		return nil
	}
	store, ok := s.store.(responsesTurnStore)
	if !ok {
		return NewHTTPError(http.StatusBadRequest, "previous_response_not_found",
			"The referenced response is not stored by this gateway")
	}
	state, found, err := store.LoadResponsesTurn(ctx, referencedID)
	if err != nil {
		return NewHTTPError(http.StatusInternalServerError, "previous_response_load_failed", err.Error())
	}
	if !found {
		return NewHTTPError(http.StatusBadRequest, "previous_response_not_found",
			"No stored response matches previous_response_id")
	}
	priorInput, err := responsesTurnItems(state.InputJSON)
	if err != nil {
		return NewHTTPError(http.StatusInternalServerError, "previous_response_load_failed", err.Error())
	}
	priorOutput, err := responsesTurnItems(state.OutputJSON)
	if err != nil {
		return NewHTTPError(http.StatusInternalServerError, "previous_response_load_failed", err.Error())
	}
	currentItems, err := responsesInputItems(*req)
	if err != nil {
		return err
	}
	if state.Instructions != "" && req.Instructions == "" {
		req.Instructions = state.Instructions
	}
	combined := make([]map[string]any, 0, len(priorInput)+len(priorOutput)+len(currentItems))
	combined = append(combined, priorInput...)
	combined = append(combined, priorOutput...)
	combined = append(combined, currentItems...)
	expanded := make([]any, 0, len(combined))
	for _, item := range combined {
		expanded = append(expanded, item)
	}
	req.Input = expanded
	delete(req.raw, "previous_response_id")
	return nil
}

// recordResponsesTurn persists the finished exchange so a later request can
// reference it through previous_response_id. Requests that set store=false opt
// out, matching the Responses semantics.
func (s *Server) recordResponsesTurn(ctx context.Context, call CallContext, req *ResponsesRequest, resp map[string]any) {
	if req.Store != nil && !*req.Store {
		return
	}
	store, ok := s.store.(responsesTurnStore)
	if !ok {
		return
	}
	id, _ := resp["id"].(string)
	if strings.TrimSpace(id) == "" {
		return
	}
	inputItems, err := responsesInputItems(*req)
	if err != nil {
		return
	}
	inputJSON, err := json.Marshal(inputItems)
	if err != nil {
		return
	}
	outputJSON, err := json.Marshal(resp["output"])
	if err != nil {
		return
	}
	state := responsesTurnState{
		ID:           id,
		ProjectID:    call.Project.ID,
		Model:        call.Model.Name,
		Instructions: req.Instructions,
		InputJSON:    string(inputJSON),
		OutputJSON:   string(outputJSON),
		ExpiresAt:    time.Now().Add(responsesTurnStateTTL).Unix(),
	}
	_ = store.SaveResponsesTurn(ctx, state)
}

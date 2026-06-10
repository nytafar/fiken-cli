// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL). The append-only audit log (build-spec §6.2/§7).
// Events are written INSIDE the write call, never as a separate step. Enough
// is recorded to reconstruct AND reverse any action. inputs_json/result_json
// each carry a top-level "v" schema-version tag (§10) so old rows stay
// interpretable forever; LogEvent injects v:1 when a caller omits it.
package fikencore

import (
	"context"
	"encoding/json"
	"fmt"
)

// Event is an agent_events row to append (id + ts are assigned by LogEvent).
type Event struct {
	CorrelationID    string
	Surface          string // "api" | "browser"
	Operation        string // noun.verb (§7)
	CompanySlug      string
	Actor            string
	TargetEntityType string
	TargetEntityID   string
	SourceRef        string
	XRequestID       string
	Confidence       string // high | medium | low
	Rationale        string
	InputsJSON       string // object; a top-level "v" is ensured
	Result           string // "success" | "failure"
	ResultJSON       string // object; a top-level "v" is ensured
	ReversesEventID  string
}

// LoggedEvent is a read-back agent_events row.
type LoggedEvent struct {
	ID        string `json:"id"`
	TS        string `json:"ts"`
	Operation string `json:"operation"`
	Surface   string `json:"surface"`
	Company   string `json:"company_slug,omitempty"`
	Target    string `json:"target_entity_id,omitempty"`
	Result    string `json:"result"`
	Rationale string `json:"rationale,omitempty"`
}

// LogEvent appends one event and returns its generated ULID id. surface,
// operation, and result are required (NOT NULL columns). The operation must be
// grammar-valid; callers that want the warn-never-reject behavior should check
// OperationGrammarValid / OperationKnown before calling.
func (c *Core) LogEvent(ctx context.Context, e Event) (string, error) {
	if e.Surface == "" {
		return "", fmt.Errorf("audit event requires surface")
	}
	if e.Operation == "" {
		return "", fmt.Errorf("audit event requires operation")
	}
	if !OperationGrammarValid(e.Operation) {
		return "", fmt.Errorf("operation %q violates the noun.verb grammar", e.Operation)
	}
	if e.Result == "" {
		e.Result = "success"
	}
	if e.Result != "success" && e.Result != "failure" {
		return "", fmt.Errorf("audit result must be success|failure, got %q", e.Result)
	}

	id := newULID()
	inputs := ensureVersioned(e.InputsJSON)
	result := ensureVersioned(e.ResultJSON)

	_, err := c.db.ExecContext(ctx, `
		INSERT INTO agent_events
			(id, ts, correlation_id, surface, operation, company_slug, actor,
			 target_entity_type, target_entity_id, source_ref, x_request_id,
			 confidence, rationale, inputs_json, result, result_json, reverses_event_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, nowRFC3339(), nullify(e.CorrelationID), e.Surface, e.Operation,
		nullify(e.CompanySlug), nullify(e.Actor), nullify(e.TargetEntityType),
		nullify(e.TargetEntityID), nullify(e.SourceRef), nullify(e.XRequestID),
		nullify(e.Confidence), nullify(e.Rationale), nullify(inputs), e.Result,
		nullify(result), nullify(e.ReversesEventID))
	if err != nil {
		return "", fmt.Errorf("append agent_event: %w", err)
	}
	return id, nil
}

// RecentEvents returns up to limit most-recent events, optionally scoped to a
// company. Read-only; the append-only triggers do not block SELECT.
func (c *Core) RecentEvents(ctx context.Context, companySlug string, limit int) ([]LoggedEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, ts, operation, surface, COALESCE(company_slug,''),
	             COALESCE(target_entity_id,''), result, COALESCE(rationale,'')
	      FROM agent_events`
	args := []any{}
	if companySlug != "" {
		q += ` WHERE company_slug = ?`
		args = append(args, companySlug)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := c.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query agent_events: %w", err)
	}
	defer rows.Close()
	var out []LoggedEvent
	for rows.Next() {
		var e LoggedEvent
		if err := rows.Scan(&e.ID, &e.TS, &e.Operation, &e.Surface, &e.Company, &e.Target, &e.Result, &e.Rationale); err != nil {
			return nil, fmt.Errorf("scan agent_event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ensureVersioned guarantees the JSON blob is an object carrying a top-level
// "v". Empty input stays empty. A non-object or unparseable input is wrapped as
// {"v":1,"value":<original>} so nothing is lost and the contract still holds.
func ensureVersioned(s string) string {
	if s == "" {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err == nil {
		if _, ok := m["v"]; !ok {
			m["v"] = json.RawMessage("1")
		}
		if b, err := json.Marshal(m); err == nil {
			return string(b)
		}
		return s
	}
	// Not an object: wrap, preserving the original as a string value.
	wrapped := map[string]any{"v": 1, "value": s}
	if b, err := json.Marshal(wrapped); err == nil {
		return string(b)
	}
	return s
}

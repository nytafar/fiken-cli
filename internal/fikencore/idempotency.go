// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL). The idempotency guard (build-spec §6.1). Every write
// checks/reserves here FIRST; a second attempt for the same
// (company_slug, source_system, source_id) no-ops instead of double-posting
// real books. The UNIQUE key is LOCKED (§10). For bank-line vouchers,
// source_id = linje.id and source_hash = (utskriftId,nr) — see §9.
package fikencore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Idempotency statuses (the lifecycle a source moves through).
const (
	StatusProposed  = "proposed"
	StatusDrafted   = "drafted"
	StatusCommitted = "committed"
	StatusMatched   = "matched"
	StatusReversed  = "reversed"
	StatusFailed    = "failed"
)

// IdempotencyRecord mirrors a row of idempotency_map.
type IdempotencyRecord struct {
	ID              int64
	CompanySlug     string
	SourceSystem    string
	SourceID        string
	SourceHash      string
	FikenEntityType string
	FikenEntityID   string
	DraftID         string
	Status          string
	XRequestID      string
	CreatedAt       string
	UpdatedAt       string
}

// Lookup returns the existing record for the idempotency key, if any.
func (c *Core) Lookup(ctx context.Context, companySlug, sourceSystem, sourceID string) (IdempotencyRecord, bool, error) {
	var r IdempotencyRecord
	var srcHash, fet, fei, draft, xrid sql.NullString
	err := c.db.QueryRowContext(ctx, `
		SELECT id, company_slug, source_system, source_id, source_hash,
		       fiken_entity_type, fiken_entity_id, draft_id, status, x_request_id,
		       created_at, updated_at
		FROM idempotency_map
		WHERE company_slug = ? AND source_system = ? AND source_id = ?`,
		companySlug, sourceSystem, sourceID).Scan(
		&r.ID, &r.CompanySlug, &r.SourceSystem, &r.SourceID, &srcHash,
		&fet, &fei, &draft, &r.Status, &xrid, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return IdempotencyRecord{}, false, fmt.Errorf("idempotency lookup: %w", err)
	}
	r.SourceHash, r.FikenEntityType, r.FikenEntityID, r.DraftID, r.XRequestID =
		srcHash.String, fet.String, fei.String, draft.String, xrid.String
	return r, true, nil
}

// Reserve inserts a new idempotency row. If the key already exists it does NOT
// overwrite — it returns created=false and the existing record, which is the
// double-post guard: the caller must treat created=false as "already handled"
// and no-op the write. status defaults to StatusProposed when empty.
func (c *Core) Reserve(ctx context.Context, rec IdempotencyRecord) (created bool, existing IdempotencyRecord, err error) {
	if rec.CompanySlug == "" || rec.SourceSystem == "" || rec.SourceID == "" {
		return false, IdempotencyRecord{}, fmt.Errorf("idempotency key requires company_slug, source_system, source_id")
	}
	if rec.Status == "" {
		rec.Status = StatusProposed
	}
	now := nowRFC3339()
	res, err := c.db.ExecContext(ctx, `
		INSERT INTO idempotency_map
			(company_slug, source_system, source_id, source_hash,
			 fiken_entity_type, fiken_entity_id, draft_id, status, x_request_id,
			 created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(company_slug, source_system, source_id) DO NOTHING`,
		rec.CompanySlug, rec.SourceSystem, rec.SourceID, nullify(rec.SourceHash),
		nullify(rec.FikenEntityType), nullify(rec.FikenEntityID), nullify(rec.DraftID),
		rec.Status, nullify(rec.XRequestID), now, now)
	if err != nil {
		return false, IdempotencyRecord{}, fmt.Errorf("idempotency reserve: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, IdempotencyRecord{}, nil
	}
	// Conflict: the key already exists. Return the existing record.
	ex, _, lerr := c.Lookup(ctx, rec.CompanySlug, rec.SourceSystem, rec.SourceID)
	if lerr != nil {
		return false, IdempotencyRecord{}, lerr
	}
	return false, ex, nil
}

// Advance updates the status (and optionally the Fiken entity / draft /
// x-request-id) of an existing idempotency row. Empty optional fields are left
// unchanged so a status-only transition does not clobber prior IDs.
func (c *Core) Advance(ctx context.Context, companySlug, sourceSystem, sourceID, status string, opts AdvanceOpts) error {
	now := nowRFC3339()
	_, err := c.db.ExecContext(ctx, `
		UPDATE idempotency_map SET
			status = ?,
			fiken_entity_type = COALESCE(NULLIF(?, ''), fiken_entity_type),
			fiken_entity_id   = COALESCE(NULLIF(?, ''), fiken_entity_id),
			draft_id          = COALESCE(NULLIF(?, ''), draft_id),
			x_request_id      = COALESCE(NULLIF(?, ''), x_request_id),
			updated_at = ?
		WHERE company_slug = ? AND source_system = ? AND source_id = ?`,
		status, opts.FikenEntityType, opts.FikenEntityID, opts.DraftID, opts.XRequestID,
		now, companySlug, sourceSystem, sourceID)
	if err != nil {
		return fmt.Errorf("idempotency advance: %w", err)
	}
	return nil
}

// AdvanceOpts carries the optional fields Advance may set.
type AdvanceOpts struct {
	FikenEntityType string
	FikenEntityID   string
	DraftID         string
	XRequestID      string
}

func nullify(s string) any {
	if s == "" {
		return nil
	}
	return s
}

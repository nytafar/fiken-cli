// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The per-(company, resource) sync watermark (issue #22). sync_state could not
// carry it: it is keyed by resource_type alone, so a `sync --company A` run
// would move the watermark past changes company B has not pulled yet, and the
// next unscoped run would skip them for good. A watermark that can be wrong in
// that direction is worse than none at all, so this table keys on both, and
// sync_state is left exactly as it was — `doctor`, the provenance envelope and
// the resume cursor all still read it.
//
// last_synced_at is TEXT holding RFC3339 in UTC, written by the CLI rather than
// by CURRENT_TIMESTAMP: the value is the START of the run that filled the
// window, not the moment the row was written, because rows keep changing while
// a walk runs. The reader (internal/cli/sync_watermark.go) widens it by a day
// before it reaches the wire.
package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// syncWatermarkCreateSQL is the table, run from migrateExtras on every open.
const syncWatermarkCreateSQL = `CREATE TABLE IF NOT EXISTS sync_watermark (
	company_slug TEXT NOT NULL,
	resource_type TEXT NOT NULL,
	last_synced_at TEXT NOT NULL,
	PRIMARY KEY (company_slug, resource_type)
)`

// SyncWatermark returns the stored watermark for one (company, resource) as
// the RFC3339 string it was written as, or "" when no complete pull has ever
// finished for that pair — which the caller must read as "pull it in full".
//
// A store error answers "" too, deliberately: the conservative direction for a
// missing watermark is a full pull, and a sync must not fail over a
// bookkeeping row.
func (s *Store) SyncWatermark(companySlug, resourceType string) string {
	if companySlug == "" || resourceType == "" {
		return ""
	}
	var ts sql.NullString
	err := s.db.QueryRow(
		`SELECT last_synced_at FROM sync_watermark WHERE company_slug = ? AND resource_type = ?`,
		companySlug, resourceType,
	).Scan(&ts)
	if err != nil || !ts.Valid {
		return ""
	}
	return ts.String
}

// SetSyncWatermark records that everything changed up to lastSyncedAt has been
// pulled for one (company, resource). Callers must only call it after a pull
// they can vouch for as complete; the store cannot tell.
func (s *Store) SetSyncWatermark(companySlug, resourceType, lastSyncedAt string) error {
	if companySlug == "" || resourceType == "" {
		return fmt.Errorf("sync watermark needs both a company and a resource (got %q, %q)", companySlug, resourceType)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO sync_watermark (company_slug, resource_type, last_synced_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(company_slug, resource_type) DO UPDATE SET last_synced_at = excluded.last_synced_at`,
		companySlug, resourceType, lastSyncedAt,
	)
	return err
}

// ClearSyncWatermark drops the watermark for one (company, resource), so the
// next default run pulls that pair in full. This is what `sync --full` does
// before it refetches: the run is only allowed to write a new watermark from
// the pull it is about to make, never to leave an old one standing.
func (s *Store) ClearSyncWatermark(companySlug, resourceType string) error {
	if companySlug == "" || resourceType == "" {
		return fmt.Errorf("sync watermark needs both a company and a resource (got %q, %q)", companySlug, resourceType)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(
		`DELETE FROM sync_watermark WHERE company_slug = ? AND resource_type = ?`,
		companySlug, resourceType,
	)
	return err
}

// ClearSyncWatermarks drops the watermarks for a whole `sync --full` scope in
// one statement: the named resources for companySlug, or for EVERY company
// when companySlug is "". It returns the number of rows dropped.
//
// The per-pair ClearSyncWatermark above is the lazy form, run as each parent's
// refetch starts. That is one clear too late for the pairs a run never reaches:
// `--full --company X` deletes the scoped company's rows before the first
// request, so a run interrupted after the delete would leave a mark standing
// over rows it had already removed, and the next default run would window
// straight over the hole. This clears the whole scope at the same instant as
// the delete, so the worst an interruption can leave behind is "pull me in
// full".
//
// An empty resourceTypes list is a no-op rather than "every resource": the
// scope of a --full run is the resources it names, and widening that here would
// reset pairs the run never intended to refetch.
func (s *Store) ClearSyncWatermarks(companySlug string, resourceTypes []string) (int64, error) {
	if len(resourceTypes) == 0 {
		return 0, nil
	}
	query := `DELETE FROM sync_watermark WHERE resource_type IN (?` + strings.Repeat(", ?", len(resourceTypes)-1) + `)`
	args := make([]any, 0, len(resourceTypes)+1)
	for _, resource := range resourceTypes {
		args = append(args, resource)
	}
	if companySlug != "" {
		query += ` AND company_slug = ?`
		args = append(args, companySlug)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return removed, nil
}

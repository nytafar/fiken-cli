// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
// HAND-AUTHORED (NOVEL). Not generated, not overwritten by regen-merge: this is the
// sanctioned migration extension point for novel-feature tables, deliberately kept out
// of the generated store so a reprint cannot drop it.

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrateExtras runs after the generated store migrations and before the
// schema-version stamp. It is the canonical place for novel-feature auxiliary
// tables that need to live in the local store.
//
// Edit this file when adding tables for novel commands. Keep migrations
// idempotent with CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS so
// every store open can safely re-run them.
func (s *Store) migrateExtras(ctx context.Context, conn *sql.Conn) error {
	migrations := []string{
		// Add CREATE TABLE IF NOT EXISTS statements here.
		//
		// The per-(company, resource) sync watermark (issue #22). Declared in
		// sync_watermark.go beside the accessors that read and write it.
		syncWatermarkCreateSQL,
	}
	for _, m := range migrations {
		if _, err := conn.ExecContext(ctx, m); err != nil {
			return fmt.Errorf("extra migration failed: %w", err)
		}
	}
	return nil
}

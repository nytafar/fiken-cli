// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated. The irreplaceable core store
// (build-spec §5): a SEPARATE SQLite file (fiken-core.db) from the disposable
// mirror (data.db). It holds exactly two tables, both with LOCKED schemas
// (§10): idempotency_map (guard against double-posting) and agent_events
// (append-only audit). Keeping it in its own file means "delete the mirror to
// force a clean re-sync" never touches the one dataset that cannot be
// reconstructed.
package fikencore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, same as internal/store
)

// Core wraps the fiken-core.db connection.
type Core struct {
	db   *sql.DB
	path string
}

// DefaultPath returns the core DB path alongside the mirror DB
// (~/.local/share/fiken-cli/fiken-core.db).
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "fiken-cli", "fiken-core.db")
}

// Open opens or creates the core DB and applies the locked migrations.
func Open(ctx context.Context, path string) (*Core, error) {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating core db directory: %w", err)
	}
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening core db: %w", err)
	}
	db.SetMaxOpenConns(2)
	c := &Core{db: db, path: path}
	if err := c.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("core db migrations: %w", err)
	}
	return c, nil
}

// DB exposes the underlying handle for read-only audit queries.
func (c *Core) DB() *sql.DB { return c.db }

// Path returns the core DB file path.
func (c *Core) Path() string { return c.path }

// Close closes the core DB.
func (c *Core) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

// coreMigrations is the LOCKED schema (build-spec §6/§10). The column lists,
// the idempotency UNIQUE key, the top-level versioned JSON blobs, and the
// append-only triggers are not to be changed without an explicit migration.
var coreMigrations = []string{
	`CREATE TABLE IF NOT EXISTS idempotency_map (
		id                INTEGER PRIMARY KEY,
		company_slug      TEXT NOT NULL,
		source_system     TEXT NOT NULL,
		source_id         TEXT NOT NULL,
		source_hash       TEXT,
		fiken_entity_type TEXT,
		fiken_entity_id   TEXT,
		draft_id          TEXT,
		status            TEXT NOT NULL,
		x_request_id      TEXT,
		created_at        TEXT NOT NULL,
		updated_at        TEXT NOT NULL,
		UNIQUE (company_slug, source_system, source_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_idempotency_status ON idempotency_map(company_slug, status)`,
	`CREATE TABLE IF NOT EXISTS agent_events (
		id                 TEXT PRIMARY KEY,
		ts                 TEXT NOT NULL,
		correlation_id     TEXT,
		surface            TEXT NOT NULL,
		operation          TEXT NOT NULL,
		company_slug       TEXT,
		actor              TEXT,
		target_entity_type TEXT,
		target_entity_id   TEXT,
		source_ref         TEXT,
		x_request_id       TEXT,
		confidence         TEXT,
		rationale          TEXT,
		inputs_json        TEXT,
		result             TEXT NOT NULL,
		result_json        TEXT,
		reverses_event_id  TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_agent_events_corr ON agent_events(correlation_id)`,
	`CREATE INDEX IF NOT EXISTS idx_agent_events_op ON agent_events(operation)`,
	`CREATE INDEX IF NOT EXISTS idx_agent_events_company ON agent_events(company_slug, ts)`,
	// Append-only enforced structurally, not by convention.
	`CREATE TRIGGER IF NOT EXISTS agent_events_no_update BEFORE UPDATE ON agent_events
		BEGIN SELECT RAISE(ABORT, 'agent_events is append-only'); END`,
	`CREATE TRIGGER IF NOT EXISTS agent_events_no_delete BEFORE DELETE ON agent_events
		BEGIN SELECT RAISE(ABORT, 'agent_events is append-only'); END`,
}

func (c *Core) migrate(ctx context.Context) error {
	for _, stmt := range coreMigrations {
		if _, err := c.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration failed: %w\nstatement: %s", err, stmt)
		}
	}
	return nil
}

// nowRFC3339 returns the current UTC time as an ISO8601 string.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// crockfordEncoding is Crockford base32 (no I, L, O, U), no padding.
var crockfordEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newULID returns a 26-char time-sortable ID: 48-bit big-endian millisecond
// timestamp + 80 bits of randomness, Crockford-base32 encoded. The leading
// timestamp bytes keep agent_events rows ordered chronologically by primary
// key. Not bit-canonical ULID, but the sortability + uniqueness contract holds.
func newULID() string {
	ms := uint64(time.Now().UTC().UnixMilli())
	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	_, _ = rand.Read(b[6:])
	return crockfordEncoding.EncodeToString(b[:])
}

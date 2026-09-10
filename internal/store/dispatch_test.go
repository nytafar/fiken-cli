// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The regression test for issue #6 §1: on a fully synced file the journal_entries
// typed table held 0 rows while `resources` held the complete set, because
// UpsertBatch's dispatch arm was spelled "journal-entries" and sync passes
// "journal_entries". Before the canonicalisation fix, the first assertion below
// fails (typed count 0) and the second passes for the wrong reason.
package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func journalEntryItems() []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"journalEntryId": 9001, "parent_id": "agensia", "date": "2026-01-02"}`),
		json.RawMessage(`{"journalEntryId": 9002, "parent_id": "agensia", "date": "2026-01-03"}`),
		json.RawMessage(`{"journalEntryId": 9003, "parent_id": "agensia", "date": "2026-01-04"}`),
	}
}

func openTempStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertBatch_SnakeJournalEntriesReachesTypedTable(t *testing.T) {
	s := openTempStore(t)
	items := journalEntryItems()

	stored, extractFailures, err := s.UpsertBatch("journal_entries", items)
	if err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
	if stored != len(items) || extractFailures != 0 {
		t.Fatalf("UpsertBatch stored=%d extractFailures=%d, want %d/0", stored, extractFailures, len(items))
	}

	var typed int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM "journal_entries"`).Scan(&typed); err != nil {
		t.Fatalf("count journal_entries: %v", err)
	}
	if typed == 0 {
		t.Fatalf(`journal_entries typed table is empty after UpsertBatch("journal_entries", …) (issue #6 §1)`)
	}
	if typed != len(items) {
		t.Fatalf("journal_entries count = %d, want %d", typed, len(items))
	}
}

// The kebab spelling must behave identically — same resource_type in `resources`,
// same typed rows — rather than opening a second, disjoint population.
func TestUpsertBatch_KebabAndSnakeAreTheSameResource(t *testing.T) {
	for _, name := range []string{"journal_entries", "journal-entries"} {
		t.Run(name, func(t *testing.T) {
			s := openTempStore(t)
			if _, _, err := s.UpsertBatch(name, journalEntryItems()); err != nil {
				t.Fatalf("UpsertBatch(%q): %v", name, err)
			}

			var typed int
			if err := s.DB().QueryRow(`SELECT COUNT(*) FROM "journal_entries"`).Scan(&typed); err != nil {
				t.Fatalf("count journal_entries: %v", err)
			}
			if typed != 3 {
				t.Fatalf("journal_entries count = %d, want 3", typed)
			}

			var generic int
			if err := s.DB().QueryRow(
				`SELECT COUNT(*) FROM resources WHERE resource_type = ?`, "journal_entries",
			).Scan(&generic); err != nil {
				t.Fatalf("count resources: %v", err)
			}
			if generic != 3 {
				t.Fatalf("resources[journal_entries] count = %d, want 3", generic)
			}

			var kebab int
			if err := s.DB().QueryRow(
				`SELECT COUNT(*) FROM resources WHERE resource_type = ?`, "journal-entries",
			).Scan(&kebab); err != nil {
				t.Fatalf("count resources: %v", err)
			}
			if kebab != 0 {
				t.Fatalf("resources[journal-entries] count = %d, want 0 (a second population)", kebab)
			}
		})
	}
}

// The parent-key map was hyphen-spelled for journal_entries and bank_accounts,
// so resourceStorageID returned a BARE id and two companies' entries with the
// same API id collided on the resources primary key.
func TestUpsertBatch_ParentKeyedStorageIDKeepsCompaniesApart(t *testing.T) {
	s := openTempStore(t)

	for _, slug := range []string{"agensia", "other-co"} {
		items := []json.RawMessage{
			json.RawMessage(fmt.Sprintf(`{"journalEntryId": 4242, "parent_id": %q}`, slug)),
		}
		if _, _, err := s.UpsertBatch("journal_entries", items); err != nil {
			t.Fatalf("UpsertBatch(%s): %v", slug, err)
		}
	}

	var rows int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM resources WHERE resource_type = ?`, "journal_entries",
	).Scan(&rows); err != nil {
		t.Fatalf("count resources: %v", err)
	}
	if rows != 2 {
		t.Fatalf("resources[journal_entries] count = %d, want 2 (one company overwrote the other)", rows)
	}

	var bare int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM resources WHERE resource_type = ? AND instr(id, char(0)) = 0`, "journal_entries",
	).Scan(&bare); err != nil {
		t.Fatalf("count bare keys: %v", err)
	}
	if bare != 0 {
		t.Fatalf("%d journal_entries rows carry a bare storage key, want 0", bare)
	}
}

func TestUpsertBatch_UnknownResourceIsAnError(t *testing.T) {
	s := openTempStore(t)
	items := []json.RawMessage{json.RawMessage(`{"id": "x"}`)}

	if _, _, err := s.UpsertBatch("no-such-thing", items); err == nil {
		t.Fatal(`UpsertBatch("no-such-thing", …) returned nil, want an error`)
	}
	if err := s.Upsert("no-such-thing", "x", items[0]); err == nil {
		t.Fatal(`Upsert("no-such-thing", …) returned nil, want an error`)
	}

	var rows int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM resources`).Scan(&rows); err != nil {
		t.Fatalf("count resources: %v", err)
	}
	if rows != 0 {
		t.Fatalf("an unknown resource wrote %d rows, want 0", rows)
	}
}

// Every canonical name with a typed table must have a dispatch arm. The loud
// default arm turns a missing arm into an error; this walks the whole closed set
// so a future generator run that drops an arm fails here rather than silently
// leaving a table empty.
func TestUpsertBatch_EveryTypedResourceHasADispatchArm(t *testing.T) {
	for _, name := range TypedTableResources() {
		t.Run(name, func(t *testing.T) {
			s := openTempStore(t)
			// A minimal object: the generic fallback list resolves "id", and every
			// typed table's insert binds missing columns as NULL.
			items := []json.RawMessage{json.RawMessage(`{"id": "probe-1", "parent_id": "agensia", "contacts_id": "agensia", "invoices_id": "agensia", "journal_entries_id": "agensia", "order_confirmations_id": "agensia", "purchases_id": "agensia", "sales_id": "agensia", "transactions_id": "agensia"}`)}
			if _, _, err := s.UpsertBatch(name, items); err != nil {
				t.Fatalf("UpsertBatch(%q): %v", name, err)
			}
			var typed int
			if err := s.DB().QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, name)).Scan(&typed); err != nil {
				t.Fatalf("count %s: %v", name, err)
			}
			if typed != 1 {
				t.Fatalf("%s typed table count = %d, want 1 (dispatch arm missing?)", name, typed)
			}
		})
	}
}

// ClearCompanyResources is what makes `sync --full --company S --resources R`
// actually full. It must be exactly that narrow: one company, one resource.
func TestClearCompanyResources_OnlyTheNamedCompanyAndResource(t *testing.T) {
	s := openTempStore(t)

	seed := func(resource, slug string, ids ...int) {
		items := make([]json.RawMessage, 0, len(ids))
		for _, id := range ids {
			items = append(items, json.RawMessage(fmt.Sprintf(
				`{"journalEntryId": %d, "bankAccountId": %d, "parent_id": %q}`, id, id, slug)))
		}
		if _, _, err := s.UpsertBatch(resource, items); err != nil {
			t.Fatalf("seed %s/%s: %v", resource, slug, err)
		}
	}
	seed("journal_entries", "agensia", 1, 2, 3)
	seed("journal_entries", "other-co", 1, 2)
	seed("bank_accounts", "agensia", 7, 8)

	count := func(resource, slug string) int {
		t.Helper()
		n, err := s.CountCompanyResources(resource, slug)
		if err != nil {
			t.Fatalf("count %s/%s: %v", resource, slug, err)
		}
		return n
	}

	removed, err := s.ClearCompanyResources("journal_entries", "agensia")
	if err != nil {
		t.Fatalf("ClearCompanyResources: %v", err)
	}
	if removed != 3 {
		t.Fatalf("removed = %d, want 3", removed)
	}
	if got := count("journal_entries", "agensia"); got != 0 {
		t.Errorf("agensia journal_entries = %d, want 0", got)
	}
	if got := count("journal_entries", "other-co"); got != 2 {
		t.Errorf("other-co journal_entries = %d, want 2 (another company's rows were deleted)", got)
	}
	if got := count("bank_accounts", "agensia"); got != 2 {
		t.Errorf("agensia bank_accounts = %d, want 2 (another resource was deleted)", got)
	}

	// The FTS index must go with the rows, not outlive them.
	var fts int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM resources_fts`).Scan(&fts); err != nil {
		t.Fatalf("count resources_fts: %v", err)
	}
	if fts != 4 {
		t.Errorf("resources_fts rows = %d, want 4 (index left out of step with resources)", fts)
	}

	// An empty slug must never become an unscoped wipe.
	if _, err := s.ClearCompanyResources("journal_entries", ""); err == nil {
		t.Error("ClearCompanyResources with an empty slug returned nil, want an error")
	}
	if _, err := s.ClearCompanyResources("no-such-thing", "agensia"); err == nil {
		t.Error("ClearCompanyResources with an unknown resource returned nil, want an error")
	}
}

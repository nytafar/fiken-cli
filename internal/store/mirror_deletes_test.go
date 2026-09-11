// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Pins the store half of deletion detection (issue #17): the set difference is
// scoped to one company, spares pre-canonicalisation bare-key rows, keeps the
// FTS index in step, and does not degrade when the keep set is larger than
// SQLite can bind in one statement.

package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func openDeleteTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedContacts upserts contacts for one company the way the dependent sync
// walker does (parent_id injected before the upsert) and returns their storage
// keys in the same order.
func seedContacts(t *testing.T, db *Store, slug string, ids ...int) []string {
	t.Helper()
	items := make([]json.RawMessage, 0, len(ids))
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		item := json.RawMessage(fmt.Sprintf(`{"contactId":%d,"name":"Contact %d","parent_id":%q}`, id, id, slug))
		items = append(items, item)
		key, ok := StorageKeyOf("contacts", item)
		if !ok {
			t.Fatalf("StorageKeyOf(contacts, %s) refused the item", item)
		}
		keys = append(keys, key)
	}
	if _, _, err := db.UpsertBatch("contacts", items); err != nil {
		t.Fatalf("seed contacts for %s: %v", slug, err)
	}
	return keys
}

// TestDeleteCompanyResourcesNotIn_DeletesOnlyTheAbsentRowsOfThatCompany is the
// rule: what the pull served stays, what it did not serve goes, and the other
// company is not part of the difference at all.
func TestDeleteCompanyResourcesNotIn_DeletesOnlyTheAbsentRowsOfThatCompany(t *testing.T) {
	db := openDeleteTestStore(t)
	keys := seedContacts(t, db, "alpha", 1, 2, 3)
	seedContacts(t, db, "beta", 4, 5)

	// The pull served contacts 1 and 2; contact 3 is gone from Fiken.
	removed, err := db.DeleteCompanyResourcesNotIn("contacts", "alpha", keys[:2])
	if err != nil {
		t.Fatalf("DeleteCompanyResourcesNotIn: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if got, err := db.CountCompanyResources("contacts", "alpha"); err != nil || got != 2 {
		t.Fatalf("alpha rows = (%d, %v), want (2, nil)", got, err)
	}
	if got, err := db.CountCompanyResources("contacts", "beta"); err != nil || got != 2 {
		t.Fatalf("beta rows = (%d, %v), want (2, nil): another company's rows are not in the difference", got, err)
	}

	// The FTS index has to lose the row too, or `search` keeps answering with
	// a record the mirror no longer holds. The surviving row proves the search
	// itself works, so the empty result below means something.
	if hits, err := db.Search("Contact", 10, "contacts"); err != nil || len(hits) == 0 {
		t.Fatalf("search for a surviving row returned (%d hits, %v); the FTS assertion below would be vacuous", len(hits), err)
	}
	hits, err := db.Search("Contact 3", 10, "contacts")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, hit := range hits {
		if strings.Contains(string(hit), `"contactId":3`) {
			t.Fatalf("search still returns the deleted row: %s", hit)
		}
	}

	// The typed table is keyed by the same storage id and is what `doctor` and
	// the MCP sql tool read; leaving the row there would trade a stale mirror
	// for a mirror that disagrees with itself.
	var typed int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM "contacts" WHERE id = ?`, keys[2]).Scan(&typed); err != nil {
		t.Fatalf("count typed row: %v", err)
	}
	if typed != 0 {
		t.Fatalf("typed contacts row for the deleted id survived (%d)", typed)
	}
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM "contacts"`).Scan(&typed); err != nil {
		t.Fatalf("count typed rows: %v", err)
	}
	if typed != 4 {
		t.Fatalf("typed contacts rows = %d, want 4 (two alpha, two beta)", typed)
	}
}

// TestDeleteCompanyResourcesNotIn_EmptyKeepSetClearsTheCompany: an empty set is
// the API saying the collection is empty, which the caller has already checked
// against Fiken-Api-Result-Count. Every scoped row goes; nobody else's does.
func TestDeleteCompanyResourcesNotIn_EmptyKeepSetClearsTheCompany(t *testing.T) {
	db := openDeleteTestStore(t)
	seedContacts(t, db, "alpha", 1, 2)
	seedContacts(t, db, "beta", 3)

	removed, err := db.DeleteCompanyResourcesNotIn("contacts", "alpha", nil)
	if err != nil {
		t.Fatalf("DeleteCompanyResourcesNotIn: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	if got, err := db.CountCompanyResources("contacts", "alpha"); err != nil || got != 0 {
		t.Fatalf("alpha rows = (%d, %v), want (0, nil)", got, err)
	}
	if got, err := db.CountCompanyResources("contacts", "beta"); err != nil || got != 1 {
		t.Fatalf("beta rows = (%d, %v), want (1, nil)", got, err)
	}
}

// TestDeleteCompanyResourcesNotIn_LeavesBareLegacyRowsAlone: a row whose id
// carries no `id\0<slug>` suffix predates the storage-key fix and cannot be
// attributed to this company's pull — two companies' rows collided on that
// primary key, which is the whole reason the suffix exists. The sweep must not
// take it on the strength of an absence. (`sync --full --company` clears those
// deliberately; that is a repair the operator asked for.)
func TestDeleteCompanyResourcesNotIn_LeavesBareLegacyRowsAlone(t *testing.T) {
	db := openDeleteTestStore(t)
	keys := seedContacts(t, db, "alpha", 1)

	// The pre-fix shape: parent_id in the blob, bare id as the primary key.
	if _, err := db.db.Exec(
		`INSERT INTO resources (id, resource_type, data, synced_at, updated_at)
		 VALUES (?, 'contacts', ?, datetime('now'), datetime('now'))`,
		"9999", `{"contactId":9999,"name":"Legacy","parent_id":"alpha"}`,
	); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	removed, err := db.DeleteCompanyResourcesNotIn("contacts", "alpha", keys)
	if err != nil {
		t.Fatalf("DeleteCompanyResourcesNotIn: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0: a bare legacy row is not this company's row to delete", removed)
	}
	var n int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM resources WHERE id = '9999'`).Scan(&n); err != nil {
		t.Fatalf("count legacy row: %v", err)
	}
	if n != 1 {
		t.Fatalf("legacy row count = %d, want 1", n)
	}
}

// TestDeleteCompanyResourcesNotIn_KeepSetLargerThanTheSQLVariableLimit is why
// the difference is computed in Go rather than as a SQL `NOT IN (?, ?, …)`:
// journal_entries holds tens of thousands of rows for a single company, far
// past what SQLite will bind in one statement, and a chunked NOT IN would
// delete every row absent from the chunk it happened to be compared against.
// 2 500 kept rows and 300 stale ones is well over the 999-variable default.
func TestDeleteCompanyResourcesNotIn_KeepSetLargerThanTheSQLVariableLimit(t *testing.T) {
	db := openDeleteTestStore(t)
	ids := make([]int, 0, 2800)
	for i := 0; i < 2800; i++ {
		ids = append(ids, 10000+i)
	}
	keys := seedContacts(t, db, "alpha", ids...)
	if got, err := db.CountCompanyResources("contacts", "alpha"); err != nil || got != 2800 {
		t.Fatalf("seeded rows = (%d, %v), want (2800, nil)", got, err)
	}

	removed, err := db.DeleteCompanyResourcesNotIn("contacts", "alpha", keys[:2500])
	if err != nil {
		t.Fatalf("DeleteCompanyResourcesNotIn: %v", err)
	}
	if removed != 300 {
		t.Fatalf("removed = %d, want 300", removed)
	}
	if got, err := db.CountCompanyResources("contacts", "alpha"); err != nil || got != 2500 {
		t.Fatalf("surviving rows = (%d, %v), want (2500, nil)", got, err)
	}
	// A second sweep with the same set is a no-op: the difference is empty.
	removed, err = db.DeleteCompanyResourcesNotIn("contacts", "alpha", keys[:2500])
	if err != nil || removed != 0 {
		t.Fatalf("second sweep removed (%d, %v), want (0, nil)", removed, err)
	}
}

// TestDeleteCompanyResourcesNotIn_RefusesUnscopableCalls: an empty slug would
// be an unscoped wipe of a resource type, and a resource whose rows carry a
// bare id (companies, the flat walk) has no company scope in its key at all.
// Both are refused rather than silently widened.
func TestDeleteCompanyResourcesNotIn_RefusesUnscopableCalls(t *testing.T) {
	db := openDeleteTestStore(t)
	seedContacts(t, db, "alpha", 1, 2)

	if _, err := db.DeleteCompanyResourcesNotIn("contacts", "  ", nil); err == nil {
		t.Fatalf("an empty slug was accepted; that is an unscoped wipe")
	}
	if _, err := db.DeleteCompanyResourcesNotIn("companies", "alpha", nil); err == nil {
		t.Fatalf("a resource without a parent-keyed storage key was accepted")
	}
	if _, err := db.DeleteCompanyResourcesNotIn("not_a_resource", "alpha", nil); err == nil {
		t.Fatalf("an unknown resource name was accepted")
	}
	if got, err := db.CountCompanyResources("contacts", "alpha"); err != nil || got != 2 {
		t.Fatalf("alpha rows = (%d, %v), want (2, nil): a refused call must not delete", got, err)
	}
}

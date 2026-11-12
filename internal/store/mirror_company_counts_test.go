// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — tests for the per-company row counts and for the
// typed-table half of the two delete paths (issue #29).
package store

import (
	"encoding/json"
	"fmt"
	"testing"
)

func seedInbox(t *testing.T, s *Store, slug string, ids ...int) {
	t.Helper()
	items := make([]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		items = append(items, json.RawMessage(fmt.Sprintf(`{"documentId": %d, "filename": "doc-%d.pdf", "parent_id": %q}`, id, id, slug)))
	}
	if _, _, err := s.UpsertBatch("inbox", items); err != nil {
		t.Fatalf("seed inbox/%s: %v", slug, err)
	}
}

// staleTypedRow writes a typed inbox row with no generic counterpart, the way
// a document keyed on its filename before the id-key fix was left behind.
func staleTypedRow(t *testing.T, s *Store, id, slug string) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO "inbox" (id, data, parent_id) VALUES (?, ?, ?)`,
		id+"\x00"+slug, `{"filename":"`+id+`"}`, slug); err != nil {
		t.Fatalf("stale typed row: %v", err)
	}
}

func companyCounts(t *testing.T, s *Store, resource string) map[string]CompanyRowCounts {
	t.Helper()
	counts, ok, err := s.CountByCompany(resource)
	if err != nil || !ok {
		t.Fatalf("CountByCompany(%s): ok=%v err=%v", resource, ok, err)
	}
	out := map[string]CompanyRowCounts{}
	for _, c := range counts {
		out[c.Company] = c
	}
	return out
}

func TestCountByCompany(t *testing.T) {
	s := openTempStore(t)
	seedInbox(t, s, "agensia", 1, 2, 3)
	seedInbox(t, s, "other-co", 1, 2)
	staleTypedRow(t, s, "doc-1.pdf", "other-co")
	// A bare-key generic row and a row that names no company.
	if err := s.Upsert("inbox", "77", json.RawMessage(`{"documentId": 77, "parent_id": "agensia"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert("inbox", "88", json.RawMessage(`{"documentId": 88}`)); err != nil {
		t.Fatal(err)
	}

	got := companyCounts(t, s, "inbox")
	if c := got["agensia"]; c.Resources != 4 || c.BareKeys != 1 || !c.HasTyped {
		t.Errorf("agensia = %+v, want 4 resources rows, 1 of them bare", c)
	}
	if c := got["other-co"]; c.Resources != 2 || c.Typed != 3 || c.BareKeys != 0 {
		t.Errorf("other-co = %+v, want 2 resources rows against 3 typed", c)
	}
	if c, ok := got[""]; !ok || c.Resources != 1 {
		t.Errorf("unowned = %+v (present %v), want the one row that names no company", c, ok)
	}

	if _, ok, err := s.CountByCompany("companies"); ok || err != nil {
		t.Errorf("companies has no company column: ok=%v err=%v, want false/nil", ok, err)
	}
}

func TestClearCompanyResources_ClearsTheTypedTableToo(t *testing.T) {
	s := openTempStore(t)
	seedInbox(t, s, "agensia", 1, 2)
	seedInbox(t, s, "other-co", 1)
	staleTypedRow(t, s, "doc-1.pdf", "agensia")
	staleTypedRow(t, s, "doc-1.pdf", "other-co")

	if _, err := s.ClearCompanyResources("inbox", "agensia"); err != nil {
		t.Fatalf("ClearCompanyResources: %v", err)
	}
	got := companyCounts(t, s, "inbox")
	if c, left := got["agensia"]; left && (c.Resources != 0 || c.Typed != 0) {
		t.Errorf("agensia = %+v, want nothing left in either table", c)
	}
	if c := got["other-co"]; c.Resources != 1 || c.Typed != 2 {
		t.Errorf("other-co = %+v, want its rows untouched (1 generic, 2 typed)", c)
	}
}

func TestDeleteCompanyResourcesNotIn_SweepsTypedRowsWithNoGenericRow(t *testing.T) {
	s := openTempStore(t)
	seedInbox(t, s, "agensia", 1, 2)
	seedInbox(t, s, "other-co", 1)
	staleTypedRow(t, s, "doc-1.pdf", "agensia")
	staleTypedRow(t, s, "doc-9.pdf", "other-co")

	keep := []string{"1\x00agensia", "2\x00agensia"}
	removed, err := s.DeleteCompanyResourcesNotIn("inbox", "agensia", keep)
	if err != nil {
		t.Fatalf("DeleteCompanyResourcesNotIn: %v", err)
	}
	// The stale typed row was never a document in Fiken, so it is not counted
	// as a deletion; it is just gone.
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (nothing was deleted in Fiken)", removed)
	}
	got := companyCounts(t, s, "inbox")
	if c := got["agensia"]; c.Resources != 2 || c.Typed != 2 {
		t.Errorf("agensia = %+v, want the typed table back in step with resources (2/2)", c)
	}
	if c := got["other-co"]; c.Typed != 2 {
		t.Errorf("other-co typed = %d, want 2 (another company's stale row was swept)", c.Typed)
	}
}

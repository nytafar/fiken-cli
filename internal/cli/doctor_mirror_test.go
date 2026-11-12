// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — tests for the per-company mirror findings and
// `doctor --company` (issue #29).
package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// threeCompanyMirror is a mirror where company `agensia` is clean and two
// others are not: `other-co` holds bare-key journal entries and `third-co` a
// typed inbox table out of step with resources.
func threeCompanyMirror(t *testing.T) []mirrorFinding {
	t.Helper()
	db := openTestStore(t)
	batch := func(resource string, items ...string) {
		raw := make([]json.RawMessage, len(items))
		for i, it := range items {
			raw[i] = json.RawMessage(it)
		}
		if _, _, err := db.UpsertBatch(resource, raw); err != nil {
			t.Fatalf("seed %s: %v", resource, err)
		}
	}
	batch("journal_entries",
		`{"journalEntryId": 1, "parent_id": "agensia"}`, `{"journalEntryId": 2, "parent_id": "agensia"}`,
		`{"journalEntryId": 1, "parent_id": "other-co"}`)
	batch("inbox", `{"documentId": 5, "parent_id": "agensia"}`, `{"documentId": 5, "parent_id": "third-co"}`)
	// Pre-fix rows: a bare-key journal entry for other-co, a typed inbox row
	// with no generic counterpart for third-co, and a row naming no company.
	if err := db.Upsert("journal_entries", "9", json.RawMessage(`{"journalEntryId": 9, "parent_id": "other-co"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().Exec(`INSERT INTO "inbox" (id, data, parent_id) VALUES (?, '{}', 'third-co')`, "old.pdf\x00third-co"); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert("inbox", "77", json.RawMessage(`{"documentId": 77}`)); err != nil {
		t.Fatal(err)
	}
	for resource, n := range map[string]int{"journal_entries": 4, "inbox": 3} {
		if err := db.SaveSyncState(resource, "", n); err != nil {
			t.Fatalf("sync_state %s: %v", resource, err)
		}
	}
	return mirrorInvariantFindings(db)
}

func TestMirrorFindings_NameTheCompanyTheyBelongTo(t *testing.T) {
	findings := threeCompanyMirror(t)
	got := map[string]mirrorFinding{}
	for _, f := range findings {
		got[fmt.Sprintf("%s/%s/%s", f.Resource, f.Company, f.Kind)] = f
	}
	for _, f := range findings {
		if f.Company == "agensia" {
			t.Errorf("the clean company has a finding: %+v", f)
		}
	}
	bare, ok := got["journal_entries/other-co/bare_storage_key"]
	if !ok {
		t.Fatalf("no bare_storage_key finding for other-co: %+v", findings)
	}
	if bare.Fix != "fiken-cli sync --full --company other-co --resources journal_entries" {
		t.Errorf("fix = %q, want the slug named in the command", bare.Fix)
	}
	if !strings.Contains(bare.Message, "other-co: 1 of 2") {
		t.Errorf("message = %q, want that company's own counts", bare.Message)
	}
	if f, ok := got["inbox/third-co/row_count_disagreement"]; !ok || !strings.Contains(f.Message, "resources=1, inbox table=2") {
		t.Errorf("third-co inbox disagreement = %+v (present %v)", f, ok)
	}
	if f, ok := got["inbox//unowned_rows"]; !ok || f.Fix != "" {
		t.Errorf("unowned rows = %+v (present %v), want a finding that names no company and promises no fix", f, ok)
	}
}

func TestScopeMirrorFindings(t *testing.T) {
	report := func() map[string]any {
		return map[string]any{"cache": map[string]any{"mirror_findings": mirrorFindingsJSON(threeCompanyMirror(t))}}
	}
	findingsOf := func(r map[string]any) []map[string]any {
		f, _ := r["cache"].(map[string]any)["mirror_findings"].([]map[string]any)
		return f
	}

	clean := report()
	all := len(findingsOf(clean))
	scopeMirrorFindings(clean, "agensia")
	cache := clean["cache"].(map[string]any)
	if got := findingsOf(clean); got == nil || len(got) != 0 {
		t.Fatalf("doctor --company agensia = %+v, want an empty list for the clean company", got)
	}
	if cache["mirror_company"] != "agensia" || cache["mirror_findings_other_companies"] != all {
		t.Fatalf("cache = %v, want the scope and the %d findings left out recorded", cache, all)
	}

	dirty := report()
	scopeMirrorFindings(dirty, "other-co")
	for _, f := range findingsOf(dirty) {
		if f["company"] != "other-co" {
			t.Errorf("a finding for another company survived the scope: %v", f)
		}
	}
	if len(findingsOf(dirty)) == 0 {
		t.Fatal("other-co's own findings were dropped")
	}

	unscoped := report()
	scopeMirrorFindings(unscoped, "")
	if len(findingsOf(unscoped)) != all {
		t.Fatal("no --company must leave the report as it is")
	}
}

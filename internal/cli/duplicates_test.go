// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the pure duplicate detector.
package cli

import "testing"

func TestFindDuplicateGroups(t *testing.T) {
	t.Run("empty input yields no groups", func(t *testing.T) {
		if got := findDuplicateGroups(nil, 5); len(got) != 0 {
			t.Fatalf("got %d groups, want 0", len(got))
		}
	})

	t.Run("same contact and amount within window groups together", func(t *testing.T) {
		cands := []dupCandidate{
			{DocType: "purchase", DocID: 1, Date: "2026-01-01", ContactID: 9, ContactName: "Acme", GrossOre: 12500},
			{DocType: "purchase", DocID: 2, Date: "2026-01-03", ContactID: 9, ContactName: "Acme", GrossOre: 12500},
		}
		got := findDuplicateGroups(cands, 5)
		if len(got) != 1 {
			t.Fatalf("got %d groups %+v, want 1", len(got), got)
		}
		if len(got[0].Docs) != 2 || got[0].AmountOre != 12500 {
			t.Errorf("group = %+v; want 2 docs at 12500", got[0])
		}
	})

	t.Run("dates beyond window do not group", func(t *testing.T) {
		cands := []dupCandidate{
			{DocType: "purchase", DocID: 1, Date: "2026-01-01", ContactID: 9, GrossOre: 12500},
			{DocType: "purchase", DocID: 2, Date: "2026-01-20", ContactID: 9, GrossOre: 12500},
		}
		if got := findDuplicateGroups(cands, 5); len(got) != 0 {
			t.Fatalf("got %d groups, want 0 (19 days apart > 5)", len(got))
		}
	})

	t.Run("different amount does not group", func(t *testing.T) {
		cands := []dupCandidate{
			{DocType: "purchase", DocID: 1, Date: "2026-01-01", ContactID: 9, GrossOre: 12500},
			{DocType: "purchase", DocID: 2, Date: "2026-01-02", ContactID: 9, GrossOre: 99999},
		}
		if got := findDuplicateGroups(cands, 5); len(got) != 0 {
			t.Fatalf("got %d groups, want 0 (different amounts)", len(got))
		}
	})

	t.Run("different contact does not group", func(t *testing.T) {
		cands := []dupCandidate{
			{DocType: "purchase", DocID: 1, Date: "2026-01-01", ContactID: 9, GrossOre: 12500},
			{DocType: "purchase", DocID: 2, Date: "2026-01-02", ContactID: 10, GrossOre: 12500},
		}
		if got := findDuplicateGroups(cands, 5); len(got) != 0 {
			t.Fatalf("got %d groups, want 0 (different contacts)", len(got))
		}
	})

	t.Run("sales and purchases never cross-group even when identical", func(t *testing.T) {
		cands := []dupCandidate{
			{DocType: "purchase", DocID: 1, Date: "2026-01-01", ContactID: 9, GrossOre: 12500},
			{DocType: "sale", DocID: 2, Date: "2026-01-01", ContactID: 9, GrossOre: 12500},
		}
		if got := findDuplicateGroups(cands, 5); len(got) != 0 {
			t.Fatalf("got %d groups, want 0 (different doc types)", len(got))
		}
	})

	t.Run("window zero requires same day", func(t *testing.T) {
		cands := []dupCandidate{
			{DocType: "sale", DocID: 1, Date: "2026-01-01", ContactID: 3, GrossOre: 500},
			{DocType: "sale", DocID: 2, Date: "2026-01-01", ContactID: 3, GrossOre: 500},
			{DocType: "sale", DocID: 3, Date: "2026-01-02", ContactID: 3, GrossOre: 500},
		}
		got := findDuplicateGroups(cands, 0)
		if len(got) != 1 || len(got[0].Docs) != 2 {
			t.Fatalf("got %+v; want a single 2-doc same-day group", got)
		}
	})
}

func TestDuplicateFindings(t *testing.T) {
	t.Run("no groups yields no findings", func(t *testing.T) {
		if got := duplicateFindings(nil); len(got) != 0 {
			t.Fatalf("got %d findings, want 0", len(got))
		}
	})

	t.Run("a group becomes one finding keyed on its earliest document", func(t *testing.T) {
		groups := []duplicateGroup{{
			DocType:     "purchase",
			ContactID:   9,
			ContactName: "Acme",
			AmountOre:   12500,
			Docs: []duplicateDoc{
				{DocID: 1, Date: "2026-01-01"},
				{DocID: 2, Date: "2026-01-03"},
			},
		}}
		got := duplicateFindings(groups)
		if len(got) != 1 {
			t.Fatalf("got %d findings %+v; want 1", len(got), got)
		}
		f := got[0]
		if f.Kind != "duplicate_group" || f.Severity != SeverityWarning {
			t.Errorf("kind/severity = %q/%q; want duplicate_group/warning", f.Kind, f.Severity)
		}
		if f.DocType != "purchase" || f.DocID != 1 || f.Date != "2026-01-01" {
			t.Errorf("finding identity = %+v; want purchase #1 on 2026-01-01", f)
		}
		if f.ContactID != 9 || f.ContactName != "Acme" {
			t.Errorf("contact = %d/%q; want 9/Acme", f.ContactID, f.ContactName)
		}
		// The shared gross is the impact — the money at stake if it really was
		// booked twice.
		if f.ImpactOre != 12500 {
			t.Errorf("impact = %d; want 12500", f.ImpactOre)
		}
		if f.Detail["group_size"] != 2 {
			t.Errorf("group_size = %v; want 2", f.Detail["group_size"])
		}
		ids, _ := f.Detail["doc_ids"].([]int64)
		dates, _ := f.Detail["dates"].([]string)
		if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
			t.Errorf("doc_ids = %v; want [1 2]", f.Detail["doc_ids"])
		}
		if len(dates) != 2 || dates[1] != "2026-01-03" {
			t.Errorf("dates = %v; want both member dates", f.Detail["dates"])
		}
	})

	t.Run("every member document survives in the detail", func(t *testing.T) {
		groups := []duplicateGroup{{
			DocType:   "sale",
			AmountOre: 500,
			Docs: []duplicateDoc{
				{DocID: 7, Date: "2026-02-01"},
				{DocID: 8, Date: "2026-02-01"},
				{DocID: 9, Date: "2026-02-02"},
			},
		}}
		got := duplicateFindings(groups)
		if len(got) != 1 {
			t.Fatalf("got %d findings; want 1", len(got))
		}
		if ids, _ := got[0].Detail["doc_ids"].([]int64); len(ids) != 3 {
			t.Errorf("doc_ids = %v; want all 3 members", got[0].Detail["doc_ids"])
		}
	})
}

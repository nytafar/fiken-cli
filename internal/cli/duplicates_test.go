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

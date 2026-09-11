// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The store half of issue #18. invoiceId and creditNoteId are in no generic
// fallback list, so without the override entries every row of the two new
// dependents is an extract failure and nothing lands at all; and without the
// parent-key entries the rows land under a bare id, which is the issue #6
// collision (two companies share invoiceId ranges) all over again.

package store

import (
	"encoding/json"
	"testing"
)

func TestExtractResourceID_InvoicesAndCreditNotes(t *testing.T) {
	cases := []struct {
		resource string
		item     string
		want     string
	}{
		{"invoices", `{"invoiceId":4001,"invoiceNumber":"10001","issueDate":"2026-01-05"}`, "4001"},
		{"credit_notes", `{"creditNoteId":9001,"creditNoteNumber":"5001"}`, "9001"},
	}
	for _, tc := range cases {
		obj, err := DecodeJSONObject(json.RawMessage(tc.item))
		if err != nil {
			t.Fatalf("%s: %v", tc.resource, err)
		}
		if got := ExtractResourceID(tc.resource, obj); got != tc.want {
			t.Errorf("ExtractResourceID(%q) = %q, want %q", tc.resource, got, tc.want)
		}
	}
}

// TestStorageKeyOf_InvoicesAndCreditNotesCarryTheCompany pins the parent-key
// entry: the key is id\0<companySlug>, so the same invoiceId in two books is
// two rows rather than one overwriting the other.
func TestStorageKeyOf_InvoicesAndCreditNotesCarryTheCompany(t *testing.T) {
	nul := string([]byte{0})
	cases := []struct {
		resource string
		item     string
		want     string
	}{
		{"invoices", `{"invoiceId":4001,"parent_id":"agensia"}`, "4001" + nul + "agensia"},
		{"invoices", `{"invoiceId":4001,"parent_id":"otherco"}`, "4001" + nul + "otherco"},
		{"credit_notes", `{"creditNoteId":9001,"parent_id":"agensia"}`, "9001" + nul + "agensia"},
	}
	for _, tc := range cases {
		got, ok := StorageKeyOf(tc.resource, json.RawMessage(tc.item))
		if !ok {
			t.Fatalf("StorageKeyOf(%q, %s) did not land", tc.resource, tc.item)
		}
		if got != tc.want {
			t.Errorf("StorageKeyOf(%q, %s) = %q, want %q", tc.resource, tc.item, got, tc.want)
		}
	}
}

// TestCanonicalResource_InvoicesAndCreditNotesKebab: the generated read and
// mutation commands address these two by their kebab spelling, and the sync
// registry by the snake one. Both must reach the same rows.
func TestCanonicalResource_InvoicesAndCreditNotesKebab(t *testing.T) {
	for spelled, want := range map[string]string{
		"credit-notes": "credit_notes",
		"credit_notes": "credit_notes",
		"invoices":     "invoices",
	} {
		got, err := CanonicalResource(spelled)
		if err != nil {
			t.Fatalf("CanonicalResource(%q): %v", spelled, err)
		}
		if got != want {
			t.Errorf("CanonicalResource(%q) = %q, want %q", spelled, got, want)
		}
	}
	for _, name := range []string{"invoices", "credit_notes"} {
		if resourceParentKeyColumns[name] != "parent_id" {
			t.Errorf("resourceParentKeyColumns[%q] = %q, want parent_id", name, resourceParentKeyColumns[name])
		}
	}
}

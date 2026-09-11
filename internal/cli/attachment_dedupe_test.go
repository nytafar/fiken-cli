// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Issue #10: the Fiken API returns byte-identical duplicate entries in
// saleAttachments, so anything counting attachments counts every document
// twice. Pins the collapse, the cases that must NOT collapse (two distinct
// attachments, an entry with no identity at all), which resources the dedupe
// applies to, and that it happens before the upsert so the stored blob — what
// the detectors and the MCP sql tool read — is the clean one.

package cli

import (
	"encoding/json"
	"testing"
)

// attachmentsOf returns the named attachment array of one document blob.
func attachmentsOf(t *testing.T, item json.RawMessage, field string) []map[string]any {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(item, &doc); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	raw, ok := doc[field]
	if !ok {
		return nil
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", field, err)
	}
	return out
}

// saleWith wraps attachment entries in a sale document.
func saleWith(entries string) json.RawMessage {
	return json.RawMessage(`{"saleId":4001,"parent_id":"testco","saleAttachments":` + entries + `}`)
}

// TestDedupeAttachmentArrays_CollapsesTheDuplicatePair is the observed payload:
// two byte-identical entries where the book holds one document.
func TestDedupeAttachmentArrays_CollapsesTheDuplicatePair(t *testing.T) {
	entry := `{"identifier":"bilag-1","downloadUrl":"https://fiken.no/a/1","uuid":"11111111-1111-1111-1111-111111111111","filename":"faktura.pdf","type":"invoice"}`
	items := []json.RawMessage{saleWith(`[` + entry + `,` + entry + `]`)}

	got := dedupeAttachmentArrays("sales", items)
	atts := attachmentsOf(t, got[0], "saleAttachments")
	if len(atts) != 1 {
		t.Fatalf("saleAttachments = %d entries, want 1: %s", len(atts), got[0])
	}
	if atts[0]["filename"] != "faktura.pdf" || atts[0]["uuid"] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("surviving entry lost fields: %v", atts[0])
	}
	// The rest of the document is untouched.
	var doc map[string]any
	if err := json.Unmarshal(got[0], &doc); err != nil {
		t.Fatal(err)
	}
	if doc["saleId"] != float64(4001) || doc["parent_id"] != "testco" {
		t.Errorf("document fields changed: %v", doc)
	}
	// The caller's slice still holds what the API sent.
	if n := len(attachmentsOf(t, items[0], "saleAttachments")); n != 2 {
		t.Errorf("input item was mutated: %d entries, want the original 2", n)
	}
}

// TestDedupeAttachmentArrays_KeepsDistinctAttachments: two real documents on
// one sale must stay two. This is the failure mode a careless dedupe has.
func TestDedupeAttachmentArrays_KeepsDistinctAttachments(t *testing.T) {
	items := []json.RawMessage{saleWith(`[
		{"uuid":"aaaa","identifier":"bilag-1","downloadUrl":"https://fiken.no/a/1","filename":"faktura.pdf"},
		{"uuid":"bbbb","identifier":"bilag-2","downloadUrl":"https://fiken.no/a/2","filename":"kvittering.pdf"}
	]`)}
	got := dedupeAttachmentArrays("sales", items)
	if n := len(attachmentsOf(t, got[0], "saleAttachments")); n != 2 {
		t.Fatalf("two distinct attachments collapsed to %d", n)
	}
	// Nothing changed, so the blob is the very same bytes.
	if string(got[0]) != string(items[0]) {
		t.Errorf("an unaffected document was rewritten:\n got %s\nwant %s", got[0], items[0])
	}
}

// TestDedupeAttachmentArrays_KeepsEntriesWithoutAnyKey: no uuid, no identifier,
// no downloadUrl means no evidence two entries are the same document, so both
// stay. Dropping one would be data loss to fix a cosmetic count.
func TestDedupeAttachmentArrays_KeepsEntriesWithoutAnyKey(t *testing.T) {
	items := []json.RawMessage{saleWith(`[
		{"comment":"scanned","type":"invoice"},
		{"comment":"scanned","type":"invoice"},
		{"uuid":"cccc"},
		{"uuid":"cccc"}
	]`)}
	got := dedupeAttachmentArrays("sales", items)
	atts := attachmentsOf(t, got[0], "saleAttachments")
	if len(atts) != 3 {
		t.Fatalf("saleAttachments = %d entries, want 3 (two keyless kept, one uuid pair collapsed): %s", len(atts), got[0])
	}
	if atts[0]["comment"] != "scanned" || atts[1]["comment"] != "scanned" || atts[2]["uuid"] != "cccc" {
		t.Errorf("order was not preserved: %v", atts)
	}
}

// TestDedupeAttachmentArrays_FallsBackThroughTheKeyChain: uuid is undocumented
// (spec.yaml's attachment schema has only identifier, downloadUrl, comment and
// type), so a payload without it must still dedupe.
func TestDedupeAttachmentArrays_FallsBackThroughTheKeyChain(t *testing.T) {
	cases := []struct {
		name    string
		entries string
		want    int
	}{
		{"identifier when uuid is absent", `[{"identifier":"bilag-1","downloadUrl":"https://fiken.no/a/1"},{"identifier":"bilag-1","downloadUrl":"https://fiken.no/a/1"}]`, 1},
		{"downloadUrl when both are absent", `[{"downloadUrl":"https://fiken.no/a/1"},{"downloadUrl":"https://fiken.no/a/1"}]`, 1},
		{"uuid wins over a differing identifier", `[{"uuid":"aaaa","identifier":"x"},{"uuid":"aaaa","identifier":"y"}]`, 1},
		{"differing uuids are two documents", `[{"uuid":"aaaa","identifier":"x"},{"uuid":"bbbb","identifier":"x"}]`, 2},
		{"an empty key value is no key", `[{"uuid":"","identifier":""},{"uuid":"","identifier":""}]`, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dedupeAttachmentArrays("sales", []json.RawMessage{saleWith(tc.entries)})
			if n := len(attachmentsOf(t, got[0], "saleAttachments")); n != tc.want {
				t.Fatalf("saleAttachments = %d entries, want %d: %s", n, tc.want, got[0])
			}
		})
	}
}

// TestDedupeAttachmentArrays_AppliesToTheThreeCarriers: the field name differs
// per resource (purchaseAttachments, saleAttachments, the bare attachments),
// and no other resource has an inline attachment array to clean.
func TestDedupeAttachmentArrays_AppliesToTheThreeCarriers(t *testing.T) {
	entry := `{"uuid":"aaaa","identifier":"bilag-1"}`
	pair := `[` + entry + `,` + entry + `]`
	for _, tc := range []struct{ resource, field string }{
		{"sales", "saleAttachments"},
		{"purchases", "purchaseAttachments"},
		{"journal_entries", "attachments"},
	} {
		item := json.RawMessage(`{"id":1,"` + tc.field + `":` + pair + `}`)
		got := dedupeAttachmentArrays(tc.resource, []json.RawMessage{item})
		if n := len(attachmentsOf(t, got[0], tc.field)); n != 1 {
			t.Errorf("%s.%s = %d entries, want 1", tc.resource, tc.field, n)
		}
	}

	// A resource with no attachment array of its own is returned untouched,
	// even when it happens to carry a field by one of those names.
	for _, resource := range []string{"contacts", "invoices", "credit_notes", "transactions"} {
		item := json.RawMessage(`{"id":1,"attachments":` + pair + `,"saleAttachments":` + pair + `}`)
		got := dedupeAttachmentArrays(resource, []json.RawMessage{item})
		if string(got[0]) != string(item) {
			t.Errorf("%s was rewritten: %s", resource, got[0])
		}
	}
}

// TestUpsertResourceBatch_StoresTheDedupedBlob is the placement assertion: the
// clean array is what lands in the mirror, because that is what every detector
// and the MCP sql tool read.
func TestUpsertResourceBatch_StoresTheDedupedBlob(t *testing.T) {
	db := openTestStore(t)
	entry := `{"uuid":"aaaa","identifier":"bilag-1","downloadUrl":"https://fiken.no/a/1"}`
	items := []json.RawMessage{saleWith(`[` + entry + `,` + entry + `]`)}

	stored, extractFailures, err := upsertResourceBatch(db, "sales", items)
	if err != nil {
		t.Fatalf("upsertResourceBatch: %v", err)
	}
	if stored != 1 || extractFailures != 0 {
		t.Fatalf("upsertResourceBatch = (%d stored, %d extract failures), want (1, 0)", stored, extractFailures)
	}

	data, err := db.Get("sales", "4001"+string([]byte{0})+"testco")
	if err != nil {
		t.Fatalf("read back the sale: %v", err)
	}
	if n := len(attachmentsOf(t, data, "saleAttachments")); n != 1 {
		t.Fatalf("stored blob holds %d saleAttachments, want 1: %s", n, data)
	}
}

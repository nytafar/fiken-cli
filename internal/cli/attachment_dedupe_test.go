// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Issue #10: the Fiken API returns byte-identical duplicate entries in
// saleAttachments, so anything counting attachments counts every document
// twice. Pins the collapse, the cases that must NOT collapse (two distinct
// attachments, and above all two distinct files sharing one user-defined
// identifier), which resources the dedupe applies to, and that every path that
// writes the mirror — the sync walker and both write-through paths — stores
// the clean blob, because the detectors and the MCP sql tool read the STORED
// blob rather than the response.

package cli

import (
	"context"
	"encoding/json"
	"testing"

	"fiken-cli/internal/store"
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

// TestDedupeAttachmentArrays_KeepsDistinctFilesSharingAnIdentifier is the
// review finding: identifier is a USER-DEFINED label (spec.yaml:4559-4562,
// "Could be the Invoice Id or receipt number"), never a per-file key. An
// invoice PDF and its reminder on one sale share it, and keying on it would
// delete one real document.
func TestDedupeAttachmentArrays_KeepsDistinctFilesSharingAnIdentifier(t *testing.T) {
	items := []json.RawMessage{saleWith(`[
		{"identifier":"10001","downloadUrl":"https://fiken.no/a/1","filename":"faktura.pdf","type":"invoice"},
		{"identifier":"10001","downloadUrl":"https://fiken.no/a/2","filename":"purring.pdf","type":"reminder"}
	]`)}
	got := dedupeAttachmentArrays("sales", items)
	atts := attachmentsOf(t, got[0], "saleAttachments")
	if len(atts) != 2 {
		t.Fatalf("two files sharing one identifier collapsed to %d: %s", len(atts), got[0])
	}
	if atts[0]["filename"] != "faktura.pdf" || atts[1]["filename"] != "purring.pdf" {
		t.Errorf("wrong entries survived: %v", atts)
	}
	// Not even the downloadUrl is needed for that: filename alone is enough
	// to make them two documents.
	noURL := []json.RawMessage{saleWith(`[
		{"identifier":"10001","filename":"faktura.pdf","type":"invoice"},
		{"identifier":"10001","filename":"purring.pdf","type":"reminder"}
	]`)}
	if n := len(attachmentsOf(t, dedupeAttachmentArrays("sales", noURL)[0], "saleAttachments")); n != 2 {
		t.Errorf("with no downloadUrl, two files sharing one identifier collapsed to %d", n)
	}
}

// TestDedupeAttachmentArrays_KeylessEntriesCollapseOnlyWhenIdentical: with no
// uuid and no downloadUrl the entry's own canonical bytes are the key, so the
// byte-identical pair issue #10 reports still collapses while two entries that
// differ in any field both stay.
func TestDedupeAttachmentArrays_KeylessEntriesCollapseOnlyWhenIdentical(t *testing.T) {
	items := []json.RawMessage{saleWith(`[
		{"comment":"scanned","type":"invoice"},
		{"comment":"scanned","type":"invoice"},
		{"comment":"scanned","type":"reminder"},
		{"uuid":"cccc"},
		{"uuid":"cccc"}
	]`)}
	got := dedupeAttachmentArrays("sales", items)
	atts := attachmentsOf(t, got[0], "saleAttachments")
	if len(atts) != 3 {
		t.Fatalf("saleAttachments = %d entries, want 3 (identical keyless pair collapsed, the differing one kept, uuid pair collapsed): %s", len(atts), got[0])
	}
	if atts[0]["type"] != "invoice" || atts[1]["type"] != "reminder" || atts[2]["uuid"] != "cccc" {
		t.Errorf("order was not preserved: %v", atts)
	}
}

// TestDedupeAttachmentArrays_KeyOrderDoesNotDefeatTheByteKey: the bytes key is
// canonical JSON, not raw bytes, so the same entry serialised with its fields
// in another order is still one document.
func TestDedupeAttachmentArrays_KeyOrderDoesNotDefeatTheByteKey(t *testing.T) {
	items := []json.RawMessage{saleWith(`[
		{"comment":"scanned","type":"invoice"},
		{"type":"invoice","comment":"scanned"}
	]`)}
	if n := len(attachmentsOf(t, dedupeAttachmentArrays("sales", items)[0], "saleAttachments")); n != 1 {
		t.Fatalf("reordered fields kept %d entries, want 1", n)
	}
}

// TestDedupeAttachmentArrays_FallsBackThroughTheKeyChain: uuid is undocumented
// (spec.yaml's attachment schema has only identifier, downloadUrl, comment and
// type), so a payload without it must still dedupe — on downloadUrl, and
// failing that on the entry's own canonical bytes. identifier never keys
// anything.
func TestDedupeAttachmentArrays_FallsBackThroughTheKeyChain(t *testing.T) {
	cases := []struct {
		name    string
		entries string
		want    int
	}{
		{"downloadUrl when uuid is absent", `[{"identifier":"bilag-1","downloadUrl":"https://fiken.no/a/1","filename":"a.pdf"},{"identifier":"bilag-2","downloadUrl":"https://fiken.no/a/1","filename":"b.pdf"}]`, 1},
		{"downloadUrl alone", `[{"downloadUrl":"https://fiken.no/a/1"},{"downloadUrl":"https://fiken.no/a/1"}]`, 1},
		{"a shared identifier with differing downloadUrls stays two", `[{"identifier":"bilag-1","downloadUrl":"https://fiken.no/a/1"},{"identifier":"bilag-1","downloadUrl":"https://fiken.no/a/2"}]`, 2},
		{"uuid wins over a differing identifier", `[{"uuid":"aaaa","identifier":"x"},{"uuid":"aaaa","identifier":"y"}]`, 1},
		{"uuid wins over a differing downloadUrl", `[{"uuid":"aaaa","downloadUrl":"https://fiken.no/a/1"},{"uuid":"aaaa","downloadUrl":"https://fiken.no/a/2"}]`, 1},
		{"differing uuids are two documents", `[{"uuid":"aaaa","identifier":"x"},{"uuid":"bbbb","identifier":"x"}]`, 2},
		{"an empty key value falls through to the bytes", `[{"uuid":"","identifier":""},{"uuid":"","identifier":""}]`, 1},
		{"an empty key value with differing bytes stays two", `[{"uuid":"","filename":"a.pdf"},{"uuid":"","filename":"b.pdf"}]`, 2},
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

// nulByte is the separator store.StorageKeyOf puts between the API id and the
// company slug for a parent-keyed resource.
const nulByte = "\x00"

// openMirrorForTest opens the store the write-through paths write to, under the
// HOME the test redirected.
func openMirrorForTest(t *testing.T, ctx context.Context) *store.Store {
	t.Helper()
	db, err := store.OpenWithContext(ctx, defaultDBPath("fiken-cli"))
	if err != nil {
		t.Fatalf("open mirror: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestWriteThroughCache_StoresTheDedupedBlob is the second half of the
// placement claim: upsertResourceBatch is not the only path into the mirror.
// `sales list` and `sales get` on the default auto data source write through
// this function, so a mirror sync had just cleaned would be rewritten with the
// duplicate pair on the next read.
func TestWriteThroughCache_StoresTheDedupedBlob(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	entry := `{"uuid":"aaaa","identifier":"bilag-1","downloadUrl":"https://fiken.no/a/1"}`
	pair := `[` + entry + `,` + entry + `]`

	// list response: a bare array, the shape /companies/{slug}/sales returns
	writeThroughCache(ctx, "sales", "agensia",
		json.RawMessage(`[{"saleId":4001,"saleAttachments":`+pair+`}]`))
	// detail response: a single object, the shape `sales get` returns
	writeThroughCache(ctx, "sales", "agensia",
		json.RawMessage(`{"saleId":4002,"saleAttachments":`+pair+`}`))

	db := openMirrorForTest(t, ctx)
	for _, id := range []string{"4001", "4002"} {
		data, err := db.Get("sales", id+nulByte+"agensia")
		if err != nil {
			t.Fatalf("read back sale %s: %v", id, err)
		}
		if n := len(attachmentsOf(t, data, "saleAttachments")); n != 1 {
			t.Errorf("stored blob for sale %s holds %d saleAttachments, want 1: %s", id, n, data)
		}
	}
}

// TestWriteMutationResponseToStore_StoresTheDedupedBlob: the third path into
// the mirror. A mutation response carries the same duplicated array and lands
// in the same row, so it is cleaned on the same terms.
func TestWriteMutationResponseToStore_StoresTheDedupedBlob(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	entry := `{"uuid":"aaaa","identifier":"bilag-1","downloadUrl":"https://fiken.no/a/1"}`

	writeMutationResponseToStore(ctx, "sales",
		json.RawMessage(`{"saleId":4003,"saleAttachments":[`+entry+`,`+entry+`]}`),
		"", "/companies/agensia/sales")

	db := openMirrorForTest(t, ctx)
	data, err := db.Get("sales", "4003"+nulByte+"agensia")
	if err != nil {
		t.Fatalf("read back the sale: %v", err)
	}
	if n := len(attachmentsOf(t, data, "saleAttachments")); n != 1 {
		t.Fatalf("stored blob holds %d saleAttachments, want 1: %s", n, data)
	}
}

// TestWriteThroughCache_KeepsDistinctFilesSharingAnIdentifier: the write-through
// paths inherit the whole key chain, not just the collapse — two documents
// sharing a user-defined identifier stay two rows' worth of attachments.
func TestWriteThroughCache_KeepsDistinctFilesSharingAnIdentifier(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()

	writeThroughCache(ctx, "sales", "agensia", json.RawMessage(`[{"saleId":4004,"saleAttachments":[
		{"identifier":"10001","downloadUrl":"https://fiken.no/a/1","filename":"faktura.pdf"},
		{"identifier":"10001","downloadUrl":"https://fiken.no/a/2","filename":"purring.pdf"}
	]}]`))

	db := openMirrorForTest(t, ctx)
	data, err := db.Get("sales", "4004"+nulByte+"agensia")
	if err != nil {
		t.Fatalf("read back the sale: %v", err)
	}
	if n := len(attachmentsOf(t, data, "saleAttachments")); n != 2 {
		t.Fatalf("stored blob holds %d saleAttachments, want both files: %s", n, data)
	}
}

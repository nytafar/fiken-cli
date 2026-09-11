// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Attachment dedupe (issue #10). The Fiken API serves the same attachment
// twice: 78 of 85 synced sales on the test company carry a saleAttachments
// array holding two BYTE-IDENTICAL entries (same uuid, identifier,
// downloadUrl, filename, type). The mirror is not at fault — the upsert
// replaces the data blob wholesale, and a fresh re-sync leaves the array
// lengths unchanged — so the duplication arrives in the payload.
//
// Nothing breaks today because the only reader, missing-bilag, tests for a
// non-empty array. Anything that COUNTS or LISTS attachments shows every
// document twice, and the two readers that matter (the detectors and the MCP
// sql tool) both read the stored blob, not the response. So the dedupe happens
// on the way in, before the upsert, rather than at each read site: one place
// that cannot be forgotten by the next report.
//
// KEY ORDER is uuid, then downloadUrl, then the canonical bytes of the whole
// entry. uuid is what the live payload identifies an attachment by, but it is
// undocumented — spec.yaml's attachment schema (~4556) carries only
// identifier, downloadUrl, comment and type — so a payload without it must
// still dedupe, and downloadUrl is the only documented value that is per file.
//
// identifier is NOT a key at any position. spec.yaml:4559-4562 documents it as
// a USER-DEFINED label ("Could be the Invoice Id or receipt number for
// example") and no upload endpoint sets it per file, so an invoice PDF and a
// reminder PDF on the same sale routinely carry the same identifier; keying on
// it would delete one of two real documents.
//
// The last resort is the entry's canonical JSON — key order and whitespace
// normalised — because a byte-identical pair is exactly what issue #10
// reports, and two entries equal field for field carry no evidence of being
// two documents. An entry that is not JSON at all is KEPT AS IS. First
// occurrence wins and the order is preserved, so the blob a reader sees is the
// API's own order minus the repeats.
package cli

import (
	"encoding/json"

	"fiken-cli/internal/store"
)

// attachmentDedupeKeys is the per-file identity chain, in priority order.
// identifier is deliberately absent — it is a user-defined label, not a file
// key (spec.yaml:4559-4562) — and the canonical-bytes fallback below is what
// catches an entry carrying neither of these.
var attachmentDedupeKeys = []string{"uuid", "downloadUrl"}

// dedupeAttachmentArrays returns items with the parent document's attachment
// array collapsed to one entry per distinct attachment. Only the three
// resources that carry an inline attachment array are touched — the field name
// differs per resource, which is what attachmentsField (missing_bilag.go)
// already records — and anything else is returned untouched.
//
// The input slice is never modified: an item that changes is replaced in a
// copy, so a caller that still holds the original payload keeps holding it.
func dedupeAttachmentArrays(resource string, items []json.RawMessage) []json.RawMessage {
	canonical := resource
	if c, err := store.CanonicalResource(resource); err == nil {
		canonical = c
	}
	field, ok := attachmentsField[canonical]
	if !ok {
		return items
	}

	out := items
	copied := false
	for i, item := range items {
		deduped, changed := dedupeAttachmentsIn(item, field)
		if !changed {
			continue
		}
		if !copied {
			out = make([]json.RawMessage, len(items))
			copy(out, items)
			copied = true
		}
		out[i] = deduped
	}
	return out
}

// dedupeAttachmentsIn rewrites one document's attachment array. It returns
// changed=false — and the original bytes — whenever nothing would be dropped,
// so an unaffected document's blob stays byte-for-byte what the API sent.
func dedupeAttachmentsIn(item json.RawMessage, field string) (json.RawMessage, bool) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(item, &doc); err != nil {
		return item, false
	}
	raw, ok := doc[field]
	if !ok {
		return item, false
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil || len(entries) < 2 {
		return item, false
	}

	kept := make([]json.RawMessage, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		key, ok := attachmentIdentity(entry)
		if !ok {
			// Nothing to compare on: keep it. Unreachable for an element of an
			// array json.Unmarshal accepted — a guard, not a case — and the
			// only safe direction if it ever is reached, since dropping an
			// entry would be data loss to fix a cosmetic count.
			kept = append(kept, entry)
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		kept = append(kept, entry)
	}
	if len(kept) == len(entries) {
		return item, false
	}

	keptJSON, err := json.Marshal(kept)
	if err != nil {
		return item, false
	}
	doc[field] = keptJSON
	rewritten, err := json.Marshal(doc)
	if err != nil {
		return item, false
	}
	return rewritten, true
}

// attachmentIdentity returns the identity an attachment is deduped on: the
// first non-empty per-file key it carries, namespaced by the field the value
// came from so a downloadUrl that happens to equal another entry's uuid cannot
// collapse the two, and otherwise the entry's canonical bytes. ok=false means
// the entry is not JSON at all and has to be kept as is — which an element of
// an array json.Unmarshal already accepted never is.
func attachmentIdentity(entry json.RawMessage) (string, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(entry, &obj); err == nil {
		for _, key := range attachmentDedupeKeys {
			raw, ok := obj[key]
			if !ok {
				continue
			}
			var value string
			if err := json.Unmarshal(raw, &value); err != nil || value == "" {
				continue
			}
			return key + "\x00" + value, true
		}
	}
	canonical, ok := canonicalAttachmentBytes(entry)
	if !ok {
		return "", false
	}
	return "bytes\x00" + canonical, true
}

// canonicalAttachmentBytes renders an entry independently of key order and
// whitespace, so two entries the API serialised differently still compare
// equal. ok=false for bytes that are not JSON at all.
func canonicalAttachmentBytes(entry json.RawMessage) (string, bool) {
	var value any
	if err := json.Unmarshal(entry, &value); err != nil {
		return "", false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

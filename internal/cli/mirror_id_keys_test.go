// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// sync.go carries its own copy of the per-resource id-field table. This pins
// it to the store's copy for the resources whose entry was hand-set so the
// sync walker and the store cannot disagree on what a row is keyed by: the
// two fixed in issue #16 (accounts on `code`, inbox on `documentId`, neither
// on the display name) and the two added in issue #18 (invoices and credit
// notes, whose id fields are in no generic fallback list).

package cli

import (
	"testing"
)

func TestSyncIDFieldOverrides_MatchStoreForFixedResources(t *testing.T) {
	want := map[string]string{
		"accounts":     "code",
		"inbox":        "documentId",
		"credit_notes": "creditNoteId",
		"invoices":     "invoiceId",
	}
	for resource, field := range want {
		if got := resourceIDFieldOverrides[resource]; got != field {
			t.Errorf("sync resourceIDFieldOverrides[%q] = %q, want %q", resource, got, field)
		}
	}
}

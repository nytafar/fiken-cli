// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// sync.go carries its own copy of the per-resource id-field table. This pins
// it to the store's copy for the two resources fixed in issue #16 so the sync
// walker and the store cannot disagree on what an account or inbox row is
// keyed by.

package cli

import (
	"testing"
)

func TestSyncIDFieldOverrides_MatchStoreForFixedResources(t *testing.T) {
	want := map[string]string{"accounts": "code", "inbox": "documentId"}
	for resource, field := range want {
		if got := resourceIDFieldOverrides[resource]; got != field {
			t.Errorf("sync resourceIDFieldOverrides[%q] = %q, want %q", resource, got, field)
		}
	}
}

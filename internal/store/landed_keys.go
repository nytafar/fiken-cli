// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The one thing a caller outside this package cannot work out for itself: the
// primary key UpsertBatch lands a given item under. sync counts rows by
// summing UpsertBatch's `stored` counter, which counts ITEMS with an
// extractable id — so two items that share a storage key (the same id twice
// across a page boundary, or two rows whose key collapses, e.g. accounts keyed
// on `code`) count twice while the mirror holds one row. Comparing that sum
// with Fiken-Api-Result-Count therefore both invents anomalies (a duplicate on
// a page boundary) and hides the real one (a key collapse leaves the mirror
// short with the counters in agreement — the issue #12 failure class).
//
// ExtractResourceID is exported but resourceStorageID is not, and the storage
// key is id-plus-parent for every parent-keyed resource, so the key a row
// actually lands under is only computable in here.
//
// The promise is scoped to UpsertBatch. Rows that land through the single-object
// fallback (upsertSingleObject) are keyed by a different route entirely, so this
// file answers for the batch path only.
package store

import "encoding/json"

// StorageKeyOf returns the primary key UpsertBatch would store this item
// under, and false when the item would not land at all: unparseable JSON, an
// unknown resource name, or no extractable id (what UpsertBatch counts as an
// extract failure). The item must already carry whatever the sync walker adds
// before the upsert (parent_id on the dependent path), because the parent
// value is part of the key.
//
// UpsertBatch is the ONLY path this answers for. The single-object upsert the
// flat sync walker falls back to dispatches to a typed Upsert<Resource> keyed
// on extractObjectID (no resourceIDFieldOverrides), and stores a non-object
// body under the resource name with no id at all; a false from here for such a
// body means "not this path", not "nothing landed", so a caller counting rows
// must not treat a single-object response as comparable.
func StorageKeyOf(resourceType string, item json.RawMessage) (string, bool) {
	canonical, err := CanonicalResource(resourceType)
	if err != nil {
		return "", false
	}
	obj, err := DecodeJSONObject(item)
	if err != nil {
		return "", false
	}
	id := ExtractResourceID(canonical, obj)
	if id == "" {
		return "", false
	}
	return resourceStorageID(canonical, id, obj), true
}

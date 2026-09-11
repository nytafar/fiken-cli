// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The "rows" side of the result-count completeness check (issue #15), and the
// id set issue #17 (deletion detection) will read.
//
// sync used to compare Fiken-Api-Result-Count against the sum of UpsertBatch's
// `stored` counter. That counter counts ITEMS with an extractable id, not rows
// in the mirror, and the two differ in both directions:
//
//   - the same id served twice (a page boundary that shifts under a
//     concurrent write) counts twice, so a perfect mirror of 577 accounts
//     reports rows 578 against result_count 577 — a false anomaly;
//   - two items whose storage key collapses to one row (e.g. accounts key on
//     `code`, so a duplicated code overwrites) also count twice, which makes
//     the counters agree while the mirror is one row short — exactly the
//     issue #12 failure class the check exists to catch.
//
// Counting DISTINCT storage keys — the key store.UpsertBatch lands the row
// under — makes "rows" mean rows. The set therefore covers the UpsertBatch
// path and nothing else; a walk that stored a row any other way (the flat
// walker's single-object branch) is not comparable at all and says so with its
// truncated flag. The set keeps insertion order so issue #17
// can diff "ids the API served for this parent" against "ids the mirror holds"
// without a second walk.
package cli

import (
	"encoding/json"

	"fiken-cli/internal/store"
)

// landedIDs is the set of distinct storage keys a walk landed for one
// collection: one (company, resource) on the dependent path, one resource on
// the flat path. All methods are nil-safe so a caller never has to guard.
type landedIDs struct {
	seen  map[string]struct{}
	order []string
}

func newLandedIDs() *landedIDs {
	return &landedIDs{seen: map[string]struct{}{}}
}

// addBatch records every item of a page that upsertResourceBatch would land.
// Call it with the items AS UPSERTED (the dependent path injects parent_id
// before the upsert and that value is part of the storage key), and only after
// the upsert succeeded, so the set never claims a row the store rejected.
func (l *landedIDs) addBatch(resource string, items []json.RawMessage) {
	for _, item := range items {
		l.add(resource, item)
	}
}

// add records a single item of a page addBatch is walking. It is only valid
// for items that go through store.UpsertBatch: the single-object branch of the
// flat walker calls upsertSingleObject, which keys the row by a different route
// (a typed upsert on extractObjectID, or the resource name itself when the body
// is not an object), so StorageKeyOf cannot say what landed there. That branch
// marks its walk non-comparable instead of feeding this set.
func (l *landedIDs) add(resource string, item json.RawMessage) {
	if l == nil {
		return
	}
	key, ok := store.StorageKeyOf(resource, item)
	if !ok {
		return
	}
	if l.seen == nil {
		l.seen = map[string]struct{}{}
	}
	if _, dup := l.seen[key]; dup {
		return
	}
	l.seen[key] = struct{}{}
	l.order = append(l.order, key)
}

// Len is the number of distinct rows this walk landed — the "rows" side of the
// Fiken-Api-Result-Count comparison.
func (l *landedIDs) Len() int {
	if l == nil {
		return 0
	}
	return len(l.order)
}

// Keys returns the storage keys in the order they were first seen. Issue #17
// (deletion detection) reads this to find mirror rows the API no longer serves.
func (l *landedIDs) Keys() []string {
	if l == nil {
		return nil
	}
	out := make([]string, len(l.order))
	copy(out, l.order)
	return out
}

// Reset starts a new collection, e.g. the next parent of a dependent sync.
func (l *landedIDs) Reset() {
	if l == nil {
		return
	}
	l.seen = map[string]struct{}{}
	l.order = l.order[:0]
}

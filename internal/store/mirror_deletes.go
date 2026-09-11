// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The store half of deletion detection (issue #17). Fiken has no negative-delta
// channel: products, projects, inbox documents, drafts, time entries and
// contact persons are hard-deleted with no flag and never listed again, so the
// only evidence a row is gone is its absence from a pull that saw the whole
// collection. That makes the delete a SET DIFFERENCE — mirror rows for one
// (resource, company) minus the storage keys the walk just landed — and the
// caller, not this file, is responsible for proving the pull was complete
// (see internal/cli/sync_deletes.go).
//
// Addressing matches ClearCompanyResources (same resource_type, same
// json_extract(data, '$.parent_id') predicate, same explicit-rowid FTS delete),
// so the two DELETE paths of the mirror cannot disagree about which rows belong
// to a company. Two deliberate differences:
//
//   - a row whose id carries no `id\0<parent>` suffix is a pre-canonicalisation
//     legacy row that some other company's sync may have written, and a
//     deletion sweep must never take it on the strength of an absence it cannot
//     attribute. The --full --company repair path clears those on purpose;
//     this one leaves them alone.
//   - the typed table row goes too, where the resource has one. Typed tables
//     are keyed by the same storage id (see upsertContactsTx and its siblings),
//     UpsertBatch writes both in one transaction, and `doctor` cross-checks the
//     two counts — so dropping only the generic row would trade a stale mirror
//     for a mirror that disagrees with itself, and the MCP sql tool would keep
//     serving the deleted record out of the typed table. That
//     ClearCompanyResources does NOT do this is a pre-existing gap in the
//     --full --company path, not a precedent worth copying; fixing it there is
//     a separate change.
package store

import (
	"fmt"
	"strings"
)

// DeleteCompanyResourcesNotIn deletes one company's rows of one canonical
// resource type whose storage key is NOT in keep, in a single transaction, and
// returns how many rows went. keep is the set of `resources.id` values a
// completed pull landed — the `id\0<slug>` form StorageKeyOf returns, which is
// what landedIDs.Keys() collects.
//
// An empty keep set is meaningful, not a mistake: the API answered "this
// collection holds nothing" and the caller verified that against
// Fiken-Api-Result-Count, so every scoped row goes. An empty slug is still an
// error, so this can never degenerate into an unscoped wipe of a resource type.
//
// Only parent-keyed resources can be addressed this way. A resource whose rows
// are stored under a bare id (companies, the flat walk) has no company scope in
// its primary key at all, so scoping a delete to one company is not expressible
// and the call is refused rather than silently widened.
//
// The difference is computed in Go over a streamed scan of the company's own
// ids, not as a SQL `NOT IN (?, ?, …)`: journal_entries holds ~38k rows for a
// single company, far past SQLite's variable limit, and chunking a NOT IN would
// have to intersect the chunks by hand to avoid deleting a row that is merely
// absent from one chunk. Scanning once and testing membership in a map keeps
// the statement count proportional to the rows that actually go — usually zero.
func (s *Store) DeleteCompanyResourcesNotIn(resourceType, slug string, keep []string) (int, error) {
	canonical, err := CanonicalResource(resourceType)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(slug) == "" {
		return 0, fmt.Errorf("DeleteCompanyResourcesNotIn(%q): company slug is required", canonical)
	}
	if resourceParentKeyColumns[canonical] == "" {
		return 0, fmt.Errorf("DeleteCompanyResourcesNotIn(%q): resource is not parent-keyed, so its rows carry no company scope", canonical)
	}

	keepSet := make(map[string]struct{}, len(keep))
	for _, key := range keep {
		keepSet[key] = struct{}{}
	}
	// The suffix every row of this company must carry. instr() on the NUL
	// separator is how CountBareStorageKeys spots a legacy bare key; the same
	// test here keeps those rows out of the sweep.
	suffix := string([]byte{0}) + slug

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(
		`SELECT id FROM resources
		  WHERE resource_type = ? AND json_extract(data, '$.parent_id') = ?`,
		canonical, slug,
	)
	if err != nil {
		return 0, err
	}
	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		if !strings.HasSuffix(id, suffix) {
			continue // bare legacy row: not attributable to this company's pull
		}
		if _, ok := keepSet[id]; ok {
			continue
		}
		stale = append(stale, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, id := range stale {
		if _, err := tx.Exec(
			`DELETE FROM resources WHERE resource_type = ? AND id = ?`, canonical, id,
		); err != nil {
			return 0, err
		}
		// Same explicit-rowid pattern ClearCompanyResources and
		// upsertGenericResourceTx use: modernc's FTS5 will not honour
		// DELETE WHERE column = ? on a virtual table.
		if _, err := tx.Exec(
			`DELETE FROM resources_fts WHERE rowid = ?`, ftsRowID(canonical, id),
		); err != nil {
			return 0, err
		}
		if HasTypedTable(canonical) {
			// canonical comes out of the closed set CanonicalResource returns,
			// so it cannot carry a quote; it is still not a bindable
			// identifier, which is why CountTypedTable formats it the same way.
			if _, err := tx.Exec(
				fmt.Sprintf(`DELETE FROM "%s" WHERE id = ?`, canonical), id,
			); err != nil {
				return 0, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(stale), nil
}

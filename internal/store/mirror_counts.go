// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Row counts that let sync persist a real sync_state.total_count and let
// `doctor` cross-check the mirror's three views of the same resource
// (PLAN 2.2 items 5 and 6, issue #6 §1 and §3).
package store

import (
	"fmt"
	"sort"
	"strings"
)

// CountResources returns the number of `resources` rows of a canonical
// resource type, across every company. sync_state is keyed by resource_type
// alone, so this — not a per-company count — is the figure total_count must
// equal after a completed sync.
func (s *Store) CountResources(resourceType string) (int, error) {
	canonical, err := CanonicalResource(resourceType)
	if err != nil {
		return 0, err
	}
	var n int
	err = s.db.QueryRow(
		`SELECT COUNT(*) FROM resources WHERE resource_type = ?`, canonical,
	).Scan(&n)
	return n, err
}

// CountCompanyResources returns the number of `resources` rows of a canonical
// resource type whose data blob carries parent_id = slug. That is the exact
// predicate loadCompanyResources uses, so this counts the rows the detectors
// can actually see.
func (s *Store) CountCompanyResources(resourceType, slug string) (int, error) {
	canonical, err := CanonicalResource(resourceType)
	if err != nil {
		return 0, err
	}
	var n int
	err = s.db.QueryRow(
		`SELECT COUNT(*) FROM resources
		  WHERE resource_type = ? AND json_extract(data, '$.parent_id') = ?`,
		canonical, slug,
	).Scan(&n)
	return n, err
}

// CountTypedTable returns the row count of the domain-specific table for a
// canonical resource name. Returns ok=false when the resource has no typed
// table, so callers can skip the comparison instead of reporting a phantom
// disagreement.
func (s *Store) CountTypedTable(resourceType string) (int, bool, error) {
	canonical, err := CanonicalResource(resourceType)
	if err != nil {
		return 0, false, err
	}
	if !HasTypedTable(canonical) {
		return 0, false, nil
	}
	// canonical came out of the closed set above, so it cannot carry a quote;
	// it is still not a bindable parameter, hence the explicit membership check.
	var n int
	if err := s.db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, canonical)).Scan(&n); err != nil {
		return 0, true, err
	}
	return n, true, nil
}

// CountBareStorageKeys returns how many `resources` rows of a canonical type
// carry a bare id instead of the `id\0<parent>` storage key. For a
// parent-keyed resource (journal_entries, bank_accounts, …) a bare key is a
// pre-fix row: two companies' entries with the same API id collided on the
// primary key and one silently overwrote the other. Returns ok=false when the
// resource is not parent-keyed, where a bare key is correct.
func (s *Store) CountBareStorageKeys(resourceType string) (int, bool, error) {
	canonical, err := CanonicalResource(resourceType)
	if err != nil {
		return 0, false, err
	}
	if resourceParentKeyColumns[canonical] == "" {
		return 0, false, nil
	}
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM resources WHERE resource_type = ? AND instr(id, char(0)) = 0`,
		canonical,
	).Scan(&n); err != nil {
		return 0, true, err
	}
	return n, true, nil
}

// SyncStateCounts returns the persisted total_count per resource_type.
func (s *Store) SyncStateCounts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT resource_type, COALESCE(total_count, 0) FROM sync_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var rt string
		var n int
		if err := rows.Scan(&rt, &n); err != nil {
			return nil, err
		}
		out[rt] = n
	}
	return out, rows.Err()
}

// ResourceTypesInMirror returns every distinct resource_type present in
// `resources`, so a report can name a stale kebab-spelled population that no
// sync writes any more.
func (s *Store) ResourceTypesInMirror() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT resource_type FROM resources ORDER BY resource_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var rt string
		if err := rows.Scan(&rt); err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, rows.Err()
}

// LooksKebabSpelled reports whether a stored resource_type is a kebab spelling
// of a resource the store knows under a snake name — i.e. an orphaned
// population from before canonicalisation.
func LooksKebabSpelled(storedResourceType string) bool {
	if !strings.Contains(storedResourceType, "-") {
		return false
	}
	return IsKnownResource(strings.ReplaceAll(storedResourceType, "-", "_"))
}

// ClearCompanyResources deletes one company's rows of one canonical resource
// type from `resources`, keeping resources_fts in step, and returns how many
// rows went. It is what makes `sync --full --company S --resources R` actually
// full: the printed --full only resets the cursor, so a run that changes the
// storage key (as the canonicalisation fix does for journal_entries and
// bank_accounts) leaves the pre-fix bare-key rows behind and the company's data
// is then visible TWICE to loadCompanyResources — double-counted by every
// detector. Deleting the scoped rows first is the "repair by resync" the owner
// chose, without a migration.
//
// Scoped deliberately narrow: an empty slug is an error, so this can never
// become an unscoped wipe of a resource type.
func (s *Store) ClearCompanyResources(resourceType, slug string) (int, error) {
	canonical, err := CanonicalResource(resourceType)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(slug) == "" {
		return 0, fmt.Errorf("ClearCompanyResources(%q): company slug is required", canonical)
	}

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
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, id := range ids {
		if _, err := tx.Exec(
			`DELETE FROM resources WHERE resource_type = ? AND id = ?`, canonical, id,
		); err != nil {
			return 0, err
		}
		// Same explicit-rowid pattern upsertGenericResourceTx uses: modernc's
		// FTS5 will not honour DELETE WHERE column = ? on a virtual table.
		if _, err := tx.Exec(
			`DELETE FROM resources_fts WHERE rowid = ?`, ftsRowID(canonical, id),
		); err != nil {
			return 0, err
		}
	}

	// The typed table goes with it (issue #29). It is keyed by the same
	// storage id, but a row written under an earlier id rule has no generic
	// counterpart, so the delete addresses the company by the typed table's own
	// parent column rather than by the ids just removed. Without this a scoped
	// --full left the typed table holding each document twice and `doctor`
	// reporting a disagreement the repair it recommends could not clear.
	if HasTypedTable(canonical) && resourceParentKeyColumns[canonical] == "parent_id" {
		if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM "%s" WHERE parent_id = ?`, canonical), slug); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// CompanyRowCounts is one company's share of a parent-keyed resource: the rows
// in `resources`, how many of them carry a bare storage key, and the rows in
// the typed table. Company is "" for rows that name no company at all.
type CompanyRowCounts struct {
	Company   string
	Resources int
	BareKeys  int
	Typed     int
	HasTyped  bool
}

// CountByCompany breaks a parent-keyed resource down per company, so `doctor`
// can say whose rows disagree (issue #29). ok=false when the resource carries
// no company column to break down by (companies, and the child tables keyed on
// their parent document), where only the mirror-wide counts exist.
func (s *Store) CountByCompany(resourceType string) (counts []CompanyRowCounts, ok bool, err error) {
	canonical, err := CanonicalResource(resourceType)
	if err != nil {
		return nil, false, err
	}
	if resourceParentKeyColumns[canonical] != "parent_id" {
		return nil, false, nil
	}
	by := map[string]*CompanyRowCounts{}
	get := func(company string) *CompanyRowCounts {
		c := by[company]
		if c == nil {
			c = &CompanyRowCounts{Company: company, HasTyped: HasTypedTable(canonical)}
			by[company] = c
		}
		return c
	}

	rows, err := s.db.Query(
		`SELECT COALESCE(json_extract(data, '$.parent_id'), ''), COUNT(*), SUM(instr(id, char(0)) = 0)
		   FROM resources WHERE resource_type = ? GROUP BY 1`, canonical)
	if err != nil {
		return nil, true, err
	}
	for rows.Next() {
		var company string
		var n, bare int
		if err := rows.Scan(&company, &n, &bare); err != nil {
			rows.Close()
			return nil, true, err
		}
		c := get(company)
		c.Resources, c.BareKeys = n, bare
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, true, err
	}
	rows.Close()

	if HasTypedTable(canonical) {
		typed, err := s.db.Query(fmt.Sprintf(`SELECT COALESCE(parent_id, ''), COUNT(*) FROM "%s" GROUP BY 1`, canonical))
		if err != nil {
			return nil, true, err
		}
		for typed.Next() {
			var company string
			var n int
			if err := typed.Scan(&company, &n); err != nil {
				typed.Close()
				return nil, true, err
			}
			get(company).Typed = n
		}
		if err := typed.Err(); err != nil {
			typed.Close()
			return nil, true, err
		}
		typed.Close()
	}

	for _, c := range by {
		counts = append(counts, *c)
	}
	sort.Slice(counts, func(i, j int) bool { return counts[i].Company < counts[j].Company })
	return counts, true, nil
}

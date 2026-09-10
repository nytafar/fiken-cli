// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The CLI-side half of the mirror fixes (PLAN 2.2, issue #6). Everything the
// generated files need is a one-line call into this file, so the generated diff
// stays at the size of a call site.
//
//   - mirrorCompanySlugFromPath / mirrorWithParentID close the "two disjoint row
//     sets" hole: rows written by the read/live path (writeThroughCache) carried no
//     parent_id, so loadCompanyResources — which filters on $.parent_id — could not
//     see a single one of them.
//   - syncStateTotalCount replaces the running counter sync used to persist with
//     the real row count, so sync_state.total_count stops drifting from reality.
//   - syncCompanyScope keeps a sync run on one company. The printed sync walks every
//     row of the `companies` table, so an unscoped run reaches every book in the
//     mirror; only agensia is a test company.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"fiken-cli/internal/store"
)

// mirrorCompanySlugFromPath pulls the company slug out of a Fiken API path.
// Fiken scopes every per-company resource as /companies/{slug}/<resource...>,
// so the slug is the second segment — and only when a third segment follows:
// "/companies/agensia" is the company record itself, which has no parent.
func mirrorCompanySlugFromPath(path string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(path), "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 3 || parts[0] != "companies" {
		return ""
	}
	slug := parts[1]
	if slug == "" || strings.HasPrefix(slug, "{") {
		return ""
	}
	return slug
}

// mirrorWithParentID stamps parent_id = slug onto every item that does not
// already carry one, matching what dependent-resource sync injects. Without it
// a write-through row is invisible to loadCompanyResources and the mirror holds
// two populations of the same resource that never merge.
//
// An item that is not a JSON object, or that already has a parent_id, is passed
// through untouched; so is every item when slug is empty.
func mirrorWithParentID(items []json.RawMessage, slug string) []json.RawMessage {
	if slug == "" || len(items) == 0 {
		return items
	}
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		out = append(out, mirrorItemWithParentID(item, slug))
	}
	return out
}

func mirrorItemWithParentID(item json.RawMessage, slug string) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(item, &obj); err != nil || obj == nil {
		return item
	}
	if raw, ok := obj["parent_id"]; ok && strings.TrimSpace(string(raw)) != "null" {
		return item
	}
	encoded, err := json.Marshal(slug)
	if err != nil {
		return item
	}
	obj["parent_id"] = encoded
	merged, err := json.Marshal(obj)
	if err != nil {
		return item
	}
	return merged
}

// syncStateTotalCount resolves what sync should persist as
// sync_state.total_count on finalisation: the actual number of `resources` rows
// of that type, not the run's own running counter. The counter drifted in both
// directions (issue #6 §3) because it counted items consumed by this run, not
// rows the mirror holds — a resumed run, a deduplicated page or a row an earlier
// run already had all break the identity. Falls back to the running counter when
// the query fails, so a count problem can never fail a sync.
func syncStateTotalCount(db *store.Store, resource string, running int) int {
	if db == nil {
		return running
	}
	n, err := db.CountResources(resource)
	if err != nil {
		return running
	}
	return n
}

// syncCompanyScope is the --company value of the current sync run. Empty means
// "every company in the mirror", the printed behaviour.
var syncCompanyScope string

// scopeParentRowsToCompany filters the parent rows a dependent sync iterates
// down to a single company. The printed sync has no company flag at all: it
// walks every row of the `companies` table, so a run touches every book the
// mirror knows. Only `agensia` is a Fiken testCompany; the other books are live
// and must not be fetched from during development (see the write guard, PLAN
// 2.4.1). Non-company parent tables are returned unchanged.
func scopeParentRowsToCompany(parentTable string, rows []map[string]string) []map[string]string {
	if syncCompanyScope == "" || parentTable != "companies" {
		return rows
	}
	out := make([]map[string]string, 0, 1)
	for _, row := range rows {
		for _, v := range row {
			if v == syncCompanyScope {
				out = append(out, row)
				break
			}
		}
	}
	return out
}

// clearFullSyncCompanyRows deletes the scoped company's existing rows for each
// resource named on a --full run, so the refetch replaces them instead of
// sitting alongside them. Silent no-op without --company or without an explicit
// --resources list: an unscoped --full must not wipe the mirror.
func clearFullSyncCompanyRows(db *store.Store, resources []string) {
	if db == nil || syncCompanyScope == "" || len(resources) == 0 {
		return
	}
	for _, resource := range resources {
		removed, err := db.ClearCompanyResources(resource, syncCompanyScope)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: --full could not clear %s rows for %s: %v\n", resource, syncCompanyScope, err)
			continue
		}
		if removed > 0 {
			fmt.Fprintf(os.Stderr, "  --full: cleared %d existing %s rows for %s before refetching\n", removed, resource, syncCompanyScope)
		}
	}
}

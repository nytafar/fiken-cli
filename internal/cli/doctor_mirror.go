// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The mirror invariant `doctor` checks (PLAN 2.2 item 6, issue #6).
//
// Three independently derived views of the same resource must agree:
//
//	sync_state.total_count   what the last sync says it stored
//	COUNT(*) resources       the table every detector actually reads
//	COUNT(*) <typed table>   the domain table the generated dispatch fills
//
// They disagreed silently for two years: the typed tables for bank_accounts and
// journal_entries were never written at all because the dispatch arm was spelled
// with a hyphen, and total_count was a per-run counter nobody reconciled. This is
// the check that would have caught #6 on day one, and it is the only reason to
// keep the typed tables: a cheap, independently derived cross-check.
//
// A fourth check has no counterpart: rows whose storage key is bare where it
// should be `id\0<companySlug>`. Those are pre-fix rows, and for a multi-company
// mirror they are silent data loss — two companies' journal entries with the same
// journalEntryId collided on the primary key and one overwrote the other. The
// repair is `sync --full --company <slug> --resources <name>`, which clears that
// company's rows for that resource before refetching them under the correct key.
package cli

import (
	"fmt"
	"io"
	"sort"

	"fiken-cli/internal/store"
)

// mirrorFinding is one disagreement, in the JSON shape the doctor report uses.
type mirrorFinding struct {
	Resource string `json:"resource"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
}

func mirrorResyncHint(resource string) string {
	return fmt.Sprintf("fiken-cli sync --full --company <slug> --resources %s", resource)
}

// mirrorInvariantFindings cross-checks the mirror's three row counts per
// resource and reports every disagreement, plus any bare storage keys and any
// orphaned kebab-spelled resource_type population left over from before
// canonicalisation. Returns nil when the mirror is consistent.
func mirrorInvariantFindings(s *store.Store) []mirrorFinding {
	if s == nil {
		return nil
	}
	var out []mirrorFinding

	stateCounts, err := s.SyncStateCounts()
	if err != nil {
		return []mirrorFinding{{
			Kind:    "mirror_unreadable",
			Message: fmt.Sprintf("cannot read sync_state: %v", err),
		}}
	}

	names := make([]string, 0, len(stateCounts))
	for name := range stateCounts {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		canonical, cerr := store.CanonicalResource(name)
		if cerr != nil {
			out = append(out, mirrorFinding{
				Resource: name,
				Kind:     "unknown_resource_name",
				Message:  fmt.Sprintf("sync_state holds resource_type %q, which the store does not recognise: %v", name, cerr),
				Fix:      "remove the row or add the resource to internal/store/resource_name.go",
			})
			continue
		}

		stateCount := stateCounts[name]
		genericCount, gerr := s.CountResources(canonical)
		if gerr != nil {
			out = append(out, mirrorFinding{
				Resource: canonical,
				Kind:     "mirror_unreadable",
				Message:  fmt.Sprintf("cannot count resources rows: %v", gerr),
			})
			continue
		}

		typedCount, hasTyped, terr := s.CountTypedTable(canonical)
		if terr != nil {
			out = append(out, mirrorFinding{
				Resource: canonical,
				Kind:     "mirror_unreadable",
				Message:  fmt.Sprintf("cannot count typed table: %v", terr),
			})
			hasTyped = false
		}

		typedRepr := "n/a"
		if hasTyped {
			typedRepr = fmt.Sprintf("%d", typedCount)
		}
		disagrees := stateCount != genericCount || (hasTyped && typedCount != genericCount)
		if disagrees {
			out = append(out, mirrorFinding{
				Resource: canonical,
				Kind:     "row_count_disagreement",
				Message: fmt.Sprintf(
					"sync_state.total_count=%d, resources=%d, %s table=%s",
					stateCount, genericCount, canonical, typedRepr),
				Fix: mirrorResyncHint(canonical),
			})
		}

		bare, parentKeyed, berr := s.CountBareStorageKeys(canonical)
		if berr == nil && parentKeyed && bare > 0 {
			out = append(out, mirrorFinding{
				Resource: canonical,
				Kind:     "bare_storage_key",
				Message: fmt.Sprintf(
					"%d of %d resources rows carry a bare id instead of id\\0<companySlug>; "+
						"in a multi-company mirror those rows collided on the primary key and one company's data overwrote the other's. "+
						"A scoped --full run clears that company's rows before refetching, so repair one company at a time",
					bare, genericCount),
				Fix: mirrorResyncHint(canonical),
			})
		}
	}

	// Orphaned kebab populations: rows a pre-canonicalisation read/live path wrote
	// under the CLI spelling. No sync writes them any more and no detector reads
	// them, so they are stale by construction.
	if stored, rerr := s.ResourceTypesInMirror(); rerr == nil {
		for _, rt := range stored {
			if !store.LooksKebabSpelled(rt) {
				continue
			}
			canonical, cerr := store.CanonicalResource(rt)
			if cerr != nil {
				continue
			}
			n, cntErr := s.CountResources(canonical)
			if cntErr != nil {
				continue
			}
			var orphaned int
			_ = s.DB().QueryRow(
				`SELECT COUNT(*) FROM resources WHERE resource_type = ?`, rt).Scan(&orphaned)
			if orphaned == 0 {
				continue
			}
			out = append(out, mirrorFinding{
				Resource: canonical,
				Kind:     "orphaned_kebab_rows",
				Message: fmt.Sprintf(
					"%d rows stored under the pre-canonicalisation resource_type %q; the canonical %q holds %d",
					orphaned, rt, canonical, n),
				Fix: mirrorResyncHint(canonical),
			})
		}
	}

	return out
}

// mirrorFindingsJSON renders the findings for the machine-readable doctor report.
func mirrorFindingsJSON(findings []mirrorFinding) []map[string]any {
	if len(findings) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(findings))
	for _, f := range findings {
		entry := map[string]any{"kind": f.Kind, "message": f.Message}
		if f.Resource != "" {
			entry["resource"] = f.Resource
		}
		if f.Fix != "" {
			entry["fix"] = f.Fix
		}
		out = append(out, entry)
	}
	return out
}

// renderMirrorInvariants prints the mirror section of the human-facing doctor
// output. Silent when the mirror agrees with itself.
func renderMirrorInvariants(w io.Writer, rep map[string]any) {
	raw, ok := rep["mirror_findings"]
	if !ok {
		return
	}
	findings, ok := raw.([]map[string]any)
	if !ok || len(findings) == 0 {
		return
	}
	fmt.Fprintf(w, "    %s mirror: %d disagreement(s)\n", yellow("WARN"), len(findings))
	for _, f := range findings {
		resource, _ := f["resource"].(string)
		if resource == "" {
			resource = "-"
		}
		fmt.Fprintf(w, "      - %s [%v]: %v\n", resource, f["kind"], f["message"])
		if fix, ok := f["fix"]; ok {
			fmt.Fprintf(w, "        fix: %v\n", fix)
		}
	}
}

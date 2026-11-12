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
//
// Every finding that can be attributed names its company (issue #29). The
// counts used to be mirror-wide only, so after `sync --full --company a` on a
// three-company mirror the same eleven findings were still listed with a fix
// line reading `--company <slug>`: the rows of company a were clean and the
// findings belonged to the other two, but nothing said so and it read as a
// failed repair. `doctor --company <slug>` keeps only that company's findings.
package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"fiken-cli/internal/store"
)

// mirrorFinding is one disagreement, in the JSON shape the doctor report uses.
// Company is the slug the rows belong to; it is empty for a finding that
// cannot be attributed (a mirror-wide count, or rows that name no company).
type mirrorFinding struct {
	Resource string `json:"resource"`
	Company  string `json:"company"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
}

func mirrorResyncHint(resource, company string) string {
	if company == "" {
		company = "<slug>"
	}
	return fmt.Sprintf("fiken-cli sync --full --company %s --resources %s", company, resource)
}

// mirrorCompanyFindings is the per-company half of the invariant for one
// parent-keyed resource: the typed table against `resources`, and the bare
// storage keys, each attributed to the company whose rows they are. Rows that
// name no company are reported once, as unowned: no command reads them (every
// detector filters on parent_id), so they distort no report, and no company's
// resync can remove them.
func mirrorCompanyFindings(resource string, counts []store.CompanyRowCounts) []mirrorFinding {
	var out []mirrorFinding
	for _, c := range counts {
		if c.Company == "" {
			if c.Resources == 0 && c.Typed == 0 {
				continue
			}
			out = append(out, mirrorFinding{
				Resource: resource,
				Kind:     "unowned_rows",
				Message: fmt.Sprintf(
					"%d resources rows and %d %s table rows name no company. They were written before rows were stamped with their company; "+
						"no command reads them, so they affect no report, and no company's resync removes them",
					c.Resources, c.Typed, resource),
			})
			continue
		}
		if c.HasTyped && c.Typed != c.Resources {
			out = append(out, mirrorFinding{
				Resource: resource,
				Company:  c.Company,
				Kind:     "row_count_disagreement",
				Message:  fmt.Sprintf("%s: resources=%d, %s table=%d", c.Company, c.Resources, resource, c.Typed),
				Fix:      mirrorResyncHint(resource, c.Company),
			})
		}
		if c.BareKeys > 0 {
			out = append(out, mirrorFinding{
				Resource: resource,
				Company:  c.Company,
				Kind:     "bare_storage_key",
				Message: fmt.Sprintf(
					"%s: %d of %d resources rows carry a bare id instead of id\\0<companySlug>; "+
						"in a multi-company mirror those rows collided on the primary key and one company's data overwrote the other's",
					c.Company, c.BareKeys, c.Resources),
				Fix: mirrorResyncHint(resource, c.Company),
			})
		}
	}
	return out
}

// mirrorInvariantFindings cross-checks the mirror's row counts per resource
// and reports every disagreement: per company where the resource is keyed by
// company, mirror-wide otherwise, plus any orphaned kebab-spelled
// resource_type population left over from before canonicalisation. Returns nil
// when the mirror is consistent.
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

		// sync_state.total_count is one number for the whole mirror, so this
		// half of the invariant has no company to name.
		if stateCount != genericCount {
			out = append(out, mirrorFinding{
				Resource: canonical,
				Kind:     "row_count_disagreement",
				Message: fmt.Sprintf(
					"sync_state.total_count=%d, resources=%d (all companies); a plain `fiken-cli sync` recounts it",
					stateCount, genericCount),
				Fix: "fiken-cli sync --resources " + canonical,
			})
		}

		counts, perCompany, berr := s.CountByCompany(canonical)
		if berr != nil {
			out = append(out, mirrorFinding{
				Resource: canonical,
				Kind:     "mirror_unreadable",
				Message:  fmt.Sprintf("cannot count rows per company: %v", berr),
			})
			continue
		}
		if perCompany {
			out = append(out, mirrorCompanyFindings(canonical, counts)...)
			continue
		}

		// No company column: only the mirror-wide typed count exists.
		typedCount, hasTyped, terr := s.CountTypedTable(canonical)
		if terr != nil {
			out = append(out, mirrorFinding{
				Resource: canonical,
				Kind:     "mirror_unreadable",
				Message:  fmt.Sprintf("cannot count typed table: %v", terr),
			})
			continue
		}
		if hasTyped && typedCount != genericCount {
			out = append(out, mirrorFinding{
				Resource: canonical,
				Kind:     "row_count_disagreement",
				Message:  fmt.Sprintf("resources=%d, %s table=%d (all companies)", genericCount, canonical, typedCount),
				Fix:      mirrorResyncHint(canonical, ""),
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
					"%d rows stored under the pre-canonicalisation resource_type %q; the canonical %q holds %d. No command reads them",
					orphaned, rt, canonical, n),
			})
		}
	}

	return out
}

// scopeMirrorFindings narrows the doctor report's mirror findings to one
// company (`doctor --company <slug>`): only findings attributed to that slug
// stay, so a company whose rows are clean reports none, whatever state the
// other companies in the mirror are in. An empty slug leaves the report as it
// is. The count of what was left out is recorded so the narrowing is visible.
func scopeMirrorFindings(report map[string]any, company string) {
	company = strings.TrimSpace(company)
	if company == "" {
		return
	}
	cache, ok := report["cache"].(map[string]any)
	if !ok {
		return
	}
	cache["mirror_company"] = company
	findings, _ := cache["mirror_findings"].([]map[string]any)
	kept := []map[string]any{}
	for _, f := range findings {
		if c, _ := f["company"].(string); c == company {
			kept = append(kept, f)
		}
	}
	cache["mirror_findings_other_companies"] = len(findings) - len(kept)
	cache["mirror_findings"] = kept
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
		if f.Company != "" {
			entry["company"] = f.Company
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
	if scoped, _ := rep["mirror_company"].(string); scoped != "" && ok && len(findings) == 0 {
		fmt.Fprintf(w, "    %s mirror: no disagreements for %s", green("OK"), scoped)
		if other, _ := rep["mirror_findings_other_companies"].(int); other > 0 {
			fmt.Fprintf(w, " (%d for other companies or the whole mirror, not shown)", other)
		}
		fmt.Fprintln(w)
		return
	}
	if !ok || len(findings) == 0 {
		return
	}
	fmt.Fprintf(w, "    %s mirror: %d disagreement(s)\n", yellow("WARN"), len(findings))
	for _, f := range findings {
		resource, _ := f["resource"].(string)
		if resource == "" {
			resource = "-"
		}
		if company, _ := f["company"].(string); company != "" {
			resource += " (" + company + ")"
		}
		fmt.Fprintf(w, "      - %s [%v]: %v\n", resource, f["kind"], f["message"])
		if fix, ok := f["fix"]; ok {
			fmt.Fprintf(w, "        fix: %v\n", fix)
		}
	}
}

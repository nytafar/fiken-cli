// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). Lists purchases and journal entries in a period
// whose `attachments` array is absent or empty — the documents missing a
// bilag. Reads only the local mirror.
package cli

// pp:data-source local

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"
)

// hasAttachments reports whether a decoded document carries a non-empty
// attachments array.
func hasAttachments(m map[string]json.RawMessage) bool {
	return len(jsonObjects(m, "attachments")) > 0
}

// journalEntryImpact approximates the size of a journal entry: the sum of its
// positive line amounts, i.e. one side of a balanced entry. Journal lines carry
// a signed amount with no debit/credit direction, so this is the only honest
// magnitude available from the mirror.
func journalEntryImpact(e map[string]json.RawMessage) int64 {
	var sum int64
	for _, ln := range jsonObjects(e, "lines") {
		if amt, _ := jsonInt(ln, "amount"); amt > 0 {
			sum += amt
		}
	}
	return sum
}

func newNovelMissingBilagCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var dbPath string
	var opts reportOpts

	cmd := &cobra.Command{
		Use:   "missing-bilag",
		Short: "List purchases and journal entries with no attached documentation",
		Long: "Scans purchases and journal entries in a period and reports any whose attachments array\n" +
			"is absent or empty — the documents missing a bilag (receipt/voucher). Each finding carries\n" +
			"the document's amount as its impact, so the biggest undocumented postings sort first.\n" +
			"Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli missing-bilag --company fiken-demo
  fiken-cli missing-bilag --company fiken-demo --period 2026-05 --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			win, err := resolvePeriod(opts.period)
			if err != nil {
				return err
			}
			if dryRunOK(flags) {
				return nil
			}
			db, err := openMirror(cmd.Context(), dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			slug, err := resolveCompanySlug(db, flagCompany)
			if err != nil {
				return err
			}

			var findings []Finding
			undated := 0
			purchases, err := loadCompanyResources(cmd.Context(), db, "purchases", slug)
			if err != nil {
				return err
			}
			for _, p := range purchases {
				date := jsonStr(p, "date")
				if date == "" {
					undated++
					continue
				}
				if !win.Contains(date) || hasAttachments(p) {
					continue
				}
				id, _ := jsonInt(p, "purchaseId")
				var contactID int64
				var contactName string
				if sup := jsonObject(p, "supplier"); sup != nil {
					contactID, _ = jsonInt(sup, "contactId")
					contactName = jsonStr(sup, "name")
				}
				f := Finding{
					Kind:        "missing_attachment",
					Severity:    SeverityInfo,
					DocType:     "purchase",
					DocID:       id,
					Date:        date,
					ContactID:   contactID,
					ContactName: contactName,
					Description: jsonStr(p, "kind"),
					ImpactOre:   purchaseGross(p),
				}
				if ident := jsonStr(p, "identifier"); ident != "" {
					f.Detail = map[string]any{"identifier": ident}
				}
				findings = append(findings, f)
			}
			entries, err := loadCompanyResources(cmd.Context(), db, "journal_entries", slug)
			if err != nil {
				return err
			}
			for _, e := range entries {
				date := jsonStr(e, "date")
				if date == "" {
					undated++
					continue
				}
				if !win.Contains(date) || hasAttachments(e) {
					continue
				}
				id, _ := jsonInt(e, "journalEntryId")
				findings = append(findings, Finding{
					Kind:        "missing_attachment",
					Severity:    SeverityInfo,
					DocType:     "journal_entry",
					DocID:       id,
					Date:        date,
					Description: jsonStr(e, "description"),
					ImpactOre:   journalEntryImpact(e),
				})
			}

			report := Report{
				Company:  slug,
				Detector: "missing_bilag",
				Window:   win,
				Undated:  undated,
			}
			keyFn := func(f Finding) map[string]string {
				return map[string]string{"doc_type": f.DocType}
			}
			return finishReport(cmd, flags, report, findings, keyFn, opts)
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	addReportFlags(cmd, &opts)
	return cmd
}

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
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type missingBilagItem struct {
	DocType     string `json:"doc_type"` // purchase | journal_entry
	DocID       int64  `json:"doc_id"`
	Date        string `json:"date"`
	Description string `json:"description,omitempty"`
}

type missingBilagReport struct {
	Company      string             `json:"company"`
	Period       string             `json:"period,omitempty"`
	TotalMissing int                `json:"total_missing"`
	Missing      []missingBilagItem `json:"missing"`
}

// hasAttachments reports whether a decoded document carries a non-empty
// attachments array.
func hasAttachments(m map[string]json.RawMessage) bool {
	return len(jsonObjects(m, "attachments")) > 0
}

func newNovelMissingBilagCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var flagPeriod string
	var dbPath string

	cmd := &cobra.Command{
		Use:   "missing-bilag",
		Short: "List purchases and journal entries with no attached documentation",
		Long: "Scans purchases and journal entries in a period and reports any whose attachments array\n" +
			"is absent or empty — the documents missing a bilag (receipt/voucher). Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli missing-bilag --company fiken-demo
  fiken-cli missing-bilag --company fiken-demo --period 2026-05 --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			from, to, err := parsePeriod(flagPeriod)
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

			inPeriod := func(date string) bool {
				if date == "" {
					return false
				}
				if from != "" && date < from {
					return false
				}
				if to != "" && date > to {
					return false
				}
				return true
			}

			var missing []missingBilagItem
			purchases, err := loadCompanyResources(cmd.Context(), db, "purchases", slug)
			if err != nil {
				return err
			}
			for _, p := range purchases {
				date := jsonStr(p, "date")
				if !inPeriod(date) || hasAttachments(p) {
					continue
				}
				id, _ := jsonInt(p, "purchaseId")
				missing = append(missing, missingBilagItem{
					DocType: "purchase", DocID: id, Date: date,
					Description: jsonStr(p, "kind"),
				})
			}
			entries, err := loadCompanyResources(cmd.Context(), db, "journal_entries", slug)
			if err != nil {
				return err
			}
			for _, e := range entries {
				date := jsonStr(e, "date")
				if !inPeriod(date) || hasAttachments(e) {
					continue
				}
				id, _ := jsonInt(e, "journalEntryId")
				missing = append(missing, missingBilagItem{
					DocType: "journal_entry", DocID: id, Date: date,
					Description: jsonStr(e, "description"),
				})
			}

			sort.SliceStable(missing, func(i, j int) bool {
				if missing[i].Date != missing[j].Date {
					return missing[i].Date < missing[j].Date
				}
				return missing[i].DocType < missing[j].DocType
			})

			report := missingBilagReport{
				Company:      slug,
				Period:       flagPeriod,
				TotalMissing: len(missing),
				Missing:      missing,
			}
			return emitFiken(cmd, flags, report, func() {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "Documents missing a bilag for %s", report.Company)
				if report.Period != "" {
					fmt.Fprintf(w, " (%s)", report.Period)
				}
				fmt.Fprintf(w, "\n\n")
				if len(report.Missing) == 0 {
					fmt.Fprintln(w, "Every purchase and journal entry has an attachment.")
					return
				}
				for _, m := range report.Missing {
					fmt.Fprintf(w, "%-14s #%-8d %s  %s\n", m.DocType, m.DocID, m.Date, m.Description)
				}
				fmt.Fprintf(w, "\nTotal missing bilag: %d\n", report.TotalMissing)
			})
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&flagPeriod, "period", "", "Period to scan: YYYY, YYYY-MM, YYYY-Qn, or from:to (default: all)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	return cmd
}

// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). The #1 feature (build-spec brief workflow #1):
// surface postings on bank accounts that aren't yet matched to the bank
// statement. Each unreconciled posting on a bank account code — dated AFTER
// that account's reconciledDate — is one Finding; the per-account
// reconciliation anchors (reconciledBalance / reconciledDate) travel in the
// report's Params. Unlike the period detectors this one is anchored to the
// ledger's own reconciled date, not a calendar period, so it takes --as-of
// rather than --period. Reads only the local mirror.
package cli

// pp:data-source local

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newNovelBankUnverifiedCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var flagAsOf string
	var dbPath string
	var opts reportOpts

	cmd := &cobra.Command{
		Use:   "bank-unverified",
		Short: "List bank-account postings not yet matched to the bank statement",
		Long: "Reports every journal posting on a bank account dated after that account's ledger\n" +
			"reconciliation anchor (reconciledBalance/reconciledDate) — the postings blocking a clean\n" +
			"bankavstemming. The anchors themselves are in the report's params. The window here is the\n" +
			"account's own reconciled date, so this command takes --as-of, not --period. Reads the local\n" +
			"mirror; run 'sync' first.",
		Example: strings.Trim(`
  fiken-cli bank-unverified --company fiken-demo
  fiken-cli bank-unverified --company fiken-demo --as-of 2026-05-31 --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagAsOf != "" && !validDate(flagAsOf) {
				return fmt.Errorf("--as-of must be YYYY-MM-DD, got %q", flagAsOf)
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

			banks, err := loadCompanyResources(cmd.Context(), db, "bank_accounts", slug)
			if err != nil {
				return err
			}
			type bankMeta struct {
				name, reconciledDate string
				reconciledBal        int64
			}
			byCode := map[string]*bankMeta{}
			order := []string{}
			for _, b := range banks {
				code := jsonStr(b, "accountCode")
				if code == "" {
					continue
				}
				bal, _ := jsonInt(b, "reconciledBalance")
				byCode[code] = &bankMeta{
					name:           jsonStr(b, "name"),
					reconciledDate: jsonStr(b, "reconciledDate"),
					reconciledBal:  bal,
				}
				order = append(order, code)
			}

			var findings []Finding
			undated := 0
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
				if flagAsOf != "" && date > flagAsOf {
					continue
				}
				jeID, _ := jsonInt(e, "journalEntryId")
				txID, _ := jsonInt(e, "transactionId")
				desc := jsonStr(e, "description")
				for _, ln := range jsonObjects(e, "lines") {
					code := jsonStr(ln, "account")
					meta, isBank := byCode[code]
					if !isBank {
						continue
					}
					// "Unverified" = posted after the account's reconciled date.
					// Empty reconciledDate => nothing reconciled yet => all count.
					if meta.reconciledDate != "" && date <= meta.reconciledDate {
						continue
					}
					amt, _ := jsonInt(ln, "amount")
					detail := map[string]any{}
					if txID != 0 {
						detail["transaction_id"] = txID
					}
					if meta.name != "" {
						detail["account_name"] = meta.name
					}
					if meta.reconciledDate != "" {
						detail["reconciled_date"] = meta.reconciledDate
					}
					findings = append(findings, Finding{
						Kind:        "unverified_posting",
						Severity:    SeverityInfo,
						DocType:     "journal_entry",
						DocID:       jeID,
						Date:        date,
						Description: desc,
						Account:     code,
						ImpactOre:   amt,
						Detail:      detail,
					})
				}
			}

			// The window is the ledger's reconciliation anchor, not a period:
			// --as-of only caps it from above.
			win := Window{Source: "all"}
			if flagAsOf != "" {
				win = Window{To: flagAsOf, Source: "flag"}
			}
			accounts := make([]map[string]any, 0, len(order))
			for _, code := range order {
				meta := byCode[code]
				accounts = append(accounts, map[string]any{
					"account":                code,
					"name":                   meta.name,
					"reconciled_date":        meta.reconciledDate,
					"reconciled_balance_ore": meta.reconciledBal,
				})
			}
			report := Report{
				Company:  slug,
				Detector: "bank_unverified",
				Window:   win,
				Undated:  undated,
				Params:   map[string]any{"accounts": accounts},
			}
			keyFn := func(f Finding) map[string]string {
				return map[string]string{"account": f.Account}
			}
			return finishReport(cmd, flags, report, findings, keyFn, opts)
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&flagAsOf, "as-of", "", "Only consider postings up to this date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	// Deliberately not addReportFlags: --period would imply a calendar window
	// this detector does not have. The two size flags are registered with the
	// same names, defaults and help text so they mean one thing everywhere.
	cmd.Flags().Int64Var(&opts.minImpactOre, "min-impact-ore", 0, "Drop findings whose absolute impact is below this many øre")
	cmd.Flags().IntVar(&opts.limit, "limit", 200, "Max findings to emit; the summary and total still cover all of them")
	return cmd
}

func orNone(s string) string {
	if s == "" {
		return "(never)"
	}
	return s
}

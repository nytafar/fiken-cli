// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). The #1 feature (build-spec brief workflow #1):
// surface postings on bank accounts that aren't yet matched to the bank
// statement. For each bank account it reports Fiken's own reconciliation
// anchor (reconciledBalance / reconciledDate) plus every journal posting on
// that account code dated AFTER reconciledDate — the set blocking a clean
// bankavstemming. Reads only the local mirror.
package cli

// pp:data-source local

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type unverifiedPosting struct {
	Date          string `json:"date"`
	Description   string `json:"description"`
	AmountOre     int64  `json:"amount_ore"`
	Amount        string `json:"amount"`
	JournalEntry  int64  `json:"journal_entry_id,omitempty"`
	TransactionID int64  `json:"transaction_id,omitempty"`
}

type bankUnverifiedAccount struct {
	AccountCode      string              `json:"account_code"`
	Name             string              `json:"name"`
	ReconciledDate   string              `json:"reconciled_date,omitempty"`
	ReconciledBalOre int64               `json:"reconciled_balance_ore"`
	ReconciledBal    string              `json:"reconciled_balance"`
	UnverifiedCount  int                 `json:"unverified_count"`
	UnverifiedSumOre int64               `json:"unverified_sum_ore"`
	UnverifiedSum    string              `json:"unverified_sum"`
	Postings         []unverifiedPosting `json:"postings"`
}

type bankUnverifiedReport struct {
	Company         string                  `json:"company"`
	AsOf            string                  `json:"as_of,omitempty"`
	TotalUnverified int                     `json:"total_unverified_postings"`
	Accounts        []bankUnverifiedAccount `json:"accounts"`
}

func newNovelBankUnverifiedCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var flagAsOf string
	var dbPath string

	cmd := &cobra.Command{
		Use:   "bank-unverified",
		Short: "List bank-account postings not yet matched to the bank statement",
		Long: "Reports, per bank account, the ledger-reconciliation anchor (reconciledBalance/reconciledDate)\n" +
			"and every journal posting on that account dated after it — the postings blocking a clean\n" +
			"bankavstemming. Reads the local mirror; run 'sync' first.",
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

			postingsByCode := map[string][]unverifiedPosting{}
			entries, err := loadCompanyResources(cmd.Context(), db, "journal_entries", slug)
			if err != nil {
				return err
			}
			for _, e := range entries {
				date := jsonStr(e, "date")
				if date == "" {
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
					postingsByCode[code] = append(postingsByCode[code], unverifiedPosting{
						Date:          date,
						Description:   desc,
						AmountOre:     amt,
						Amount:        kr(amt),
						JournalEntry:  jeID,
						TransactionID: txID,
					})
				}
			}

			report := bankUnverifiedReport{Company: slug, AsOf: flagAsOf}
			for _, code := range order {
				meta := byCode[code]
				ps := postingsByCode[code]
				sort.Slice(ps, func(i, j int) bool { return ps[i].Date < ps[j].Date })
				var sum int64
				for _, p := range ps {
					sum += p.AmountOre
				}
				report.Accounts = append(report.Accounts, bankUnverifiedAccount{
					AccountCode:      code,
					Name:             meta.name,
					ReconciledDate:   meta.reconciledDate,
					ReconciledBalOre: meta.reconciledBal,
					ReconciledBal:    kr(meta.reconciledBal),
					UnverifiedCount:  len(ps),
					UnverifiedSumOre: sum,
					UnverifiedSum:    kr(sum),
					Postings:         ps,
				})
				report.TotalUnverified += len(ps)
			}

			return emitFiken(cmd, flags, report, func() {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "Bank reconciliation status for %s", report.Company)
				if report.AsOf != "" {
					fmt.Fprintf(w, " (as of %s)", report.AsOf)
				}
				fmt.Fprintf(w, "\n\n")
				if len(report.Accounts) == 0 {
					fmt.Fprintln(w, "No bank accounts in the mirror — run 'fiken-cli sync' first.")
					return
				}
				for _, a := range report.Accounts {
					fmt.Fprintf(w, "%s  %s\n", a.AccountCode, a.Name)
					fmt.Fprintf(w, "  reconciled: %s kr through %s\n", a.ReconciledBal, orNone(a.ReconciledDate))
					fmt.Fprintf(w, "  unverified: %d posting(s), %s kr\n", a.UnverifiedCount, a.UnverifiedSum)
					for _, p := range a.Postings {
						fmt.Fprintf(w, "    %s  %12s  %s\n", p.Date, p.Amount, p.Description)
					}
					fmt.Fprintln(w)
				}
				fmt.Fprintf(w, "Total unverified postings: %d\n", report.TotalUnverified)
			})
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&flagAsOf, "as-of", "", "Only consider postings up to this date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	return cmd
}

func orNone(s string) string {
	if s == "" {
		return "(never)"
	}
	return s
}

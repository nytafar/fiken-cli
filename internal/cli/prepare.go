// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator stub. A read-compound that
// assembles everything needed to draft a posting from an inbox document
// (build-spec §8.2): the bilag metadata + documentUrl (the agent reads the
// bytes), candidate expense accounts, valid purchase MVA types, and recent
// suppliers with their usual account/VAT. Output is a proposal TEMPLATE the
// agent fills in, then pipes to validate -> commit. Read-only.
package cli

// pp:data-source local

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type prepareSupplier struct {
	ContactID    int64  `json:"contact_id"`
	Name         string `json:"name"`
	ModalAccount string `json:"modal_account,omitempty"`
	ModalVatType string `json:"modal_vat_type,omitempty"`
	PurchaseN    int    `json:"purchase_count"`
}

type prepareAccount struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type prepareReport struct {
	Company          string                     `json:"company"`
	InboxDocID       string                     `json:"inbox_document_id,omitempty"`
	InboxDoc         map[string]json.RawMessage `json:"inbox_document,omitempty"`
	DocumentURL      string                     `json:"document_url,omitempty"`
	CandidateAccount []prepareAccount           `json:"candidate_expense_accounts"`
	PurchaseVatTypes []string                   `json:"purchase_vat_types"`
	RecentSuppliers  []prepareSupplier          `json:"recent_suppliers"`
	ProposalTemplate map[string]any             `json:"proposal_template"`
}

func newNovelPrepareCmd(flags *rootFlags) *cobra.Command {
	var flagCompany, dbPath string

	cmd := &cobra.Command{
		Use:   "prepare [inbox-doc-id]",
		Short: "Assemble the context to draft a posting from an inbox document",
		Long: "Gathers the inbox document (with documentUrl for the agent to read), candidate expense accounts,\n" +
			"valid purchase MVA types, and recent suppliers with their usual account/VAT — then emits a\n" +
			"proposal template to fill in and pipe to validate/commit. Read-only.",
		Example: strings.Trim(`
  fiken-cli prepare 734083065 --company fiken-demo --agent
  fiken-cli prepare --company fiken-demo --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		Args:        cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			rep := prepareReport{Company: slug, PurchaseVatTypes: purchaseVatTypeList()}

			var inboxID string
			if len(args) > 0 {
				inboxID = args[0]
			}
			inbox, err := loadCompanyResources(cmd.Context(), db, "inbox", slug)
			if err != nil {
				return err
			}
			if inboxID != "" {
				rep.InboxDocID = inboxID
				for _, d := range inbox {
					if jsonStr(d, "inboxDocumentId") == inboxID || jsonStr(d, "documentId") == inboxID {
						rep.InboxDoc = d
						rep.DocumentURL = jsonStr(d, "documentUrl")
						break
					}
				}
			}

			accounts, err := loadCompanyResources(cmd.Context(), db, "accounts", slug)
			if err != nil {
				return err
			}
			for _, a := range accounts {
				code := jsonStr(a, "code")
				if isCostAccount(code) {
					rep.CandidateAccount = append(rep.CandidateAccount, prepareAccount{Code: code, Name: jsonStr(a, "name")})
				}
			}
			sort.Slice(rep.CandidateAccount, func(i, j int) bool { return rep.CandidateAccount[i].Code < rep.CandidateAccount[j].Code })

			purchases, err := loadCompanyResources(cmd.Context(), db, "purchases", slug)
			if err != nil {
				return err
			}
			rep.RecentSuppliers = recentSuppliers(purchases)

			rep.ProposalTemplate = map[string]any{
				"company_slug":   slug,
				"kind":           "purchase",
				"source_system":  "inbox",
				"source_id":      inboxID,
				"bank_line_date": "<YYYY-MM-DD — the bank statement line date>",
				"confidence":     "<high|medium|low>",
				"rationale":      "<why these accounts/VAT>",
				"purchase": map[string]any{
					"kind":        "supplier",
					"paid":        false,
					"currency":    "NOK",
					"supplier_id": 0,
					"lines": []map[string]any{{
						"description": "<line description>",
						"net_price":   0,
						"vat":         0,
						"account":     "<expense account code>",
						"vat_type":    "HIGH",
					}},
				},
			}

			return emitFiken(cmd, flags, rep, func() {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "Posting context for %s\n", slug)
				if rep.DocumentURL != "" {
					fmt.Fprintf(w, "  inbox doc %s — documentUrl: %s\n", rep.InboxDocID, rep.DocumentURL)
				}
				fmt.Fprintf(w, "  %d candidate expense accounts, %d recent suppliers\n", len(rep.CandidateAccount), len(rep.RecentSuppliers))
				fmt.Fprintln(w, "  fill in proposal_template, then: validate | commit --dry-run")
			})
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	return cmd
}

func purchaseVatTypeList() []string {
	return []string{"NONE", "HIGH", "MEDIUM", "LOW", "RAW_FISH", "HIGH_DIRECT", "HIGH_BASIS",
		"MEDIUM_DIRECT", "MEDIUM_BASIS", "NONE_IMPORT_BASIS"}
}

func isCostAccount(code string) bool {
	if code == "" {
		return false
	}
	c := code[0]
	return c >= '4' && c <= '7'
}

func recentSuppliers(purchases []map[string]json.RawMessage) []prepareSupplier {
	type agg struct {
		name     string
		count    int
		accounts map[string]int
		vatTypes map[string]int
	}
	by := map[int64]*agg{}
	for _, p := range purchases {
		sup := jsonObject(p, "supplier")
		if sup == nil {
			continue
		}
		cid, _ := jsonInt(sup, "contactId")
		if cid == 0 {
			continue
		}
		a := by[cid]
		if a == nil {
			a = &agg{name: jsonStr(sup, "name"), accounts: map[string]int{}, vatTypes: map[string]int{}}
			by[cid] = a
		}
		a.count++
		for _, ln := range jsonObjects(p, "lines") {
			if acct := jsonStr(ln, "account"); acct != "" {
				a.accounts[acct]++
			}
			if vt := jsonStr(ln, "vatType"); vt != "" {
				a.vatTypes[vt]++
			}
		}
	}
	var out []prepareSupplier
	for cid, a := range by {
		out = append(out, prepareSupplier{
			ContactID: cid, Name: a.name, PurchaseN: a.count,
			ModalAccount: topKey(a.accounts), ModalVatType: topKey(a.vatTypes),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PurchaseN > out[j].PurchaseN })
	if len(out) > 15 {
		out = out[:15]
	}
	return out
}

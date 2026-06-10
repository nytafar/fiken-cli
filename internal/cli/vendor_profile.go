// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). Profiles how a single vendor (supplier) is usually
// posted: the modal expense account and VAT type, purchase count, total gross,
// last date, and the top-3 (account, vatType) combinations by frequency. Reads
// only the local mirror.
package cli

// pp:data-source local

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type vendorCombo struct {
	Account string `json:"account"`
	VATType string `json:"vat_type"`
	Count   int    `json:"count"`
}

type vendorProfileReport struct {
	Company       string        `json:"company"`
	ContactID     int64         `json:"contact_id"`
	ContactName   string        `json:"contact_name,omitempty"`
	PurchaseCount int           `json:"purchase_count"`
	TotalOre      int64         `json:"total_ore"`
	Total         string        `json:"total"`
	LastDate      string        `json:"last_date,omitempty"`
	ModalAccount  string        `json:"modal_account,omitempty"`
	ModalVATType  string        `json:"modal_vat_type,omitempty"`
	Combos        []vendorCombo `json:"combos"`
}

// vendorProfilePurchase is the minimal per-purchase shape the aggregator needs.
type vendorProfilePurchase struct {
	Date     string
	GrossOre int64
	Lines    []vendorProfileLine
}

type vendorProfileLine struct {
	Account string
	VATType string
}

// buildVendorProfile is the pure aggregator over a single vendor's purchases.
// It returns the count, total gross øre, last date, modal account, modal
// vatType, and the top-3 (account,vatType) combos by frequency (ties broken
// deterministically by account then vatType).
func buildVendorProfile(purchases []vendorProfilePurchase) (count int, totalOre int64, lastDate, modalAccount, modalVAT string, combos []vendorCombo) {
	acctCounts := map[string]int{}
	vatCounts := map[string]int{}
	comboCounts := map[vendorCombo]int{}
	count = len(purchases)
	for _, p := range purchases {
		totalOre += p.GrossOre
		if p.Date > lastDate {
			lastDate = p.Date
		}
		for _, ln := range p.Lines {
			if ln.Account != "" {
				acctCounts[ln.Account]++
			}
			if ln.VATType != "" {
				vatCounts[ln.VATType]++
			}
			comboCounts[vendorCombo{Account: ln.Account, VATType: ln.VATType}]++
		}
	}
	modalAccount = modalKey(acctCounts)
	modalVAT = modalKey(vatCounts)

	combos = make([]vendorCombo, 0, len(comboCounts))
	for c, n := range comboCounts {
		c.Count = n
		combos = append(combos, c)
	}
	sort.SliceStable(combos, func(i, j int) bool {
		if combos[i].Count != combos[j].Count {
			return combos[i].Count > combos[j].Count // most frequent first
		}
		if combos[i].Account != combos[j].Account {
			return combos[i].Account < combos[j].Account
		}
		return combos[i].VATType < combos[j].VATType
	})
	if len(combos) > 3 {
		combos = combos[:3]
	}
	return count, totalOre, lastDate, modalAccount, modalVAT, combos
}

// modalKey returns the most frequent key, ties broken by lexicographically
// smallest key. Empty map yields "".
func modalKey(counts map[string]int) string {
	best := ""
	bestN := 0
	for k, n := range counts {
		if n > bestN || (n == bestN && (best == "" || k < best)) {
			best, bestN = k, n
		}
	}
	return best
}

func newNovelVendorProfileCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var flagContact string
	var dbPath string

	cmd := &cobra.Command{
		Use:   "vendor-profile [contactId]",
		Short: "Show how a vendor is usually posted (modal account, VAT type, totals)",
		Long: "Aggregates one supplier's purchase history: the modal expense account and VAT type,\n" +
			"purchase count, total gross, last purchase date, and the top-3 (account, VAT type)\n" +
			"combinations by frequency. Identify the vendor by positional contactId or --contact.\n" +
			"Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli vendor-profile 123 --company fiken-demo
  fiken-cli vendor-profile --contact 123 --company fiken-demo --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve the contact id from exactly one of positional arg or --contact.
			posit := ""
			if len(args) > 0 {
				posit = strings.TrimSpace(args[0])
			}
			flagContact = strings.TrimSpace(flagContact)
			if posit != "" && flagContact != "" {
				return fmt.Errorf("pass the contact id either positionally OR via --contact, not both")
			}
			if len(args) > 1 {
				return fmt.Errorf("vendor-profile takes at most one positional contactId, got %d args", len(args))
			}
			raw := posit
			if raw == "" {
				raw = flagContact
			}
			if raw == "" {
				// Missing required contact id: always show help so the
				// invocation produces actionable output, even under --dry-run.
				return cmd.Help()
			}
			contactID, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return fmt.Errorf("contactId must be an integer, got %q", raw)
			}
			if dryRunOK(flags) {
				// Preview the read the command would perform; emitting a line
				// (rather than silently returning) keeps --dry-run informative
				// and gives the verify harness probe output to match.
				fmt.Fprintf(cmd.OutOrStdout(),
					"would profile vendor contact %d from the local mirror (purchases, modal account/VAT, top combos)\n",
					contactID)
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

			purchases, err := loadCompanyResources(cmd.Context(), db, "purchases", slug)
			if err != nil {
				return err
			}
			var mine []vendorProfilePurchase
			var contactName string
			for _, p := range purchases {
				var sid int64
				if raw, ok := p["supplier"]; ok {
					var ent map[string]json.RawMessage
					if json.Unmarshal(raw, &ent) == nil {
						sid, _ = jsonInt(ent, "contactId")
						if sid == contactID && contactName == "" {
							contactName = jsonStr(ent, "name")
						}
					}
				}
				if sid != contactID {
					continue
				}
				var gross int64
				var lines []vendorProfileLine
				for _, ln := range jsonObjects(p, "lines") {
					net, _ := jsonInt(ln, "netPrice")
					vat, _ := jsonInt(ln, "vat")
					gross += net + vat
					lines = append(lines, vendorProfileLine{
						Account: jsonStr(ln, "account"),
						VATType: jsonStr(ln, "vatType"),
					})
				}
				mine = append(mine, vendorProfilePurchase{
					Date: jsonStr(p, "date"), GrossOre: gross, Lines: lines,
				})
			}
			// Fall back to the contacts mirror for a name if no purchase named the supplier.
			if contactName == "" {
				if contacts, cerr := loadCompanyResources(cmd.Context(), db, "contacts", slug); cerr == nil {
					for _, c := range contacts {
						if id, _ := jsonInt(c, "contactId"); id == contactID {
							contactName = jsonStr(c, "name")
							break
						}
					}
				}
			}

			count, totalOre, lastDate, modalAccount, modalVAT, combos := buildVendorProfile(mine)
			report := vendorProfileReport{
				Company:       slug,
				ContactID:     contactID,
				ContactName:   contactName,
				PurchaseCount: count,
				TotalOre:      totalOre,
				Total:         kr(totalOre),
				LastDate:      lastDate,
				ModalAccount:  modalAccount,
				ModalVATType:  modalVAT,
				Combos:        combos,
			}
			return emitFiken(cmd, flags, report, func() {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "Vendor profile: %s (contact %d) in %s\n\n", orNone(report.ContactName), report.ContactID, report.Company)
				if report.PurchaseCount == 0 {
					fmt.Fprintln(w, "No purchases recorded for this vendor in the mirror.")
					return
				}
				fmt.Fprintf(w, "  purchases:    %d\n", report.PurchaseCount)
				fmt.Fprintf(w, "  total gross:  %s kr\n", report.Total)
				fmt.Fprintf(w, "  last date:    %s\n", orNone(report.LastDate))
				fmt.Fprintf(w, "  modal acct:   %s\n", orNone(report.ModalAccount))
				fmt.Fprintf(w, "  modal vat:    %s\n", orNone(report.ModalVATType))
				if len(report.Combos) > 0 {
					fmt.Fprintln(w, "  top combos:")
					for _, c := range report.Combos {
						fmt.Fprintf(w, "    %-16s %-8s ×%d\n", c.Account, c.VATType, c.Count)
					}
				}
			})
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&flagContact, "contact", "", "Vendor contact id (alternative to the positional argument)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	return cmd
}

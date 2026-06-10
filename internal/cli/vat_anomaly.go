// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). Flags sale/purchase lines whose VAT type is
// implausible for the side (a purchase-only type on a sale, or vice versa)
// or that deviates from the modal VAT type the business usually applies to
// that (contact, account) pair. The pattern check needs >=3 samples for a
// (contact, account) group before any deviation is reported. Reads only the
// local mirror.
package cli

// pp:data-source local

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"fiken-cli/internal/fikencore"

	"github.com/spf13/cobra"
)

type vatAnomalyFinding struct {
	DocType         string `json:"doc_type"`
	DocID           int64  `json:"doc_id"`
	Date            string `json:"date"`
	ContactID       int64  `json:"contact_id,omitempty"`
	ContactName     string `json:"contact_name,omitempty"`
	Account         string `json:"account"`
	VATType         string `json:"vat_type"`
	Reason          string `json:"reason"` // invalid_for_side | deviates_from_vendor_pattern
	ExpectedVATType string `json:"expected_vat_type,omitempty"`
}

type vatAnomalyReport struct {
	Company       string              `json:"company"`
	Period        string              `json:"period,omitempty"`
	TotalFindings int                 `json:"total_findings"`
	Findings      []vatAnomalyFinding `json:"findings"`
}

// vatAnomalyLine is the minimal per-line shape the detector needs.
type vatAnomalyLine struct {
	DocType     string // sale | purchase
	Side        string // fikencore.SideSales | SidePurchases
	DocID       int64
	Date        string
	ContactID   int64
	ContactName string
	Account     string
	VATType     string
	InPeriod    bool // whether this line falls in the reported window
}

// modalVATType returns the most frequent value in counts, breaking ties
// deterministically by the lexicographically smallest type. Returns ("",0)
// for an empty map.
func modalVATType(counts map[string]int) (string, int) {
	best := ""
	bestN := 0
	for t, n := range counts {
		if n > bestN || (n == bestN && (best == "" || t < best)) {
			best, bestN = t, n
		}
	}
	return best, bestN
}

// vatAnomalyScan is the pure detector. It considers the full history (for
// building per-(contact,account) modal patterns) but only emits findings for
// lines whose InPeriod is true. minSamples is the group size threshold for the
// deviation check (the brief specifies 3).
func vatAnomalyScan(lines []vatAnomalyLine, minSamples int) []vatAnomalyFinding {
	// Build modal vatType per (contact, account) over ALL history.
	type key struct {
		contact int64
		account string
	}
	groups := map[key]map[string]int{}
	for _, ln := range lines {
		if ln.VATType == "" {
			continue
		}
		k := key{ln.ContactID, ln.Account}
		if groups[k] == nil {
			groups[k] = map[string]int{}
		}
		groups[k][ln.VATType]++
	}

	var findings []vatAnomalyFinding
	for _, ln := range lines {
		if !ln.InPeriod || ln.VATType == "" {
			continue
		}
		// Check 1: invalid for the document's side.
		if !fikencore.VATTypeValidFor(ln.VATType, ln.Side) {
			findings = append(findings, vatAnomalyFinding{
				DocType:     ln.DocType,
				DocID:       ln.DocID,
				Date:        ln.Date,
				ContactID:   ln.ContactID,
				ContactName: ln.ContactName,
				Account:     ln.Account,
				VATType:     ln.VATType,
				Reason:      "invalid_for_side",
			})
			continue // an invalid type is reported once; don't also pattern-flag it
		}
		// Check 2: deviation from the modal type for this (contact, account).
		k := key{ln.ContactID, ln.Account}
		counts := groups[k]
		total := 0
		for _, n := range counts {
			total += n
		}
		if total < minSamples {
			continue
		}
		modal, _ := modalVATType(counts)
		if modal != "" && ln.VATType != modal {
			findings = append(findings, vatAnomalyFinding{
				DocType:         ln.DocType,
				DocID:           ln.DocID,
				Date:            ln.Date,
				ContactID:       ln.ContactID,
				ContactName:     ln.ContactName,
				Account:         ln.Account,
				VATType:         ln.VATType,
				Reason:          "deviates_from_vendor_pattern",
				ExpectedVATType: modal,
			})
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Date != findings[j].Date {
			return findings[i].Date < findings[j].Date
		}
		return findings[i].DocID < findings[j].DocID
	})
	return findings
}

func newNovelVatAnomalyCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var flagPeriod string
	var dbPath string

	cmd := &cobra.Command{
		Use:   "vat-anomaly",
		Short: "Flag implausible or pattern-deviating VAT types on lines",
		Long: "Flags sale/purchase lines whose VAT type is invalid for the side (a purchase-only type\n" +
			"on a sale, etc.) or that deviates from the VAT type the business usually applies to that\n" +
			"(contact, account) pair (requires >=3 samples for the pair). Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli vat-anomaly --company fiken-demo
  fiken-cli vat-anomaly --company fiken-demo --period 2026 --agent`, "\n"),
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
				if from != "" && date < from {
					return false
				}
				if to != "" && date > to {
					return false
				}
				return true
			}
			collect := func(docs []map[string]json.RawMessage, docType, side, idKey, contactKey string) []vatAnomalyLine {
				var out []vatAnomalyLine
				for _, d := range docs {
					id, _ := jsonInt(d, idKey)
					date := jsonStr(d, "date")
					var contactID int64
					var contactName string
					// supplier/customer is a single nested object, not an array.
					if raw, ok := d[contactKey]; ok {
						var ent map[string]json.RawMessage
						if json.Unmarshal(raw, &ent) == nil {
							contactID, _ = jsonInt(ent, "contactId")
							contactName = jsonStr(ent, "name")
						}
					}
					for _, ln := range jsonObjects(d, "lines") {
						out = append(out, vatAnomalyLine{
							DocType:     docType,
							Side:        side,
							DocID:       id,
							Date:        date,
							ContactID:   contactID,
							ContactName: contactName,
							Account:     jsonStr(ln, "account"),
							VATType:     jsonStr(ln, "vatType"),
							InPeriod:    inPeriod(date),
						})
					}
				}
				return out
			}

			sales, err := loadCompanyResources(cmd.Context(), db, "sales", slug)
			if err != nil {
				return err
			}
			purchases, err := loadCompanyResources(cmd.Context(), db, "purchases", slug)
			if err != nil {
				return err
			}
			var lines []vatAnomalyLine
			lines = append(lines, collect(sales, "sale", fikencore.SideSales, "saleId", "customer")...)
			lines = append(lines, collect(purchases, "purchase", fikencore.SidePurchases, "purchaseId", "supplier")...)

			findings := vatAnomalyScan(lines, 3)
			report := vatAnomalyReport{
				Company:       slug,
				Period:        flagPeriod,
				TotalFindings: len(findings),
				Findings:      findings,
			}
			return emitFiken(cmd, flags, report, func() {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "VAT anomalies for %s", report.Company)
				if report.Period != "" {
					fmt.Fprintf(w, " (%s)", report.Period)
				}
				fmt.Fprintf(w, "\n\n")
				if len(report.Findings) == 0 {
					fmt.Fprintln(w, "No VAT anomalies detected.")
					return
				}
				for _, f := range report.Findings {
					fmt.Fprintf(w, "%s #%d  %s  account %s  vatType %s\n", f.DocType, f.DocID, f.Date, f.Account, f.VATType)
					switch f.Reason {
					case "invalid_for_side":
						fmt.Fprintf(w, "  reason: VAT type %q is not valid for a %s\n", f.VATType, f.DocType)
					case "deviates_from_vendor_pattern":
						fmt.Fprintf(w, "  reason: usually %q for %s on this account\n", f.ExpectedVATType, orNone(f.ContactName))
					}
				}
				fmt.Fprintf(w, "\nTotal findings: %d\n", report.TotalFindings)
			})
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&flagPeriod, "period", "", "Period to scan: YYYY, YYYY-MM, YYYY-Qn, or from:to (default: all)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	return cmd
}

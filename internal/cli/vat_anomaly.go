// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). Flags sale/purchase lines whose VAT type is
// implausible for the side (a purchase-only type on a sale, or vice versa)
// or that deviates from the modal VAT type the business usually applies to
// that (contact, account) pair. The pattern check needs >=3 samples for a
// (contact, account) group before any deviation is reported. Emits the shared
// Report envelope. Reads only the local mirror.
package cli

// pp:data-source local

import (
	"encoding/json"
	"sort"
	"strings"

	"fiken-cli/internal/fikencore"

	"github.com/spf13/cobra"
)

const (
	vatAnomalyInvalidForSide = "invalid_for_side"
	vatAnomalyDeviates       = "deviates_from_vendor_pattern"
	// vatAnomalyMinSamples is how many lines a (contact, account) pair needs
	// before its modal VAT type is treated as a pattern worth deviating from.
	vatAnomalyMinSamples = 3
)

// vatAnomalyLine is the minimal per-line shape the detector needs.
type vatAnomalyLine struct {
	DocType     string // sale | purchase
	Side        string // fikencore.SideSales | SidePurchases
	DocID       int64
	Date        string
	ContactID   int64
	ContactName string
	Description string
	Account     string
	VATType     string
	VATOre      int64 // the line's own VAT — the money a mis-typed line puts at stake
	InPeriod    bool  // whether this line falls in the reported window
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

// vatAnomalyFindingOf builds the shared Finding for one flagged line. A VAT
// type that is invalid for the document's side is an error (the MVA return is
// wrong); a deviation from the vendor's usual pattern is a warning (it may
// simply be an unusual but correct line).
func vatAnomalyFindingOf(ln vatAnomalyLine, kind, expected string) Finding {
	sev := SeverityWarning
	if kind == vatAnomalyInvalidForSide {
		sev = SeverityError
	}
	f := Finding{
		Kind:        kind,
		Severity:    sev,
		DocType:     ln.DocType,
		DocID:       ln.DocID,
		Date:        ln.Date,
		ContactID:   ln.ContactID,
		ContactName: ln.ContactName,
		Description: ln.Description,
		Account:     ln.Account,
		VATType:     ln.VATType,
		ImpactOre:   ln.VATOre,
	}
	if expected != "" {
		f.Detail = map[string]any{"expected_vat_type": expected}
	}
	return f
}

// vatAnomalyScan is the pure detector. It considers the full history (for
// building per-(contact,account) modal patterns) but only emits findings for
// lines whose InPeriod is true. minSamples is the group size threshold for the
// deviation check (the brief specifies 3).
func vatAnomalyScan(lines []vatAnomalyLine, minSamples int) []Finding {
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

	var findings []Finding
	for _, ln := range lines {
		if !ln.InPeriod || ln.VATType == "" {
			continue
		}
		// Check 1: invalid for the document's side.
		if !fikencore.VATTypeValidFor(ln.VATType, ln.Side) {
			findings = append(findings, vatAnomalyFindingOf(ln, vatAnomalyInvalidForSide, ""))
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
			findings = append(findings, vatAnomalyFindingOf(ln, vatAnomalyDeviates, modal))
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
	var dbPath string
	var opts reportOpts

	cmd := &cobra.Command{
		Use:   "vat-anomaly",
		Short: "Flag implausible or pattern-deviating VAT types on lines",
		Long: "Flags sale/purchase lines whose VAT type is invalid for the side (a purchase-only type\n" +
			"on a sale, etc.) or that deviates from the VAT type the business usually applies to that\n" +
			"(contact, account) pair (requires >=3 samples for the pair). The pattern is learned from\n" +
			"all history; only lines inside the period are reported. Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli vat-anomaly --company fiken-demo
  fiken-cli vat-anomaly --company fiken-demo --period 2026 --agent`, "\n"),
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

			undated := 0
			collect := func(docs []map[string]json.RawMessage, docType, side, idKey, contactKey string) []vatAnomalyLine {
				var out []vatAnomalyLine
				for _, d := range docs {
					id, _ := jsonInt(d, idKey)
					date := jsonStr(d, "date")
					if date == "" {
						// Undated is counted per document, not per line: the
						// document cannot be placed in any window, so its
						// lines are pattern history only.
						undated++
					}
					var contactID int64
					var contactName string
					// supplier/customer is a single nested object, not an array.
					if ent := jsonObject(d, contactKey); ent != nil {
						contactID, _ = jsonInt(ent, "contactId")
						contactName = jsonStr(ent, "name")
					}
					inPeriod := win.Contains(date)
					for _, ln := range jsonObjects(d, "lines") {
						vat, _ := jsonInt(ln, "vat")
						out = append(out, vatAnomalyLine{
							DocType:     docType,
							Side:        side,
							DocID:       id,
							Date:        date,
							ContactID:   contactID,
							ContactName: contactName,
							Description: jsonStr(ln, "description"),
							Account:     jsonStr(ln, "account"),
							VATType:     jsonStr(ln, "vatType"),
							VATOre:      vat,
							InPeriod:    inPeriod,
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

			report := Report{
				Company:  slug,
				Detector: "vat_anomaly",
				Window:   win,
				Undated:  undated,
				Params:   map[string]any{"min_samples": vatAnomalyMinSamples},
			}
			keyFn := func(f Finding) map[string]string {
				expected, _ := f.Detail["expected_vat_type"].(string)
				return map[string]string{
					"kind":              f.Kind,
					"vat_type":          f.VATType,
					"expected_vat_type": expected,
				}
			}
			return finishReport(cmd, flags, report, vatAnomalyScan(lines, vatAnomalyMinSamples), keyFn, opts)
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	addReportFlags(cmd, &opts)
	return cmd
}

// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). Flags sale/purchase lines whose VAT type is
// implausible for the side (a purchase-only type on a sale, or vice versa)
// or that deviates from the modal VAT type the business usually applies to
// that (contact, account, description) group. The modal is recency-bounded:
// only the group's lines in the 12 months before a line's own date vote, and
// the modal needs --min-samples (default 3) supporting lines before any
// deviation is reported. Emits the shared Report envelope. Reads only the
// local mirror.
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
	// vatAnomalyWindowMonths bounds the pattern: a line is compared only to its
	// group's lines from the 12 months before it, so a vendor that changed VAT
	// regime a year ago has a clean modal today.
	vatAnomalyWindowMonths = 12
	// vatAnomalyDefaultMinSamples is the default --min-samples: how many lines
	// must support the modal inside that window before a deviation is reported.
	vatAnomalyDefaultMinSamples = 3
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

// normaliseDescription folds a line description into a group key: lowercased,
// trimmed, internal whitespace runs collapsed to one space. Digits are kept —
// two bills for "Strøm 03/2026" and "Strøm 04/2026" are different products only
// if the business names them differently, and stripping numbers would merge
// genuinely distinct lines.
func normaliseDescription(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// vatAnomalyFindingOf builds the shared Finding for one flagged line. A VAT
// type that is invalid for the document's side is an error (the MVA return is
// wrong); a deviation from the vendor's usual pattern is a warning (it may
// simply be an unusual but correct line).
func vatAnomalyFindingOf(ln vatAnomalyLine, kind, expected string, support int) Finding {
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
		f.Detail = map[string]any{"expected_vat_type": expected, "modal_support": support}
	}
	return f
}

// vatAnomalyScan is the pure detector. It considers the full history (for
// building per-group patterns) but only emits findings for lines whose
// InPeriod is true. The expected VAT type for a line is the modal over its
// (contact, account, normalised description) group in the 12 months strictly
// before the line's own date; minSamples is how many lines must support that
// modal before a deviation is reported.
func vatAnomalyScan(lines []vatAnomalyLine, minSamples int) []Finding {
	// Group observations by (contact, account, normalised description) over ALL
	// history; recentModal does the date bounding per line.
	type key struct {
		contact int64
		account string
		desc    string
	}
	groups := map[key][]dated{}
	keyOf := func(ln vatAnomalyLine) key {
		return key{ln.ContactID, ln.Account, normaliseDescription(ln.Description)}
	}
	for _, ln := range lines {
		if ln.VATType == "" {
			continue
		}
		k := keyOf(ln)
		groups[k] = append(groups[k], dated{Date: ln.Date, Value: ln.VATType})
	}

	var findings []Finding
	for _, ln := range lines {
		if !ln.InPeriod || ln.VATType == "" {
			continue
		}
		// Check 1: invalid for the document's side.
		if !fikencore.VATTypeValidFor(ln.VATType, ln.Side) {
			findings = append(findings, vatAnomalyFindingOf(ln, vatAnomalyInvalidForSide, "", 0))
			continue // an invalid type is reported once; don't also pattern-flag it
		}
		// Check 2: deviation from the group's recent modal. recentModal's window
		// is exclusive of the line's own date, so the line itself and its
		// same-day siblings never vote on their own expected value.
		modal, support := recentModal(groups[keyOf(ln)], ln.Date, vatAnomalyWindowMonths)
		if support < minSamples {
			continue
		}
		if modal != "" && ln.VATType != modal {
			findings = append(findings, vatAnomalyFindingOf(ln, vatAnomalyDeviates, modal, support))
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
	var minSamples int
	var opts reportOpts

	cmd := &cobra.Command{
		Use:   "vat-anomaly",
		Short: "Flag implausible or pattern-deviating VAT types on lines",
		Long: "Flags sale/purchase lines whose VAT type is invalid for the side (a purchase-only type\n" +
			"on a sale, etc.) or that deviates from the VAT type the business usually applies to that\n" +
			"(contact, account, description) group. The expected type is the modal over that group's\n" +
			"lines in the 12 months before the line's own date, and needs --min-samples supporting\n" +
			"lines. History is read in full; only lines inside the period are reported. Reads the\n" +
			"local mirror.",
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
				Params:   map[string]any{"min_samples": minSamples, "modal_window_months": vatAnomalyWindowMonths},
			}
			keyFn := func(f Finding) map[string]string {
				expected, _ := f.Detail["expected_vat_type"].(string)
				return map[string]string{
					"kind":              f.Kind,
					"vat_type":          f.VATType,
					"expected_vat_type": expected,
				}
			}
			return finishReport(cmd, flags, report, vatAnomalyScan(lines, minSamples), keyFn, opts)
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	cmd.Flags().IntVar(&minSamples, "min-samples", vatAnomalyDefaultMinSamples,
		"Lines that must support a group's 12-month modal VAT type before a deviation is reported")
	addReportFlags(cmd, &opts)
	return cmd
}

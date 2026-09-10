// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). Finds rounding/total drift and VAT-rate
// mismatches across sales and purchases in a period. Two checks:
//   - total_mismatch: a sale's summed line netPrice/vat disagreeing with its
//     own netAmount/vatAmount header (purchases carry no reliable header total,
//     so they're exempt from this check).
//   - the per-regime VAT invariant of every order line, from the authority in
//     internal/fikencore: ordinary domestic lines must carry round(net*rate)
//     (vat_rate), basis and zero-rated lines must carry no VAT at all
//     (basis_has_vat / zero_rated_has_vat), a direct line must carry no basis
//     (direct_has_net), and a nondeductible reverse-charge line must carry the
//     negative embedded VAT (nondeductible_embedded_vat). A vatType the
//     authority does not know is reported (unknown_vat_type), never skipped.
//
// Note: journal-entry debit≠credit imbalance is NOT computable from the GET
// data — the mirror's journal lines carry a signed `amount` but no
// debit/credit direction — so that check is deliberately omitted. Reads only
// the local mirror.
package cli

// pp:data-source local

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"fiken-cli/internal/fikencore"
)

// driftSeverity grades a drift kind. The regime-invariant breaches are
// errors: the line contradicts the VAT regime its own vatType declares, so the
// MVA return is wrong. A rate difference or a header/lines difference is a
// warning — usually rounding or an aggregation artefact, not a wrong regime.
func driftSeverity(kind string) Severity {
	switch kind {
	case driftKindTotalMismatch, fikencore.KindVATRate:
		return SeverityWarning
	default:
		return SeverityError
	}
}

const driftKindTotalMismatch = "total_mismatch"

func absInt64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// driftScanDoc represents the minimal shape the drift detector needs from one
// sale or purchase: a header net/vat total (sales only; zero+hasHeader=false
// for purchases), the counterpart contact, and its order lines.
type driftScanDoc struct {
	DocType     string // sale | purchase
	DocID       int64
	Date        string
	ContactID   int64
	ContactName string
	Description string
	HasHeader   bool  // sales carry netAmount/vatAmount; purchases do not
	NetAmount   int64 // header net (øre)
	VATAmount   int64 // header vat (øre)
	Lines       []driftScanLine
}

type driftScanLine struct {
	Account     string
	VATType     string
	Description string
	NetPrice    int64
	VAT         int64
}

// driftNote explains a line-invariant violation in the terms of its regime.
func driftNote(kind, vatType string, expected, actual int64) string {
	switch kind {
	case fikencore.KindBasisHasVAT:
		return fmt.Sprintf("%s is a basis (grunnlag) type — the VAT belongs in the MVA return, not on the line, but the line carries %s", vatType, kr(actual))
	case fikencore.KindZeroRatedHasVAT:
		return fmt.Sprintf("%s carries no VAT, but the line carries %s", vatType, kr(actual))
	case fikencore.KindDirectHasNet:
		return fmt.Sprintf("%s posts VAT with no basis — netPrice should be 0, but the line carries %s", vatType, kr(actual))
	case fikencore.KindNondeductibleEmbeddedVAT:
		return fmt.Sprintf("%s has a VAT-inclusive netPrice, so the line vat should be the negative embedded %s, not %s", vatType, kr(expected), kr(actual))
	default:
		return fmt.Sprintf("vat %s differs from %s expected at rate for %s", kr(actual), kr(expected), vatType)
	}
}

// driftFinding builds the shared Finding for one drift hit. ImpactOre is the
// signed difference; expected/actual stay in Detail as raw øre (the text
// renderer formats them, so the JSON carries no pre-formatted kroner strings).
func driftFinding(d driftScanDoc, kind, account, vatType, description string, expected, actual, diff int64, note string) Finding {
	if description == "" {
		description = d.Description
	}
	return Finding{
		Kind:        kind,
		Severity:    driftSeverity(kind),
		DocType:     d.DocType,
		DocID:       d.DocID,
		Date:        d.Date,
		ContactID:   d.ContactID,
		ContactName: d.ContactName,
		Description: description,
		Account:     account,
		VATType:     vatType,
		ImpactOre:   diff,
		Detail: map[string]any{
			"expected_ore": expected,
			"actual_ore":   actual,
			"note":         note,
		},
	}
}

// driftScan is the pure detector: given the scanned docs and a tolerance in
// øre, it returns every finding. Kept free of Cobra/DB so it is unit-testable.
func driftScan(docs []driftScanDoc, toleranceOre int64) []Finding {
	var findings []Finding
	for _, d := range docs {
		// total_mismatch: header vs summed lines (sales only).
		if d.HasHeader {
			var sumNet, sumVAT int64
			for _, ln := range d.Lines {
				sumNet += ln.NetPrice
				sumVAT += ln.VAT
			}
			if diff := sumNet - d.NetAmount; absInt64(diff) > toleranceOre {
				findings = append(findings, driftFinding(d, driftKindTotalMismatch, "", "", "",
					d.NetAmount, sumNet, diff, "sum of line net differs from header netAmount"))
			}
			if diff := sumVAT - d.VATAmount; absInt64(diff) > toleranceOre {
				findings = append(findings, driftFinding(d, driftKindTotalMismatch, "", "", "",
					d.VATAmount, sumVAT, diff, "sum of line vat differs from header vatAmount"))
			}
		}
		// Per-line VAT: the invariant that the line's regime carries.
		for _, ln := range d.Lines {
			info, known := fikencore.Lookup(ln.VATType)
			if !known {
				findings = append(findings, driftFinding(d, fikencore.KindUnknownVATType, ln.Account, ln.VATType, ln.Description,
					0, ln.VAT, 0,
					fmt.Sprintf("vatType %s is not a recognized Fiken type — its VAT cannot be checked", orNone(ln.VATType))))
				continue
			}
			ok, kind, diff := fikencore.LineInvariant(info, ln.NetPrice, ln.VAT, toleranceOre)
			if ok {
				continue
			}
			// For every kind but direct_has_net the quantity under test is the
			// line's vat; for that one it is the net, which must be zero.
			actual := ln.VAT
			if kind == fikencore.KindDirectHasNet {
				actual = ln.NetPrice
			}
			expected := actual - diff
			findings = append(findings, driftFinding(d, kind, ln.Account, ln.VATType, ln.Description,
				expected, actual, diff, driftNote(kind, ln.VATType, expected, actual)))
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

func newNovelDriftCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var dbPath string
	var toleranceOre int64
	var opts reportOpts

	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Find rounding/total drift and VAT-rate mismatches",
		Long: "Scans sales and purchases in a period for two problems: a sale whose summed line\n" +
			"net/vat disagrees with its own header totals, and any line that breaks the VAT\n" +
			"invariant of its vatType's regime — ordinary domestic lines must carry the rate,\n" +
			"basis and zero-rated lines no VAT, direct lines no basis, and nondeductible\n" +
			"reverse-charge lines the negative embedded VAT. Journal debit/credit imbalance is\n" +
			"NOT checked — the mirror's journal lines lack a debit/credit direction. Reads the\n" +
			"local mirror.",
		Example: strings.Trim(`
  fiken-cli drift --company fiken-demo
  fiken-cli drift --company fiken-demo --period 2026-Q1 --tolerance-ore 2 --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			win, err := resolvePeriod(opts.period)
			if err != nil {
				return err
			}
			if toleranceOre < 0 {
				return fmt.Errorf("--tolerance-ore must be >= 0, got %d", toleranceOre)
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

			scanLines := func(m map[string]json.RawMessage) []driftScanLine {
				var out []driftScanLine
				for _, ln := range jsonObjects(m, "lines") {
					net, _ := jsonInt(ln, "netPrice")
					vat, _ := jsonInt(ln, "vat")
					out = append(out, driftScanLine{
						Account:     jsonStr(ln, "account"),
						VATType:     jsonStr(ln, "vatType"),
						Description: jsonStr(ln, "description"),
						NetPrice:    net,
						VAT:         vat,
					})
				}
				return out
			}
			// The counterpart contact is embedded in the document (customer on
			// a sale, supplier on a purchase), so no second lookup is needed.
			contactOf := func(m map[string]json.RawMessage, key string) (int64, string) {
				ent := jsonObject(m, key)
				if ent == nil {
					return 0, ""
				}
				id, _ := jsonInt(ent, "contactId")
				return id, jsonStr(ent, "name")
			}

			var docs []driftScanDoc
			undated := 0
			sales, err := loadCompanyResources(cmd.Context(), db, "sales", slug)
			if err != nil {
				return err
			}
			for _, s := range sales {
				date := jsonStr(s, "date")
				if date == "" {
					undated++
					continue
				}
				if !win.Contains(date) {
					continue
				}
				id, _ := jsonInt(s, "saleId")
				net, _ := jsonInt(s, "netAmount")
				vat, _ := jsonInt(s, "vatAmount")
				cid, cname := contactOf(s, "customer")
				docs = append(docs, driftScanDoc{
					DocType:     "sale",
					DocID:       id,
					Date:        date,
					ContactID:   cid,
					ContactName: cname,
					Description: jsonStr(s, "saleNumber"),
					HasHeader:   true,
					NetAmount:   net,
					VATAmount:   vat,
					Lines:       scanLines(s),
				})
			}
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
				if !win.Contains(date) {
					continue
				}
				id, _ := jsonInt(p, "purchaseId")
				cid, cname := contactOf(p, "supplier")
				docs = append(docs, driftScanDoc{
					DocType:     "purchase",
					DocID:       id,
					Date:        date,
					ContactID:   cid,
					ContactName: cname,
					Description: jsonStr(p, "identifier"),
					Lines:       scanLines(p),
				})
			}

			report := Report{
				Company:  slug,
				Detector: "drift",
				Window:   win,
				Undated:  undated,
				Params:   map[string]any{"tolerance_ore": toleranceOre},
			}
			keyFn := func(f Finding) map[string]string {
				return map[string]string{"kind": f.Kind, "vat_type": f.VATType}
			}
			return finishReport(cmd, flags, report, driftScan(docs, toleranceOre), keyFn, opts)
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	cmd.Flags().Int64Var(&toleranceOre, "tolerance-ore", 5, "Ignore differences at or below this many øre (flat, not a percentage)")
	addReportFlags(cmd, &opts)
	return cmd
}

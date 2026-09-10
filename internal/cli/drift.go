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
//   - settled_residual: a document marked settled whose ledger disagrees — a
//     purchase whose supplier account (2400*) still carries a balance in its
//     transaction, or a sale with a non-zero outstandingBalance.
//   - currency_mismatch: a foreign-currency purchase whose line netPrice
//     equals its netPriceInCurrency, i.e. never converted to NOK.
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
// MVA return is wrong. So is a settled document that still has a balance — the
// books say paid and the ledger disagrees. A rate difference, a header/lines
// difference or a suspected currency slip is a warning: usually rounding, an
// aggregation artefact or a judgement call on the invoice, not a wrong regime.
func driftSeverity(kind string) Severity {
	switch kind {
	case driftKindTotalMismatch, driftKindCurrencyMismatch, fikencore.KindVATRate:
		return SeverityWarning
	default:
		return SeverityError
	}
}

const (
	driftKindTotalMismatch = "total_mismatch"
	// driftKindSettledResidual: the document is marked settled but its
	// ledger side is not zero — a purchase whose supplier account still
	// carries a balance, or a sale with an outstanding balance.
	driftKindSettledResidual = "settled_residual"
	// driftKindCurrencyMismatch: a foreign-currency purchase whose line
	// netPrice equals its netPriceInCurrency, i.e. the currency amount was
	// typed into the NOK field and never converted.
	driftKindCurrencyMismatch = "currency_mismatch"
)

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

	// The settled/currency checks read the document header rather than its
	// lines. TransactionID joins the document to its journal (0 when the
	// mirror row carries none); Currency is the ISO code the document was
	// issued in, "" or "NOK" meaning domestic.
	Settled            bool
	Paid               bool
	OutstandingBalance int64 // sales only (øre)
	Currency           string
	TransactionID      int64
}

type driftScanLine struct {
	Account     string
	VATType     string
	Description string
	NetPrice    int64
	VAT         int64
	// NetPriceInCurrency is only meaningful when the document carries a
	// foreign currency, and only when the field was present at all — hence
	// the companion flag, so an absent field is never read as 0 == 0.
	NetPriceInCurrency    int64
	HasNetPriceInCurrency bool
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

// driftDocFinding builds a document-level Finding — one that belongs to the
// document as a whole rather than to a single order line, so it carries no
// expected/actual pair.
func driftDocFinding(d driftScanDoc, kind string, impactOre int64, detail map[string]any) Finding {
	return Finding{
		Kind:        kind,
		Severity:    driftSeverity(kind),
		DocType:     d.DocType,
		DocID:       d.DocID,
		Date:        d.Date,
		ContactID:   d.ContactID,
		ContactName: d.ContactName,
		Description: d.Description,
		ImpactOre:   impactOre,
		Detail:      detail,
	}
}

// driftSettled checks a document marked settled against the ledger. A
// purchase's truth is its supplier account: Fiken debits 2400 (or the
// supplier's 2400:NNNNN sub-account) when the invoice is paid, so a settled
// purchase's supplier lines must sum to 0. A sale's truth is its own
// outstandingBalance. Returns the finding, if any, and whether the purchase
// could be checked at all — a purchase with no transactionId, or one whose
// transaction is not mirrored, is skipped rather than reported as residual 0.
func driftSettled(d driftScanDoc, ix *txIndex) (f Finding, found, indexed bool) {
	if !d.Settled {
		return Finding{}, false, true
	}
	if d.DocType != "purchase" {
		if d.OutstandingBalance == 0 {
			return Finding{}, false, true
		}
		return driftDocFinding(d, driftKindSettledResidual, d.OutstandingBalance, map[string]any{
			"settled":         true,
			"outstanding_ore": d.OutstandingBalance,
		}), true, true
	}
	if d.TransactionID == 0 || !ix.has(d.TransactionID) {
		return Finding{}, false, false
	}
	residual := ix.sumAccount(d.TransactionID, isSupplierAccount)
	if residual == 0 {
		return Finding{}, false, true
	}
	return driftDocFinding(d, driftKindSettledResidual, residual, map[string]any{
		"paid":         d.Paid,
		"settled":      true,
		"residual_ore": residual,
	}), true, true
}

// driftCurrency flags the "USD typed into the NOK field" case: on a purchase
// issued in a foreign currency, a line whose netPrice equals its
// netPriceInCurrency was never converted. One finding per purchase — the first
// such line is enough to make the document worth opening.
func driftCurrency(d driftScanDoc) (Finding, bool) {
	if d.DocType != "purchase" || d.Currency == "" || d.Currency == "NOK" {
		return Finding{}, false
	}
	for _, ln := range d.Lines {
		if !ln.HasNetPriceInCurrency || ln.NetPrice != ln.NetPriceInCurrency {
			continue
		}
		f := driftDocFinding(d, driftKindCurrencyMismatch, ln.NetPrice, map[string]any{
			"currency":              d.Currency,
			"net_price_ore":         ln.NetPrice,
			"net_price_in_currency": ln.NetPriceInCurrency,
		})
		f.Account = ln.Account
		f.VATType = ln.VATType
		return f, true
	}
	return Finding{}, false
}

// driftScan is the pure detector: given the scanned docs, a tolerance in øre
// and the journal index (nil-safe — an unsynced `transactions` resource simply
// disables the settled check), it returns every finding plus the number of
// settled purchases that could not be checked because their transaction is not
// in the index. Kept free of Cobra/DB so it is unit-testable.
func driftScan(docs []driftScanDoc, toleranceOre int64, ix *txIndex) (findings []Finding, settledUnindexed int) {
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
		// Document-level checks: settled vs the ledger, and the
		// unconverted foreign-currency amount.
		if f, ok, indexed := driftSettled(d, ix); ok {
			findings = append(findings, f)
		} else if !indexed {
			settledUnindexed++
		}
		if f, ok := driftCurrency(d); ok {
			findings = append(findings, f)
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Date != findings[j].Date {
			return findings[i].Date < findings[j].Date
		}
		return findings[i].DocID < findings[j].DocID
	})
	return findings, settledUnindexed
}

func newNovelDriftCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var dbPath string
	var toleranceOre int64
	var opts reportOpts

	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Find rounding/total drift and VAT-rate mismatches",
		Long: "Scans sales and purchases in a period for four problems: a sale whose summed line\n" +
			"net/vat disagrees with its own header totals; any line that breaks the VAT\n" +
			"invariant of its vatType's regime — ordinary domestic lines must carry the rate,\n" +
			"basis and zero-rated lines no VAT, direct lines no basis, and nondeductible\n" +
			"reverse-charge lines the negative embedded VAT; a document marked settled whose\n" +
			"ledger still carries a balance (settled_residual); and a foreign-currency purchase\n" +
			"whose line amount was never converted to NOK (currency_mismatch). Journal\n" +
			"debit/credit imbalance is NOT checked — the mirror's journal lines lack a\n" +
			"debit/credit direction. Reads the local mirror.",
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

			// The journal index powers the settled check; a company that
			// never synced `transactions` gets an empty index and the
			// check quietly reports nothing (params say so).
			ix, err := loadTxIndex(cmd.Context(), db, slug)
			if err != nil {
				return err
			}

			scanLines := func(m map[string]json.RawMessage) []driftScanLine {
				var out []driftScanLine
				for _, ln := range jsonObjects(m, "lines") {
					net, _ := jsonInt(ln, "netPrice")
					vat, _ := jsonInt(ln, "vat")
					netCur, hasNetCur := jsonInt(ln, "netPriceInCurrency")
					out = append(out, driftScanLine{
						Account:               jsonStr(ln, "account"),
						VATType:               jsonStr(ln, "vatType"),
						Description:           jsonStr(ln, "description"),
						NetPrice:              net,
						VAT:                   vat,
						NetPriceInCurrency:    netCur,
						HasNetPriceInCurrency: hasNetCur,
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
				outstanding, _ := jsonInt(s, "outstandingBalance")
				txID, _ := jsonInt(s, "transactionId")
				cid, cname := contactOf(s, "customer")
				docs = append(docs, driftScanDoc{
					DocType:            "sale",
					DocID:              id,
					Date:               date,
					ContactID:          cid,
					ContactName:        cname,
					Description:        jsonStr(s, "saleNumber"),
					HasHeader:          true,
					NetAmount:          net,
					VATAmount:          vat,
					Lines:              scanLines(s),
					Settled:            jsonBoolField(s, "settled"),
					Paid:               jsonBoolField(s, "paid"),
					OutstandingBalance: outstanding,
					Currency:           jsonStr(s, "currency"),
					TransactionID:      txID,
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
				txID, _ := jsonInt(p, "transactionId")
				cid, cname := contactOf(p, "supplier")
				docs = append(docs, driftScanDoc{
					DocType:       "purchase",
					DocID:         id,
					Date:          date,
					ContactID:     cid,
					ContactName:   cname,
					Description:   jsonStr(p, "identifier"),
					Lines:         scanLines(p),
					Settled:       jsonBoolField(p, "settled"),
					Paid:          jsonBoolField(p, "paid"),
					Currency:      jsonStr(p, "currency"),
					TransactionID: txID,
				})
			}

			findings, settledUnindexed := driftScan(docs, toleranceOre, ix)
			report := Report{
				Company:  slug,
				Detector: "drift",
				Window:   win,
				Undated:  undated,
				Params: map[string]any{
					"tolerance_ore":        toleranceOre,
					"transactions_indexed": ix.count(),
					"settled_unindexed":    settledUnindexed,
				},
			}
			keyFn := func(f Finding) map[string]string {
				return map[string]string{"kind": f.Kind, "vat_type": f.VATType}
			}
			return finishReport(cmd, flags, report, findings, keyFn, opts)
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	cmd.Flags().Int64Var(&toleranceOre, "tolerance-ore", 5, "Ignore differences at or below this many øre (flat, not a percentage)")
	addReportFlags(cmd, &opts)
	return cmd
}

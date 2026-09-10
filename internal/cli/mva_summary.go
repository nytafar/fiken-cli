// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). Reconstructs a VAT-return-shaped view from the
// local mirror. Every line is mapped onto its basis, output VAT and input VAT
// by the regime authority in internal/fikencore rather than by summing the
// line's `vat` field — which is 0 on a basis line, the whole amount on a
// direct line and negative on a nondeductible reverse-charge line. A purchase
// bucket can therefore carry output VAT (reverse charge), and the reported net
// position is total output − total input across both sides. Reads only the
// local mirror.
//
// Buckets are keyed by (vatType, mva_code) — a return is read by code, and the
// five both-sided types carry a different code per side. A second pass
// cross-checks the reverse-charge purchase buckets against the 2702/2712 pair
// Fiken posted in the journal (see transaction_index.go) and reports a
// disagreement as a finding; the summary stays an aggregate report, so those
// findings ride along rather than turning it into a detector Report.
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

type mvaBucket struct {
	VATType      string `json:"vat_type"`
	Regime       string `json:"regime"`
	MVACode      int    `json:"mva_code,omitempty"`
	BasisOre     int64  `json:"basis_ore"`
	Basis        string `json:"basis"`
	OutputVATOre int64  `json:"output_vat_ore"`
	OutputVAT    string `json:"output_vat"`
	InputVATOre  int64  `json:"input_vat_ore"`
	InputVAT     string `json:"input_vat"`
}

type mvaSummaryReport struct {
	Company             string      `json:"company"`
	Window              Window      `json:"window"`
	UndatedDocuments    int         `json:"undated_documents"`
	TransactionsIndexed int         `json:"transactions_indexed"`
	Output              []mvaBucket `json:"output"`
	Input               []mvaBucket `json:"input"`
	TotalOutputOre      int64       `json:"total_output_vat_ore"`
	TotalOutput         string      `json:"total_output_vat"`
	TotalInputOre       int64       `json:"total_input_vat_ore"`
	TotalInput          string      `json:"total_input_vat"`
	NetVATOre           int64       `json:"net_vat_ore"`
	NetVAT              string      `json:"net_vat"`
	Findings            []Finding   `json:"findings"`
}

// mvaLine is the minimal per-line shape the summarizer needs. TxID is the
// document's transactionId, carried so the reverse-charge cross-check can find
// the journal Fiken posted for it (0 when the document has none).
type mvaLine struct {
	VATType  string
	BasisOre int64 // netPrice
	VATOre   int64 // vat
	TxID     int64
}

// mvaKind is the bucket key: a vatType alone is not enough, because the five
// both-sided types carry a different MVA code per side and a return is read by
// code. Sorting is by vatType then code.
type mvaKey struct {
	vatType string
	code    int
}

// mvaBucketKey resolves the (vatType, mva_code) key and the regime label for
// one line on a given side. An unknown vatType keeps its own key with code 0
// and regime "unknown" — visible rather than silently dropped.
func mvaBucketKey(vatType, side string) (key mvaKey, regime string) {
	info, known := fikencore.Lookup(vatType)
	if !known {
		return mvaKey{vatType: vatType}, "unknown"
	}
	code := info.Code
	if c, ok := fikencore.CodeFor(vatType, side); ok {
		code = c
	}
	return mvaKey{vatType: vatType, code: code}, info.Regime.String()
}

// summarizeMVA buckets one side's lines by (vatType, mva_code) and returns the
// sorted buckets plus the output and input VAT they total to. side is the
// document side the lines came from ("sales" or "purchases"): the five
// both-sided types cannot say from the taxonomy alone whether their VAT is
// output or input, so the document answers that. A vatType the authority does
// not know gets its own bucket with regime "unknown" and the line's raw vat on
// the document's side — visible rather than silently dropped. Pure; no
// Cobra/DB.
func summarizeMVA(lines []mvaLine, side string) (buckets []mvaBucket, outputVATOre, inputVATOre int64) {
	type agg struct {
		regime            string
		basis, output, in int64
	}
	by := map[mvaKey]*agg{}
	for _, ln := range lines {
		key, regime := mvaBucketKey(ln.VATType, side)
		info, _ := fikencore.Lookup(ln.VATType)
		if info.Side == fikencore.SideBoth || info.Side == "" {
			info.Side = side
		}
		basis, output, input := fikencore.ReturnFigures(info, ln.BasisOre, ln.VATOre)
		a := by[key]
		if a == nil {
			a = &agg{regime: regime}
			by[key] = a
		}
		a.basis += basis
		a.output += output
		a.in += input
		outputVATOre += output
		inputVATOre += input
	}
	buckets = make([]mvaBucket, 0, len(by))
	for key, a := range by {
		buckets = append(buckets, mvaBucket{
			VATType:      key.vatType,
			Regime:       a.regime,
			MVACode:      key.code,
			BasisOre:     a.basis,
			Basis:        kr(a.basis),
			OutputVATOre: a.output,
			OutputVAT:    kr(a.output),
			InputVATOre:  a.in,
			InputVAT:     kr(a.in),
		})
	}
	sort.SliceStable(buckets, func(i, j int) bool {
		if buckets[i].VATType != buckets[j].VATType {
			return buckets[i].VATType < buckets[j].VATType
		}
		return buckets[i].MVACode < buckets[j].MVACode
	})
	return buckets, outputVATOre, inputVATOre
}

// mvaCrossCheckToleranceOre is the per-bucket slack on the journal comparison:
// the summary rounds per line and Fiken rounds per posting, so a few øre of
// difference across a bucket is arithmetic, not an error.
const mvaCrossCheckToleranceOre = 5

// mvaKindReverseChargeMismatch is emitted when a reverse-charge bucket's
// computed output VAT disagrees with the 2702/2712 pair Fiken actually posted.
const mvaKindReverseChargeMismatch = "reverse_charge_journal_mismatch"

// crossCheckReverseCharge compares each purchase bucket in the reverse-charge
// regime — RegimeBasis, the one that produces equal output and input VAT
// without ever touching the order line's `vat` field — against the journal.
// The summary computes that VAT from the basis and the rate; Fiken posts it as
// a 2702/2712 pair on the purchase's transaction. Those two must agree.
//
// A bucket whose purchases have no mirrored transaction at all is not
// reported: nothing was compared, so there is nothing to disagree about. That
// is also what makes an unsynced `transactions` resource silent rather than
// noisy. Pure; no Cobra/DB.
func crossCheckReverseCharge(lines []mvaLine, buckets []mvaBucket, ix *txIndex) []Finding {
	txByKey := map[mvaKey]map[int64]bool{}
	for _, ln := range lines {
		info, known := fikencore.Lookup(ln.VATType)
		if !known || info.Regime != fikencore.RegimeBasis || ln.TxID == 0 {
			continue
		}
		key, _ := mvaBucketKey(ln.VATType, fikencore.SidePurchases)
		if txByKey[key] == nil {
			txByKey[key] = map[int64]bool{}
		}
		txByKey[key][ln.TxID] = true
	}
	findings := []Finding{}
	for _, b := range buckets {
		ids := txByKey[mvaKey{vatType: b.VATType, code: b.MVACode}]
		if len(ids) == 0 {
			continue
		}
		var journal int64
		matched := 0
		for id := range ids {
			if !ix.has(id) {
				continue
			}
			journal += ix.reverseChargeOutputVAT(id)
			matched++
		}
		if matched == 0 {
			continue
		}
		diff := b.OutputVATOre - journal
		if absInt64(diff) <= mvaCrossCheckToleranceOre {
			continue
		}
		findings = append(findings, Finding{
			Kind:      mvaKindReverseChargeMismatch,
			Severity:  SeverityWarning,
			DocType:   "bucket",
			VATType:   b.VATType,
			ImpactOre: diff,
			Detail: map[string]any{
				"mva_code":              b.MVACode,
				"bucket_output_vat_ore": b.OutputVATOre,
				"journal_vat_ore":       journal,
				"transactions_matched":  matched,
			},
		})
	}
	return findings
}

func newNovelMvaSummaryCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var flagPeriod string
	var dbPath string

	cmd := &cobra.Command{
		Use:   "mva-summary",
		Short: "Summarize output/input VAT per VAT type (a VAT-return-shaped view)",
		Long: "Buckets sales and purchase lines by VAT type, mapping each line onto the basis,\n" +
			"output VAT and input VAT its vatType's regime implies (a reverse-charge purchase\n" +
			"carries both sides; a basis line's VAT is computed, not read off the line), and\n" +
			"reports the net VAT position (total output − total input).\n" +
			"--period scopes to a term and defaults to the current year; pass `all` to summarize\n" +
			"everything. Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli mva-summary --company fiken-demo --period 2026-Q1
  fiken-cli mva-summary --company fiken-demo --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			win, err := resolvePeriod(flagPeriod)
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

			// A document with no date cannot be placed in any window, so it is
			// excluded always and counted instead of silently vanishing.
			undated := 0
			collect := func(docs []map[string]json.RawMessage) []mvaLine {
				var out []mvaLine
				for _, d := range docs {
					date := jsonStr(d, "date")
					if date == "" {
						undated++
						continue
					}
					if !win.Contains(date) {
						continue
					}
					txID, _ := jsonInt(d, "transactionId")
					for _, ln := range jsonObjects(d, "lines") {
						net, _ := jsonInt(ln, "netPrice")
						vat, _ := jsonInt(ln, "vat")
						out = append(out, mvaLine{
							VATType:  jsonStr(ln, "vatType"),
							BasisOre: net,
							VATOre:   vat,
							TxID:     txID,
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

			// The journal side of the cross-check. A company that never
			// synced `transactions` gets an empty index, and the
			// cross-check then emits nothing.
			ix, err := loadTxIndex(cmd.Context(), db, slug)
			if err != nil {
				return err
			}

			// A purchase can carry output VAT (reverse charge), so the totals
			// are taken across both sides, not one per side.
			purchLines := collect(purchases)
			salesBuckets, salesOut, salesIn := summarizeMVA(collect(sales), fikencore.SideSales)
			purchBuckets, purchOut, purchIn := summarizeMVA(purchLines, fikencore.SidePurchases)
			totalOut := salesOut + purchOut
			totalIn := salesIn + purchIn
			net := totalOut - totalIn
			report := mvaSummaryReport{
				Company:             slug,
				Window:              win,
				UndatedDocuments:    undated,
				TransactionsIndexed: ix.count(),
				Output:              salesBuckets,
				Input:               purchBuckets,
				TotalOutputOre:      totalOut,
				TotalOutput:         kr(totalOut),
				TotalInputOre:       totalIn,
				TotalInput:          kr(totalIn),
				NetVATOre:           net,
				NetVAT:              kr(net),
				Findings:            crossCheckReverseCharge(purchLines, purchBuckets, ix),
			}
			return emitFiken(cmd, flags, report, func() {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "MVA summary for %s (%s)\n", report.Company, report.Window.String())
				fmt.Fprintf(w, "Output VAT %s kr, input VAT %s kr — net (output − input) %s kr",
					report.TotalOutput, report.TotalInput, report.NetVAT)
				if report.UndatedDocuments > 0 {
					fmt.Fprintf(w, ", %d undated document(s) skipped", report.UndatedDocuments)
				}
				fmt.Fprintf(w, "\n\n")
				printBuckets := func(title string, bs []mvaBucket) {
					fmt.Fprintf(w, "%s:\n", title)
					if len(bs) == 0 {
						fmt.Fprintln(w, "  (none)")
						return
					}
					for _, b := range bs {
						code := ""
						if b.MVACode != 0 {
							code = fmt.Sprintf("kode %d", b.MVACode)
						}
						fmt.Fprintf(w, "  %-55s %-22s %-8s basis %14s kr   utg %12s kr   inng %12s kr\n",
							b.VATType, b.Regime, code, b.Basis, b.OutputVAT, b.InputVAT)
					}
				}
				printBuckets("Sales lines", report.Output)
				fmt.Fprintln(w)
				printBuckets("Purchase lines", report.Input)
				if len(report.Findings) > 0 {
					ore := func(f Finding, key string) string {
						v, _ := f.Detail[key].(int64)
						return kr(v)
					}
					fmt.Fprintf(w, "\nJournal cross-check (%d):\n", len(report.Findings))
					for _, f := range report.Findings {
						fmt.Fprintf(w, "  %-55s kode %-4v computed utg %12s kr   journal %12s kr   diff %12s kr (%v transaction(s))\n",
							f.VATType, f.Detail["mva_code"],
							ore(f, "bucket_output_vat_ore"), ore(f, "journal_vat_ore"),
							kr(f.ImpactOre), f.Detail["transactions_matched"])
					}
				}
			})
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&flagPeriod, "period", "", "Period: YYYY, YYYY-MM, YYYY-Qn, from:to, or all (default: current year)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	return cmd
}

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
	Company          string      `json:"company"`
	Window           Window      `json:"window"`
	UndatedDocuments int         `json:"undated_documents"`
	Output           []mvaBucket `json:"output"`
	Input            []mvaBucket `json:"input"`
	TotalOutputOre   int64       `json:"total_output_vat_ore"`
	TotalOutput      string      `json:"total_output_vat"`
	TotalInputOre    int64       `json:"total_input_vat_ore"`
	TotalInput       string      `json:"total_input_vat"`
	NetVATOre        int64       `json:"net_vat_ore"`
	NetVAT           string      `json:"net_vat"`
}

// mvaLine is the minimal per-line shape the summarizer needs.
type mvaLine struct {
	VATType  string
	BasisOre int64 // netPrice
	VATOre   int64 // vat
}

// summarizeMVA buckets one side's lines by vatType and returns the sorted
// buckets plus the output and input VAT they total to. side is the document
// side the lines came from ("sales" or "purchases"): the five both-sided types
// cannot say from the taxonomy alone whether their VAT is output or input, so
// the document answers that. A vatType the authority does not know gets its
// own bucket with regime "unknown" and the line's raw vat on the document's
// side — visible rather than silently dropped. Pure; no Cobra/DB.
func summarizeMVA(lines []mvaLine, side string) (buckets []mvaBucket, outputVATOre, inputVATOre int64) {
	type agg struct {
		regime            string
		code              int
		basis, output, in int64
	}
	by := map[string]*agg{}
	for _, ln := range lines {
		info, known := fikencore.Lookup(ln.VATType)
		regime := "unknown"
		code := 0
		if known {
			regime = info.Regime.String()
			if c, ok := fikencore.CodeFor(ln.VATType, side); ok {
				code = c
			} else {
				code = info.Code
			}
		}
		if info.Side == fikencore.SideBoth || info.Side == "" {
			info.Side = side
		}
		basis, output, input := fikencore.ReturnFigures(info, ln.BasisOre, ln.VATOre)
		a := by[ln.VATType]
		if a == nil {
			a = &agg{regime: regime, code: code}
			by[ln.VATType] = a
		}
		a.basis += basis
		a.output += output
		a.in += input
		outputVATOre += output
		inputVATOre += input
	}
	buckets = make([]mvaBucket, 0, len(by))
	for vt, a := range by {
		buckets = append(buckets, mvaBucket{
			VATType:      vt,
			Regime:       a.regime,
			MVACode:      a.code,
			BasisOre:     a.basis,
			Basis:        kr(a.basis),
			OutputVATOre: a.output,
			OutputVAT:    kr(a.output),
			InputVATOre:  a.in,
			InputVAT:     kr(a.in),
		})
	}
	sort.SliceStable(buckets, func(i, j int) bool { return buckets[i].VATType < buckets[j].VATType })
	return buckets, outputVATOre, inputVATOre
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
					for _, ln := range jsonObjects(d, "lines") {
						net, _ := jsonInt(ln, "netPrice")
						vat, _ := jsonInt(ln, "vat")
						out = append(out, mvaLine{
							VATType:  jsonStr(ln, "vatType"),
							BasisOre: net,
							VATOre:   vat,
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

			// A purchase can carry output VAT (reverse charge), so the totals
			// are taken across both sides, not one per side.
			salesBuckets, salesOut, salesIn := summarizeMVA(collect(sales), fikencore.SideSales)
			purchBuckets, purchOut, purchIn := summarizeMVA(collect(purchases), fikencore.SidePurchases)
			totalOut := salesOut + purchOut
			totalIn := salesIn + purchIn
			net := totalOut - totalIn
			report := mvaSummaryReport{
				Company:          slug,
				Window:           win,
				UndatedDocuments: undated,
				Output:           salesBuckets,
				Input:            purchBuckets,
				TotalOutputOre:   totalOut,
				TotalOutput:      kr(totalOut),
				TotalInputOre:    totalIn,
				TotalInput:       kr(totalIn),
				NetVATOre:        net,
				NetVAT:           kr(net),
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
			})
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&flagPeriod, "period", "", "Period: YYYY, YYYY-MM, YYYY-Qn, from:to, or all (default: current year)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	return cmd
}

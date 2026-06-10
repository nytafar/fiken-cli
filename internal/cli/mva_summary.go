// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). Reconstructs a VAT-return-shaped view from the
// local mirror: output VAT (sales lines) and input VAT (purchase lines)
// bucketed per vatType, with net basis and VAT summed per bucket, and the net
// VAT position (output VAT − input VAT). Reads only the local mirror.
package cli

// pp:data-source local

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type mvaBucket struct {
	VATType  string `json:"vat_type"`
	BasisOre int64  `json:"basis_ore"`
	Basis    string `json:"basis"`
	VATOre   int64  `json:"vat_ore"`
	VAT      string `json:"vat"`
}

type mvaSummaryReport struct {
	Company   string      `json:"company"`
	Period    string      `json:"period,omitempty"`
	Output    []mvaBucket `json:"output"`
	Input     []mvaBucket `json:"input"`
	NetVATOre int64       `json:"net_vat_ore"`
	NetVAT    string      `json:"net_vat"`
}

// mvaLine is the minimal per-line shape the summarizer needs.
type mvaLine struct {
	VATType  string
	BasisOre int64 // netPrice
	VATOre   int64 // vat
}

// summarizeMVA buckets lines by vatType into sorted buckets and returns the
// total VAT across all buckets. Pure; no Cobra/DB.
func summarizeMVA(lines []mvaLine) (buckets []mvaBucket, totalVATOre int64) {
	type agg struct {
		basis, vat int64
	}
	by := map[string]*agg{}
	for _, ln := range lines {
		a := by[ln.VATType]
		if a == nil {
			a = &agg{}
			by[ln.VATType] = a
		}
		a.basis += ln.BasisOre
		a.vat += ln.VATOre
		totalVATOre += ln.VATOre
	}
	buckets = make([]mvaBucket, 0, len(by))
	for vt, a := range by {
		buckets = append(buckets, mvaBucket{
			VATType:  vt,
			BasisOre: a.basis,
			Basis:    kr(a.basis),
			VATOre:   a.vat,
			VAT:      kr(a.vat),
		})
	}
	sort.SliceStable(buckets, func(i, j int) bool { return buckets[i].VATType < buckets[j].VATType })
	return buckets, totalVATOre
}

func newNovelMvaSummaryCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var flagPeriod string
	var dbPath string

	cmd := &cobra.Command{
		Use:   "mva-summary",
		Short: "Summarize output/input VAT per VAT type (a VAT-return-shaped view)",
		Long: "Buckets sales lines (output VAT) and purchase lines (input VAT) by VAT type, summing the\n" +
			"net basis and VAT in each bucket, and reports the net VAT position (output − input).\n" +
			"Pass --period to scope to a term; empty summarizes everything. Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli mva-summary --company fiken-demo --period 2026-Q1
  fiken-cli mva-summary --company fiken-demo --agent`, "\n"),
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
				if from == "" && to == "" {
					return true
				}
				if date == "" {
					return false
				}
				if from != "" && date < from {
					return false
				}
				if to != "" && date > to {
					return false
				}
				return true
			}
			collect := func(docs []map[string]json.RawMessage) []mvaLine {
				var out []mvaLine
				for _, d := range docs {
					if !inPeriod(jsonStr(d, "date")) {
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

			outBuckets, outTotal := summarizeMVA(collect(sales))
			inBuckets, inTotal := summarizeMVA(collect(purchases))
			net := outTotal - inTotal
			report := mvaSummaryReport{
				Company:   slug,
				Period:    flagPeriod,
				Output:    outBuckets,
				Input:     inBuckets,
				NetVATOre: net,
				NetVAT:    kr(net),
			}
			return emitFiken(cmd, flags, report, func() {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "MVA summary for %s", report.Company)
				if report.Period != "" {
					fmt.Fprintf(w, " (%s)", report.Period)
				}
				fmt.Fprintf(w, "\n\n")
				printBuckets := func(title string, bs []mvaBucket) {
					fmt.Fprintf(w, "%s:\n", title)
					if len(bs) == 0 {
						fmt.Fprintln(w, "  (none)")
						return
					}
					for _, b := range bs {
						fmt.Fprintf(w, "  %-10s basis %14s kr   vat %14s kr\n", b.VATType, b.Basis, b.VAT)
					}
				}
				printBuckets("Output VAT (sales)", report.Output)
				printBuckets("Input VAT (purchases)", report.Input)
				fmt.Fprintf(w, "\nNet VAT (output − input): %s kr\n", report.NetVAT)
			})
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&flagPeriod, "period", "", "VAT term to summarize: YYYY, YYYY-MM, YYYY-Qn, or from:to (default: all)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	return cmd
}

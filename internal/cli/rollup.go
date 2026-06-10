// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). Grouped sales/cost/margin rollups over the local
// mirror. Group by month (date[:7]), expense/revenue account, or counterpart
// contact; report sales, purchases, or margin (= sales − purchases) per group.
// Project grouping is advertised but the GET data carries no per-line project,
// so --by project is rejected with an actionable error. Reads only the local
// mirror.
package cli

// pp:data-source local

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type rollupRow struct {
	Key          string `json:"key"`
	Label        string `json:"label"`
	SalesOre     int64  `json:"sales_ore"`
	Sales        string `json:"sales"`
	PurchasesOre int64  `json:"purchases_ore"`
	Purchases    string `json:"purchases"`
	MarginOre    int64  `json:"margin_ore"`
	Margin       string `json:"margin"`
}

type rollupReport struct {
	Company string      `json:"company"`
	By      string      `json:"by"`
	Metric  string      `json:"metric"`
	Period  string      `json:"period,omitempty"`
	Rows    []rollupRow `json:"rows"`
}

// rollupContribution is one (key,label,sales,purchases) contribution emitted by
// the data-extraction step. The aggregator sums these into rows.
type rollupContribution struct {
	Key          string
	Label        string
	SalesOre     int64
	PurchasesOre int64
}

// aggregateRollup folds contributions into one row per key (sorted by key) and
// computes margin = sales − purchases. Pure; no Cobra/DB.
func aggregateRollup(contribs []rollupContribution) []rollupRow {
	type agg struct {
		label              string
		salesOre, purchOre int64
	}
	by := map[string]*agg{}
	order := []string{}
	for _, c := range contribs {
		a := by[c.Key]
		if a == nil {
			a = &agg{label: c.Label}
			by[c.Key] = a
			order = append(order, c.Key)
		}
		if a.label == "" {
			a.label = c.Label
		}
		a.salesOre += c.SalesOre
		a.purchOre += c.PurchasesOre
	}
	sort.Strings(order)
	rows := make([]rollupRow, 0, len(order))
	for _, k := range order {
		a := by[k]
		margin := a.salesOre - a.purchOre
		rows = append(rows, rollupRow{
			Key:          k,
			Label:        a.label,
			SalesOre:     a.salesOre,
			Sales:        kr(a.salesOre),
			PurchasesOre: a.purchOre,
			Purchases:    kr(a.purchOre),
			MarginOre:    margin,
			Margin:       kr(margin),
		})
	}
	return rows
}

func newNovelRollupCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var flagPeriod string
	var flagBy string
	var flagMetric string
	var dbPath string

	cmd := &cobra.Command{
		Use:   "rollup",
		Short: "Group sales/purchases/margin by month, account, or contact",
		Long: "Groups sales and purchases over a period by the chosen dimension and reports the chosen\n" +
			"metric per group. --by: month (date[:7]) | account (line account) | contact (counterpart).\n" +
			"--metric: sales | purchases | margin (= sales − purchases). Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli rollup --company fiken-demo --by month --metric margin --period 2026
  fiken-cli rollup --company fiken-demo --by account --metric purchases --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			from, to, err := parsePeriod(flagPeriod)
			if err != nil {
				return err
			}
			by := strings.ToLower(strings.TrimSpace(flagBy))
			switch by {
			case "month", "account", "contact":
				// ok
			case "project":
				return fmt.Errorf("--by project is not supported: the mirror's sales/purchase lines carry no project; use month, account, or contact")
			default:
				return fmt.Errorf("--by must be one of month|account|contact, got %q", flagBy)
			}
			metric := strings.ToLower(strings.TrimSpace(flagMetric))
			switch metric {
			case "sales", "purchases", "margin":
				// ok
			default:
				return fmt.Errorf("--metric must be one of sales|purchases|margin, got %q", flagMetric)
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
			contactOf := func(m map[string]json.RawMessage, key string) (string, string) {
				if raw, ok := m[key]; ok {
					var ent map[string]json.RawMessage
					if json.Unmarshal(raw, &ent) == nil {
						id, _ := jsonInt(ent, "contactId")
						return fmt.Sprintf("%d", id), jsonStr(ent, "name")
					}
				}
				return "0", ""
			}

			wantSales := metric == "sales" || metric == "margin"
			wantPurch := metric == "purchases" || metric == "margin"

			var contribs []rollupContribution
			if wantSales {
				sales, err := loadCompanyResources(cmd.Context(), db, "sales", slug)
				if err != nil {
					return err
				}
				for _, s := range sales {
					date := jsonStr(s, "date")
					if !inPeriod(date) {
						continue
					}
					switch by {
					case "month":
						key := monthKey(date)
						net, _ := jsonInt(s, "netAmount")
						vat, _ := jsonInt(s, "vatAmount")
						contribs = append(contribs, rollupContribution{Key: key, Label: key, SalesOre: net + vat})
					case "contact":
						key, name := contactOf(s, "customer")
						net, _ := jsonInt(s, "netAmount")
						vat, _ := jsonInt(s, "vatAmount")
						contribs = append(contribs, rollupContribution{Key: key, Label: name, SalesOre: net + vat})
					case "account":
						for _, ln := range jsonObjects(s, "lines") {
							acct := jsonStr(ln, "account")
							net, _ := jsonInt(ln, "netPrice")
							lvat, _ := jsonInt(ln, "vat")
							contribs = append(contribs, rollupContribution{Key: acct, Label: acct, SalesOre: net + lvat})
						}
					}
				}
			}
			if wantPurch {
				purchases, err := loadCompanyResources(cmd.Context(), db, "purchases", slug)
				if err != nil {
					return err
				}
				for _, p := range purchases {
					date := jsonStr(p, "date")
					if !inPeriod(date) {
						continue
					}
					switch by {
					case "month":
						key := monthKey(date)
						contribs = append(contribs, rollupContribution{Key: key, Label: key, PurchasesOre: purchaseGross(p)})
					case "contact":
						key, name := contactOf(p, "supplier")
						contribs = append(contribs, rollupContribution{Key: key, Label: name, PurchasesOre: purchaseGross(p)})
					case "account":
						for _, ln := range jsonObjects(p, "lines") {
							acct := jsonStr(ln, "account")
							net, _ := jsonInt(ln, "netPrice")
							lvat, _ := jsonInt(ln, "vat")
							contribs = append(contribs, rollupContribution{Key: acct, Label: acct, PurchasesOre: net + lvat})
						}
					}
				}
			}

			report := rollupReport{
				Company: slug,
				By:      by,
				Metric:  metric,
				Period:  flagPeriod,
				Rows:    aggregateRollup(contribs),
			}
			return emitFiken(cmd, flags, report, func() {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "Rollup of %s by %s for %s", report.Metric, report.By, report.Company)
				if report.Period != "" {
					fmt.Fprintf(w, " (%s)", report.Period)
				}
				fmt.Fprintf(w, "\n\n")
				if len(report.Rows) == 0 {
					fmt.Fprintln(w, "No data in range.")
					return
				}
				for _, r := range report.Rows {
					label := r.Label
					if label == "" {
						label = r.Key
					}
					var val string
					switch report.Metric {
					case "sales":
						val = r.Sales
					case "purchases":
						val = r.Purchases
					default:
						val = r.Margin
					}
					fmt.Fprintf(w, "  %-24s %16s kr\n", label, val)
				}
			})
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&flagPeriod, "period", "", "Period to roll up: YYYY, YYYY-MM, YYYY-Qn, or from:to (default: all)")
	cmd.Flags().StringVar(&flagBy, "by", "month", "Grouping dimension: month|account|contact")
	cmd.Flags().StringVar(&flagMetric, "metric", "margin", "Metric to report: sales|purchases|margin")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	return cmd
}

// monthKey reduces a YYYY-MM-DD date to its YYYY-MM month bucket. Short/empty
// input is returned as-is so malformed rows still group somewhere stable.
func monthKey(date string) string {
	if len(date) >= 7 {
		return date[:7]
	}
	return date
}

// purchaseGross sums a purchase's line gross (netPrice + vat) — purchases carry
// no reliable header total, so this derives it from the lines.
func purchaseGross(p map[string]json.RawMessage) int64 {
	var g int64
	for _, ln := range jsonObjects(p, "lines") {
		net, _ := jsonInt(ln, "netPrice")
		vat, _ := jsonInt(ln, "vat")
		g += net + vat
	}
	return g
}

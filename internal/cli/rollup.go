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

	"fiken-cli/internal/fikencore"
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
	Company string `json:"company"`
	By      string `json:"by"`
	Metric  string `json:"metric"`
	Window  Window `json:"window"`
	// Totals are the whole (post-window) set, so the text view can lead with
	// the answer instead of making the reader add the rows up.
	TotalSalesOre     int64       `json:"total_sales_ore"`
	TotalSales        string      `json:"total_sales"`
	TotalPurchasesOre int64       `json:"total_purchases_ore"`
	TotalPurchases    string      `json:"total_purchases"`
	TotalMarginOre    int64       `json:"total_margin_ore"`
	TotalMargin       string      `json:"total_margin"`
	UndatedDocuments  int         `json:"undated_documents"`
	Rows              []rollupRow `json:"rows"`
}

// rollupTotals sums the rows into the report-level totals. Margin is derived
// from the summed sides rather than by summing per-row margins so it stays
// consistent with them even if a row is ever added without one. Pure.
func rollupTotals(rows []rollupRow) (salesOre, purchasesOre, marginOre int64) {
	for _, r := range rows {
		salesOre += r.SalesOre
		purchasesOre += r.PurchasesOre
	}
	return salesOre, purchasesOre, salesOre - purchasesOre
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
			"--metric: sales | purchases | margin (= sales − purchases). --period defaults to the\n" +
			"current year; pass `all` to roll up everything. Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli rollup --company fiken-demo --by month --metric margin --period 2026
  fiken-cli rollup --company fiken-demo --by account --metric purchases --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			win, err := resolvePeriod(flagPeriod)
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

			// A document with no date cannot be placed in any window, so it is
			// excluded always and counted instead of silently vanishing.
			undated := 0
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
					if date == "" {
						undated++
						continue
					}
					if !win.Contains(date) {
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
					if date == "" {
						undated++
						continue
					}
					if !win.Contains(date) {
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

			rows := aggregateRollup(contribs)
			totalSales, totalPurch, totalMargin := rollupTotals(rows)
			report := rollupReport{
				Company:           slug,
				By:                by,
				Metric:            metric,
				Window:            win,
				TotalSalesOre:     totalSales,
				TotalSales:        kr(totalSales),
				TotalPurchasesOre: totalPurch,
				TotalPurchases:    kr(totalPurch),
				TotalMarginOre:    totalMargin,
				TotalMargin:       kr(totalMargin),
				UndatedDocuments:  undated,
				Rows:              rows,
			}
			return emitFiken(cmd, flags, report, func() {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "Rollup of %s by %s for %s (%s)\n", report.Metric, report.By, report.Company, report.Window.String())
				fmt.Fprintf(w, "sales %s kr, purchases %s kr, margin %s kr — %d group(s)",
					report.TotalSales, report.TotalPurchases, report.TotalMargin, len(report.Rows))
				if report.UndatedDocuments > 0 {
					fmt.Fprintf(w, ", %d undated document(s) skipped", report.UndatedDocuments)
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
	cmd.Flags().StringVar(&flagPeriod, "period", "", "Period: YYYY, YYYY-MM, YYYY-Qn, from:to, or all (default: current year)")
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

// purchaseGross sums a purchase's line gross — purchases carry no reliable
// header total, so this derives it from the lines. What "gross" means depends
// on the line's VAT regime (net+vat only for ordinary domestic VAT), so the
// regime authority answers it; an unrecognised vatType falls back to net+vat.
func purchaseGross(p map[string]json.RawMessage) int64 {
	var g int64
	for _, ln := range jsonObjects(p, "lines") {
		net, _ := jsonInt(ln, "netPrice")
		vat, _ := jsonInt(ln, "vat")
		info, _ := fikencore.Lookup(jsonStr(ln, "vatType"))
		g += fikencore.Gross(info, net, vat)
	}
	return g
}

// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// recurring-gaps: suppliers booked month after month, then not (issue #31).
// The usual gap before a VAT return is a card subscription nobody booked.
// missing-bilag and bank-unverified cannot report it, because the purchase
// does not exist yet; a pivot of purchases per supplier per month shows it at
// a glance, and this is that pivot as a detector.
//
// A series is one (supplier, account) pair. For each month inside the period
// it asks: was this pair booked in at least --min-months of the --lookback
// months before it, and is there nothing in the month itself? That is a gap.
// When the last booking lies more than --ended-after months before the month,
// the series is reported as ended instead: a subscription that stopped is not
// a missing purchase. An unused inbox document that names the supplier and
// arrived in the gap month is noted on the finding.
//
// It is a hint for where to look in Superføring, not proof of a charge: the
// bank line decides. Reads only the local mirror.
package cli

// pp:data-source local

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"fiken-cli/internal/fikencore"
)

const (
	gapKindMissing = "recurring_gap"
	gapKindEnded   = "recurring_ended"
)

// recurringParams are the detector's knobs.
type recurringParams struct {
	MinMonths  int // booked in at least this many…
	Lookback   int // …of this many months before the month checked
	EndedAfter int // last booking more than this many months back: ended, not a gap
}

// seriesKey identifies one recurring series.
type seriesKey struct {
	contactID int64
	account   string
}

// seriesBooking is one purchase's share of a series: its date and the gross
// of its lines on the series' account.
type seriesBooking struct {
	date     string
	grossOre int64
}

// monthOf returns the YYYY-MM of a YYYY-MM-DD date.
func monthOf(date string) string {
	if len(date) < 7 {
		return ""
	}
	return date[:7]
}

// addMonths shifts a YYYY-MM month by n months.
func addMonths(month string, n int) string {
	t, err := time.Parse("2006-01", month)
	if err != nil {
		return ""
	}
	return t.AddDate(0, n, 0).Format("2006-01")
}

// monthsBetween is how many months `to` lies after `from` (both YYYY-MM).
func monthsBetween(from, to string) int {
	a, err1 := time.Parse("2006-01", from)
	b, err2 := time.Parse("2006-01", to)
	if err1 != nil || err2 != nil {
		return 0
	}
	return (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
}

// monthsInWindow lists the months a window covers, capped at the month of
// `today`: a month that has not come cannot be missing a booking.
func monthsInWindow(win Window, today string) []string {
	from, to := monthOf(win.From), monthOf(win.To)
	if cap := monthOf(today); to == "" || to > cap {
		to = cap
	}
	if from == "" {
		from = to
	}
	var out []string
	for m := from; m != "" && m <= to; m = addMonths(m, 1) {
		out = append(out, m)
	}
	return out
}

// medianOre is the usual amount of a series: the median resists the one month
// a subscription was upgraded or refunded.
func medianOre(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// supplierNameTokens are the words of a supplier name long enough to identify
// it in an inbox document's name; company-form suffixes and short words say
// nothing about who sent a receipt.
func supplierNameTokens(name string) []string {
	skip := map[string]bool{"as": true, "asa": true, "ltd": true, "inc": true, "llc": true, "gmbh": true, "ab": true, "aps": true,
		"norge": true, "norway": true, "the": true, "and": true}
	var out []string
	for _, tok := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == 'æ' || r == 'ø' || r == 'å')
	}) {
		if len(tok) >= 4 && !skip[tok] {
			out = append(out, tok)
		}
	}
	return out
}

// inboxHint is an unused inbox document that may be the missing receipt.
type inboxHint struct {
	DocumentID int64  `json:"document_id"`
	Name       string `json:"name,omitempty"`
	Filename   string `json:"filename,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

// unusedInboxFor returns the unused inbox documents created in `month` whose
// name, filename or description carries a word of the supplier's name.
func unusedInboxFor(inbox []map[string]json.RawMessage, supplier, month string) []inboxHint {
	tokens := supplierNameTokens(supplier)
	if len(tokens) == 0 {
		return nil
	}
	var out []inboxHint
	for _, d := range inbox {
		if jsonBoolField(d, "status") || monthOf(jsonStr(d, "createdAt")) != month {
			continue
		}
		text := strings.ToLower(jsonStr(d, "name") + " " + jsonStr(d, "filename") + " " + jsonStr(d, "description"))
		for _, tok := range tokens {
			if strings.Contains(text, tok) {
				id, _ := jsonInt(d, "documentId")
				out = append(out, inboxHint{DocumentID: id, Name: jsonStr(d, "name"), Filename: jsonStr(d, "filename"), CreatedAt: jsonStr(d, "createdAt")})
				break
			}
		}
	}
	return out
}

// recurringGapFindings is the pure core. months are the months to check.
// Pure; no Cobra/DB.
func recurringGapFindings(purchases, inbox []map[string]json.RawMessage, months []string, p recurringParams) (findings []Finding, undated int) {
	series := map[seriesKey][]seriesBooking{}
	names := map[int64]string{}
	for _, pur := range purchases {
		if jsonBoolField(pur, "deleted") {
			continue
		}
		sup := jsonObject(pur, "supplier")
		contactID, _ := jsonInt(sup, "contactId")
		if contactID == 0 {
			continue // a cash purchase has no supplier to form a series
		}
		date := jsonStr(pur, "date")
		if monthOf(date) == "" {
			undated++
			continue
		}
		names[contactID] = jsonStr(sup, "name")
		perAccount := map[string]int64{}
		for _, ln := range jsonObjects(pur, "lines") {
			net, _ := jsonInt(ln, "netPrice")
			vat, _ := jsonInt(ln, "vat")
			info, _ := fikencore.Lookup(jsonStr(ln, "vatType"))
			perAccount[jsonStr(ln, "account")] += fikencore.Gross(info, net, vat)
		}
		for account, gross := range perAccount {
			if account == "" {
				continue
			}
			key := seriesKey{contactID: contactID, account: account}
			series[key] = append(series[key], seriesBooking{date: date, grossOre: gross})
		}
	}

	for key, bookings := range series {
		booked := map[string]bool{}
		for _, b := range bookings {
			booked[monthOf(b.date)] = true
		}
		for _, month := range months {
			if booked[month] {
				continue
			}
			from := addMonths(month, -p.Lookback)
			monthsBooked := map[string]bool{}
			var amounts []int64
			last := ""
			for _, b := range bookings {
				m := monthOf(b.date)
				if m >= month {
					continue
				}
				if b.date > last {
					last = b.date
				}
				if m >= from {
					monthsBooked[m] = true
					amounts = append(amounts, b.grossOre)
				}
			}
			if len(monthsBooked) < p.MinMonths {
				continue
			}
			since := monthsBetween(monthOf(last), month)
			f := Finding{
				Kind:        gapKindMissing,
				Severity:    SeverityWarning,
				DocType:     "supplier",
				DocID:       key.contactID,
				Date:        month + "-01",
				ContactID:   key.contactID,
				ContactName: names[key.contactID],
				Account:     key.account,
				ImpactOre:   medianOre(amounts),
				Detail: map[string]any{
					"month":             month,
					"last_booking_date": last,
					"months_booked":     len(monthsBooked),
					"lookback_months":   p.Lookback,
					"months_since_last": since,
				},
			}
			f.Description = fmt.Sprintf("no purchase in %s; booked %d of the %d months before, last on %s", month, len(monthsBooked), p.Lookback, last)
			if since > p.EndedAfter {
				f.Kind, f.Severity = gapKindEnded, SeverityInfo
				f.Description = fmt.Sprintf("series ended: last booked %s, %d months before %s", last, since, month)
			} else if hints := unusedInboxFor(inbox, names[key.contactID], month); len(hints) > 0 {
				f.Detail["unused_inbox_documents"] = hints
				f.Detail["note"] = fmt.Sprintf("%d unused inbox document(s) from this supplier arrived in %s", len(hints), month)
			}
			findings = append(findings, f)
		}
	}
	return findings, undated
}

func newNovelRecurringGapsCmd(flags *rootFlags) *cobra.Command {
	var flagCompany, dbPath string
	var opts reportOpts
	params := recurringParams{}

	cmd := &cobra.Command{
		Use:   "recurring-gaps",
		Short: "List suppliers booked every month that have a month with nothing",
		Long: "Finds the purchases that are probably missing: for each supplier and account booked in at\n" +
			"least --min-months of the --lookback months before a month, reports the months in the period\n" +
			"with no purchase, with the usual amount (the median) and the date of the last booking.\n" +
			"A series whose last booking lies more than --ended-after months back is reported as\n" +
			"recurring_ended rather than as a gap. An unused inbox document naming the supplier in the\n" +
			"gap month is noted on the finding.\n\n" +
			"A hint for where to look in Superføring, not proof of a charge: the bank line decides.\n" +
			"Months after the current one are not checked. Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli recurring-gaps --company fiken-demo --period 2026-07-01:2026-08-31
  fiken-cli recurring-gaps --company fiken-demo --period 2026-Q3 --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			win, err := resolvePeriod(opts.period)
			if err != nil {
				return err
			}
			if params.MinMonths < 1 || params.Lookback < params.MinMonths {
				return usageErr(fmt.Errorf("--min-months must be at least 1 and no more than --lookback (got %d of %d)", params.MinMonths, params.Lookback))
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
			purchases, err := loadCompanyResources(cmd.Context(), db, "purchases", slug)
			if err != nil {
				return err
			}
			inbox, err := loadCompanyResources(cmd.Context(), db, "inbox", slug)
			if err != nil {
				return err
			}

			months := monthsInWindow(win, nowFunc().Format("2006-01-02"))
			findings, undated := recurringGapFindings(purchases, inbox, months, params)
			report := Report{
				Company:  slug,
				Detector: "recurring_gaps",
				Window:   win,
				Undated:  undated,
				Params: map[string]any{
					"min_months":     params.MinMonths,
					"lookback":       params.Lookback,
					"ended_after":    params.EndedAfter,
					"months_checked": len(months),
				},
			}
			keyFn := func(f Finding) map[string]string {
				month, _ := f.Detail["month"].(string)
				return map[string]string{"kind": f.Kind, "month": month}
			}
			return finishReport(cmd, flags, report, findings, keyFn, opts)
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	cmd.Flags().IntVar(&params.MinMonths, "min-months", 3, "A series is recurring when booked in at least this many of the lookback months")
	cmd.Flags().IntVar(&params.Lookback, "lookback", 6, "How many months before each checked month to look for the series")
	cmd.Flags().IntVar(&params.EndedAfter, "ended-after", 3, "Report a series as ended, not a gap, when its last booking is more than this many months back")
	addReportFlags(cmd, &opts)
	return cmd
}

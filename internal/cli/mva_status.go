// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// mva-status: which VAT terms are delivered, paid, open or overdue (issue
// #30). It is the first question before a return and used to take hand-written
// SQL over the journal.
//
// The answer is read off the structure of the entries Fiken posts with
// transactionType `Mva-oppgjør`, not off their description text:
//
//   - a DELIVERY empties the VAT accounts (2700-2719) onto the settlement
//     account 2740 and is dated on the term's last day, so it is the
//     settlement entry that touches a VAT account, and its term is the one
//     its date falls in. A delivery that was annulled leaves a second entry
//     reversing the first; the two net to nothing on 2740 and the term reads
//     as not delivered.
//   - a PAYMENT moves 2740 against a bank account and touches no VAT account.
//     Its date says nothing about its term, so the term is the one whose label
//     ("jul-aug 2026") the description carries. A payment with no readable
//     label settles the oldest delivered term that still has a balance.
//
// A term is `paid` when it is delivered and its share of 2740 is back to zero
// or below (a return with nothing to pay is paid on delivery), `delivered` when it is
// delivered with a balance and not yet due, `overdue` when its due date has
// passed and it is undelivered or unpaid, and `open` otherwise. A term that
// ended before the first mirrored journal entry is not listed. Due dates
// follow skatteforvaltningsforskriften § 8-3-10: one month and ten days after
// the term ends, 31 August for the third bi-monthly term, 10 March for a
// yearly term, moved to the next working day off a weekend or public holiday.
// Reads only the local mirror.
package cli

// pp:data-source local

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"fiken-cli/internal/store"
)

const (
	mvaStatePaid      = "paid"
	mvaStateDelivered = "delivered"
	mvaStateOpen      = "open"
	mvaStateOverdue   = "overdue"
)

// vatSettlementAccount is where a delivered term's balance sits until paid.
const vatSettlementAccount = 2740

// mvaStatusToleranceOre: a term or the settlement account counts as settled
// within a krone, the unit the return is filed and paid in.
const mvaStatusToleranceOre = 100

// vatTerm is one reporting period of a company.
type vatTerm struct {
	Label string // "jul-aug 2026", "2026", "juli 2026"
	From  string
	To    string
	Due   string
}

var biMonthlyLabels = []string{"jan-feb", "mar-apr", "mai-jun", "jul-aug", "sep-okt", "nov-des"}

var monthLabels = []string{"januar", "februar", "mars", "april", "mai", "juni", "juli", "august", "september", "oktober", "november", "desember"}

// easterSunday is the anonymous Gregorian computus.
func easterSunday(year int) time.Time {
	a := year % 19
	b := year / 100
	c := year % 100
	d := b / 4
	e := b % 4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i := c / 4
	k := c % 4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// isNorwegianHoliday reports whether d is a public holiday (helligdag or
// lovfestet høytidsdag): the days a deadline does not fall on.
func isNorwegianHoliday(d time.Time) bool {
	switch {
	case d.Month() == time.January && d.Day() == 1,
		d.Month() == time.May && (d.Day() == 1 || d.Day() == 17),
		d.Month() == time.December && (d.Day() == 25 || d.Day() == 26):
		return true
	}
	easter := easterSunday(d.Year())
	for _, offset := range []int{-3, -2, 0, 1, 39, 49, 50} { // skjærtorsdag, langfredag, 1. og 2. påskedag, Kristi himmelfart, 1. og 2. pinsedag
		if d.Equal(easter.AddDate(0, 0, offset)) {
			return true
		}
	}
	return false
}

// nextWorkingDay moves a deadline off a Saturday, Sunday or public holiday.
func nextWorkingDay(d time.Time) time.Time {
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday || isNorwegianHoliday(d) {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// vatTerms lists a year's terms for a company's vatType (`bi-monthly`,
// `monthly`, `yearly`). Any other value, including `no` for a company that is
// not VAT-registered, has no terms.
func vatTerms(vatType string, year int) []vatTerm {
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	term := func(label string, from, to, due time.Time) vatTerm {
		return vatTerm{Label: label, From: from.Format("2006-01-02"), To: to.Format("2006-01-02"),
			Due: nextWorkingDay(due).Format("2006-01-02")}
	}
	switch strings.ToLower(strings.TrimSpace(vatType)) {
	case "bi-monthly":
		var out []vatTerm
		for i, label := range biMonthlyLabels {
			first := time.Month(2*i + 1)
			from := day(year, first, 1)
			to := day(year, first+2, 0)
			due := day(year, first+3, 10) // one month and ten days after the term ends
			if i == 2 {
				due = day(year, time.August, 31)
			}
			out = append(out, term(fmt.Sprintf("%s %d", label, year), from, to, due))
		}
		return out
	case "monthly":
		var out []vatTerm
		for i, label := range monthLabels {
			m := time.Month(i + 1)
			out = append(out, term(fmt.Sprintf("%s %d", label, year), day(year, m, 1), day(year, m+1, 0), day(year, m+2, 10)))
		}
		return out
	case "yearly":
		return []vatTerm{term(fmt.Sprintf("%d", year), day(year, 1, 1), day(year, 12, 31), day(year+1, time.March, 10))}
	}
	return nil
}

// mvaTermStatus is one row of the report. Amounts are øre, positive when the
// company owes; a negative delivered amount is a refund due.
type mvaTermStatus struct {
	Term          string `json:"term"`
	From          string `json:"from"`
	To            string `json:"to"`
	DueDate       string `json:"due_date"`
	State         string `json:"state"`
	OverdueReason string `json:"overdue_reason,omitempty"` // not_delivered | not_paid
	PeriodEnded   bool   `json:"period_ended"`
	Closed        bool   `json:"closed"` // delivered: a posting dated here needs a corrected return

	DeliveredDate      string `json:"delivered_date,omitempty"`
	DeliveredAmountOre *int64 `json:"delivered_amount_ore,omitempty"`
	DeliveredAmount    string `json:"delivered_amount,omitempty"`
	PaidDate           string `json:"paid_date,omitempty"`
	PaidAmountOre      *int64 `json:"paid_amount_ore,omitempty"`
	PaidAmount         string `json:"paid_amount,omitempty"`
	OutstandingOre     int64  `json:"outstanding_ore"`
	Outstanding        string `json:"outstanding"`

	// LedgerVATOre is what sits on the VAT accounts for the term's dates:
	// the amount an undelivered term will be filed with, and for a delivered
	// term whatever was posted after the delivery emptied them.
	LedgerVATOre int64  `json:"ledger_vat_ore"`
	LedgerVAT    string `json:"ledger_vat"`
}

type mvaStatusReport struct {
	Company  string          `json:"company"`
	VATType  string          `json:"vat_type"`
	Year     int             `json:"year"`
	AsOf     string          `json:"as_of"`
	Terms    []mvaTermStatus `json:"terms"`
	Open     []string        `json:"open_terms"`
	Overdue  []string        `json:"overdue_terms"`
	Date     *mvaDateStatus  `json:"date,omitempty"`
	Account  mvaSettlement   `json:"settlement_account"`
	Findings []Finding       `json:"findings"`
}

// mvaDateStatus answers --date: which term a date falls in and whether that
// term is closed, which is what the purchase-date rule needs.
type mvaDateStatus struct {
	Date   string `json:"date"`
	Term   string `json:"term"`
	State  string `json:"state"`
	Closed bool   `json:"closed"`
}

// mvaSettlement is account 2740 as a whole: its balance over every mirrored
// entry, what the listed terms explain of it, and the rest.
type mvaSettlement struct {
	Account     string `json:"account"`
	BalanceOre  int64  `json:"balance_ore"` // positive: owed to the state
	Balance     string `json:"balance"`
	ExpectedOre int64  `json:"expected_ore"` // the outstanding of the listed terms
	ResidualOre int64  `json:"residual_ore"`
	Residual    string `json:"residual"`
}

// entryVATFigures reads a journal entry's lines once: the net movement on the
// VAT accounts as net VAT owed (output − input), the signed amount on 2740,
// and whether the entry touches either.
func entryVATFigures(e map[string]json.RawMessage) (vatOwed, settlement int64, touchesVAT, touchesSettlement bool) {
	for _, ln := range jsonObjects(e, "lines") {
		amount, _ := jsonInt(ln, "amount")
		switch acct := vatAccountNumber(jsonStr(ln, "account")); {
		case acct >= vatOutputAccountFrom && acct <= vatAccountTo:
			// A credit is negative, so both output (credit) and input (debit)
			// VAT add up to net VAT owed when negated.
			vatOwed -= amount
			touchesVAT = true
		case acct == vatSettlementAccount:
			settlement += amount
			touchesSettlement = true
		}
	}
	return vatOwed, settlement, touchesVAT, touchesSettlement
}

// buildMvaStatus is the pure core: the status of every term in `terms` as of
// `today`, plus findings. Terms must be in chronological order. Pure; no
// Cobra/DB.
func buildMvaStatus(terms []vatTerm, entries []map[string]json.RawMessage, today string) ([]mvaTermStatus, mvaSettlement, []Finding) {
	type acc struct {
		deliveries    int
		deliveredOn   string
		deliveredOre  int64 // owed, from the delivery entries' 2740 lines
		paidOre       int64
		paidOn        string
		payments      int
		ledgerVATOre  int64
		settlementOre int64
	}
	accs := make([]acc, len(terms))
	termAt := func(date string) int {
		for i, t := range terms {
			if date >= t.From && date <= t.To {
				return i
			}
		}
		return -1
	}

	var balance int64
	// firstEntry is where the mirrored books begin. A term that ended before
	// it is not this company's to deliver, and is left out rather than
	// reported as overdue for ever.
	firstEntry := ""
	type payment struct {
		date, description string
		ore               int64
	}
	var unlabelled []payment
	for _, e := range uniqueJournalEntries(entries) {
		vatOwed, settlement, touchesVAT, touchesSettlement := entryVATFigures(e)
		balance -= settlement
		date := jsonStr(e, "date")
		if date != "" && (firstEntry == "" || date < firstEntry) {
			firstEntry = date
		}
		i := termAt(date)
		if i >= 0 {
			accs[i].ledgerVATOre += vatOwed
		}
		if jsonStr(e, "transactionType") != vatSettlementType {
			continue
		}
		switch {
		case touchesVAT:
			if i < 0 {
				continue
			}
			accs[i].deliveries++
			accs[i].deliveredOre -= settlement
			if date > accs[i].deliveredOn {
				accs[i].deliveredOn = date
			}
		case touchesSettlement:
			description := strings.ToLower(jsonStr(e, "description"))
			matched := -1
			for j, t := range terms {
				if strings.Contains(description, strings.ToLower(t.Label)) {
					matched = j
					break
				}
			}
			if matched < 0 {
				unlabelled = append(unlabelled, payment{date: date, description: description, ore: settlement})
				continue
			}
			accs[matched].paidOre += settlement
			accs[matched].payments++
			if date > accs[matched].paidOn {
				accs[matched].paidOn = date
			}
		}
	}

	// A delivery that was annulled: two or more entries that cancel on both
	// the settlement account and the VAT accounts leave the term undelivered.
	delivered := func(a acc) bool {
		if a.deliveries == 0 {
			return false
		}
		return !(a.deliveries > 1 && a.deliveredOre == 0 && absInt64(a.ledgerVATOre) > mvaStatusToleranceOre)
	}

	// Payments with no readable term settle the oldest delivered term that
	// still has a balance. Only payments towards the listed terms can be
	// placed; the rest stay in the settlement account's residual.
	sort.SliceStable(unlabelled, func(i, j int) bool { return unlabelled[i].date < unlabelled[j].date })
	for _, p := range unlabelled {
		for i := range accs {
			if !delivered(accs[i]) || p.date < terms[i].To {
				continue
			}
			if absInt64(accs[i].deliveredOre-accs[i].paidOre) <= mvaStatusToleranceOre {
				continue
			}
			accs[i].paidOre += p.ore
			accs[i].payments++
			if p.date > accs[i].paidOn {
				accs[i].paidOn = p.date
			}
			break
		}
	}

	findings := []Finding{}
	rows := make([]mvaTermStatus, 0, len(terms))
	var expected int64
	for i, t := range terms {
		a := accs[i]
		if firstEntry != "" && t.To < firstEntry {
			continue
		}
		row := mvaTermStatus{
			Term: t.Label, From: t.From, To: t.To, DueDate: t.Due,
			PeriodEnded:  today > t.To,
			LedgerVATOre: a.ledgerVATOre, LedgerVAT: kr(a.ledgerVATOre),
		}
		isDelivered := delivered(a)
		if isDelivered {
			owed := a.deliveredOre
			row.Closed = true
			row.DeliveredDate = a.deliveredOn
			row.DeliveredAmountOre = &owed
			row.DeliveredAmount = kr(owed)
			row.OutstandingOre = owed - a.paidOre
			if a.payments > 0 {
				paid := a.paidOre
				row.PaidDate = a.paidOn
				row.PaidAmountOre = &paid
				row.PaidAmount = kr(paid)
			}
		}
		row.Outstanding = kr(row.OutstandingOre)
		// Paid in full, or paid by more than was owed: the excess is not this
		// term's debt, and it stays visible as the settlement account's residual.
		overpaid := isDelivered && a.payments > 0 && a.deliveredOre > 0 && row.OutstandingOre < 0
		switch {
		case isDelivered && (absInt64(row.OutstandingOre) <= mvaStatusToleranceOre || overpaid):
			row.State = mvaStatePaid
		case isDelivered && today > t.Due && row.OutstandingOre > 0:
			// A refund the state has not paid out yet is not the company's
			// overdue payment, so only an amount owed can be overdue.
			row.State, row.OverdueReason = mvaStateOverdue, "not_paid"
		case isDelivered:
			row.State = mvaStateDelivered
		case today > t.Due:
			row.State, row.OverdueReason = mvaStateOverdue, "not_delivered"
		default:
			row.State = mvaStateOpen
		}
		if isDelivered && row.State != mvaStatePaid {
			expected += row.OutstandingOre
		}
		rows = append(rows, row)

		if row.State == mvaStateOverdue {
			impact := row.OutstandingOre
			if row.OverdueReason == "not_delivered" {
				impact = row.LedgerVATOre
			}
			findings = append(findings, Finding{
				Kind: "mva_term_overdue", Severity: SeverityError, DocType: "term", Date: t.Due, ImpactOre: impact,
				Description: fmt.Sprintf("%s was due %s and is %s", t.Label, t.Due, strings.ReplaceAll(row.OverdueReason, "_", " ")),
				Detail:      map[string]any{"term": t.Label, "reason": row.OverdueReason, "due_date": t.Due},
			})
		}
		if isDelivered && absInt64(a.ledgerVATOre) > mvaStatusToleranceOre {
			findings = append(findings, Finding{
				Kind: "posted_after_delivery", Severity: SeverityWarning, DocType: "term", Date: t.To, ImpactOre: a.ledgerVATOre,
				Description: fmt.Sprintf("%s is delivered, but its VAT accounts hold %s kr: something was posted in the term afterwards", t.Label, kr(a.ledgerVATOre)),
				Detail: map[string]any{"term": t.Label,
					"note": "a posting in a delivered term is not in the return that was filed; it needs a corrected return (endringsmelding)"},
			})
		}
	}

	account := mvaSettlement{
		Account:    fmt.Sprintf("%d", vatSettlementAccount),
		BalanceOre: balance, Balance: kr(balance),
		ExpectedOre: expected,
		ResidualOre: balance - expected, Residual: kr(balance - expected),
	}
	if absInt64(account.ResidualOre) > mvaStatusToleranceOre {
		findings = append(findings, Finding{
			Kind: "settlement_account_residual", Severity: SeverityWarning, DocType: "account", Account: account.Account,
			ImpactOre: account.ResidualOre,
			Description: fmt.Sprintf("account 2740 holds %s kr; the listed terms explain %s kr of it, leaving %s kr",
				account.Balance, kr(expected), account.Residual),
			Detail: map[string]any{"note": "an earlier term that is not settled, a payment booked to the wrong term, or a rounding difference never written off"},
		})
	}
	return rows, account, findings
}

// companyVATType reads the company's vatType from the mirror.
func companyVATType(db *store.Store, slug string) string {
	rows, err := db.ListFieldSets("companies", []string{"slug", "vatType"})
	if err != nil {
		return ""
	}
	for _, row := range rows {
		if strings.TrimSpace(row["slug"]) == slug {
			return strings.TrimSpace(row["vatType"])
		}
	}
	return ""
}

func newNovelMvaStatusCmd(flags *rootFlags) *cobra.Command {
	var flagCompany, dbPath, flagVATType, flagDate string
	var flagYear int

	cmd := &cobra.Command{
		Use:   "mva-status",
		Short: "Show which MVA terms are delivered, paid, open or overdue",
		Long: "Lists the company's VAT terms for a year, one row per term: period, due date (moved off\n" +
			"weekends and public holidays), when the return was delivered and for how much, when it was\n" +
			"paid, what is outstanding, what sits on the VAT accounts for the term, and a state:\n\n" +
			"  paid       delivered, and nothing is outstanding\n" +
			"  delivered  delivered, with an amount outstanding that is not yet due\n" +
			"  open       not delivered, not yet due\n" +
			"  overdue    past its due date and undelivered, or delivered and unpaid\n\n" +
			"Terms come from the company's vatType (bi-monthly, monthly, yearly). The year before is\n" +
			"read as well, and its terms are listed when they are not paid. Findings report each overdue\n" +
			"term, a delivered term with VAT posted after delivery, and a balance on account 2740 that\n" +
			"the listed terms do not explain. --date answers whether the term a date falls in is closed.\n" +
			"Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli mva-status --company fiken-demo
  fiken-cli mva-status --company fiken-demo --year 2025 --agent
  fiken-cli mva-status --company fiken-demo --date 2026-06-28 --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			now := nowFunc()
			today := now.Format("2006-01-02")
			if flagDate != "" && !validDate(flagDate) {
				return usageErr(fmt.Errorf("invalid --date %q (want YYYY-MM-DD)", flagDate))
			}
			year := flagYear
			if year == 0 {
				year = now.Year()
				if flagDate != "" {
					if t, perr := time.Parse("2006-01-02", flagDate); perr == nil {
						year = t.Year()
					}
				}
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
			vatType := flagVATType
			if vatType == "" {
				vatType = companyVATType(db, slug)
			}
			entries, err := loadCompanyResources(cmd.Context(), db, "journal_entries", slug)
			if err != nil {
				return err
			}

			// The year before is read too: its last term falls due in this
			// year, and an unpaid term must not vanish at New Year.
			terms := append(vatTerms(vatType, year-1), vatTerms(vatType, year)...)
			rows, account, findings := buildMvaStatus(terms, entries, today)
			report := mvaStatusReport{
				Company: slug, VATType: vatType, Year: year, AsOf: today,
				Terms: []mvaTermStatus{}, Open: []string{}, Overdue: []string{},
				Account: account, Findings: findings,
			}
			prefix := fmt.Sprintf("%d-", year)
			for _, row := range rows {
				if !strings.HasPrefix(row.From, prefix) && row.State == mvaStatePaid {
					continue
				}
				report.Terms = append(report.Terms, row)
				// A term that has not begun is neither open nor overdue yet.
				switch {
				case row.State == mvaStateOverdue:
					report.Overdue = append(report.Overdue, row.Term)
				case row.State == mvaStateOpen && today >= row.From:
					report.Open = append(report.Open, row.Term)
				}
			}
			if flagDate != "" {
				for _, row := range rows {
					if flagDate >= row.From && flagDate <= row.To {
						report.Date = &mvaDateStatus{Date: flagDate, Term: row.Term, State: row.State, Closed: row.Closed}
					}
				}
			}
			if len(terms) == 0 {
				report.Findings = append(report.Findings, Finding{
					Kind: "no_vat_terms", Severity: SeverityInfo, DocType: "company",
					Description: fmt.Sprintf("company vatType is %q, which has no VAT terms; pass --vat-type bi-monthly|monthly|yearly if that is wrong", vatType),
				})
			}

			return emitFiken(cmd, flags, report, func() {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "MVA status for %s, %d (%s terms, as of %s)\n\n", report.Company, report.Year, report.VATType, report.AsOf)
				fmt.Fprintf(w, "  %-14s %-22s %-11s %-10s %-11s %14s  %-11s %14s  %14s\n",
					"term", "period", "due", "state", "delivered", "amount", "paid", "outstanding", "on VAT accts")
				for _, r := range report.Terms {
					state := r.State
					fmt.Fprintf(w, "  %-14s %s..%s %-11s %-10s %-11s %14s  %-11s %14s  %14s\n",
						r.Term, r.From, r.To[5:], r.DueDate, state, r.DeliveredDate, r.DeliveredAmount, r.PaidDate, r.Outstanding, r.LedgerVAT)
				}
				if len(report.Terms) == 0 {
					fmt.Fprintln(w, "  (no terms)")
				}
				fmt.Fprintf(w, "\nOpen: %s\nOverdue: %s\n", listOrNone(report.Open), listOrNone(report.Overdue))
				fmt.Fprintf(w, "Account 2740: balance %s kr, of which the terms above explain %s kr\n", report.Account.Balance, kr(report.Account.ExpectedOre))
				if report.Date != nil {
					closed := "open: a posting dated here goes into the next return"
					if report.Date.Closed {
						closed = "CLOSED: the return is delivered"
					}
					fmt.Fprintf(w, "%s falls in %s (%s), %s\n", report.Date.Date, report.Date.Term, report.Date.State, closed)
				}
				if len(report.Findings) > 0 {
					fmt.Fprintf(w, "\nFindings (%d)\n", len(report.Findings))
					for _, f := range report.Findings {
						fmt.Fprintf(w, "  %-8s %-28s %s\n", f.Severity, f.Kind, f.Description)
					}
				}
			})
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().IntVar(&flagYear, "year", 0, "Year to list (default: the current year, or the year of --date)")
	cmd.Flags().StringVar(&flagDate, "date", "", "Also report the term this date (YYYY-MM-DD) falls in and whether it is closed")
	cmd.Flags().StringVar(&flagVATType, "vat-type", "", "Term length when the mirror's company record has none: bi-monthly, monthly, yearly")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	return cmd
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

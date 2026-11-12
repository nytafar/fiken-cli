// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — tests for mva-status (issue #30) on invented journal
// entries shaped like the ones Fiken posts for a VAT settlement.
package cli

import (
	"testing"
	"time"
)

func TestVatTerms_BiMonthlyDueDates(t *testing.T) {
	// 2026: 10 October is a Saturday, so the fourth term is due Monday 12th.
	want := []vatTerm{
		{"jan-feb 2026", "2026-01-01", "2026-02-28", "2026-04-10"},
		{"mar-apr 2026", "2026-03-01", "2026-04-30", "2026-06-10"},
		{"mai-jun 2026", "2026-05-01", "2026-06-30", "2026-08-31"},
		{"jul-aug 2026", "2026-07-01", "2026-08-31", "2026-10-12"},
		{"sep-okt 2026", "2026-09-01", "2026-10-31", "2026-12-10"},
		{"nov-des 2026", "2026-11-01", "2026-12-31", "2027-02-10"},
	}
	got := vatTerms("bi-monthly", 2026)
	if len(got) != len(want) {
		t.Fatalf("got %d terms, want 6", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("term %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestVatTerms_OtherLengthsAndHolidays(t *testing.T) {
	// 2023: 10 April was Easter Monday, so the first term was due the 11th.
	if got := vatTerms("bi-monthly", 2023)[0].Due; got != "2023-04-11" {
		t.Errorf("jan-feb 2023 due = %s, want 2023-04-11 (10 April was 2. påskedag)", got)
	}
	// A leap year ends its first term on the 29th.
	if got := vatTerms("bi-monthly", 2028)[0].To; got != "2028-02-29" {
		t.Errorf("jan-feb 2028 ends %s, want 2028-02-29", got)
	}
	yearly := vatTerms("yearly", 2026)
	if len(yearly) != 1 || yearly[0] != (vatTerm{"2026", "2026-01-01", "2026-12-31", "2027-03-10"}) {
		t.Errorf("yearly = %+v, want one term due 10 March the year after", yearly)
	}
	monthly := vatTerms("monthly", 2026)
	if len(monthly) != 12 || monthly[0] != (vatTerm{"januar 2026", "2026-01-01", "2026-01-31", "2026-03-10"}) ||
		monthly[5].Due != "2026-08-10" || monthly[11].Due != "2027-02-10" {
		t.Errorf("monthly = %+v", monthly)
	}
	// A company that is not VAT-registered has no terms.
	if got := vatTerms("no", 2026); len(got) != 0 {
		t.Errorf("vatType no = %+v, want no terms", got)
	}
}

func TestNextWorkingDay(t *testing.T) {
	day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
	tests := map[string]string{
		"2026-10-10": "2026-10-12", // Saturday
		"2026-05-17": "2026-05-18", // Sunday and 17 May
		"2026-04-02": "2026-04-07", // skjærtorsdag through 2. påskedag
		"2026-05-14": "2026-05-15", // Kristi himmelfartsdag
		"2026-12-25": "2026-12-28", // jul, then a weekend
		"2026-06-10": "2026-06-10", // an ordinary Wednesday
	}
	for in, want := range tests {
		if got := nextWorkingDay(day(in)).Format("2006-01-02"); got != want {
			t.Errorf("nextWorkingDay(%s) = %s, want %s", in, got, want)
		}
	}
}

// statusFixture: the return for mar-apr was delivered and paid, mai-jun was
// delivered and is unpaid, and nothing has been delivered since. Each term
// has one sale posting 250 kr of output VAT.
const statusFixture = `[
  {"journalEntryId": 1, "date": "2026-03-10", "transactionType": "Salg", "lines": [{"amount": -25000, "account": "2701"}, {"amount": 25000, "account": "1500:1"}]},
  {"journalEntryId": 2, "date": "2026-04-30", "transactionType": "Mva-oppgjør", "description": "Overføring etter mva-melding for mar-apr 2026",
   "lines": [{"amount": 25000, "account": "2701"}, {"amount": -25000, "account": "2740"}]},
  {"journalEntryId": 3, "date": "2026-06-08", "transactionType": "Mva-oppgjør", "description": "Betaling av merverdiavgift (mva-melding mar-apr 2026)",
   "lines": [{"amount": 25000, "account": "2740"}, {"amount": -25000, "account": "1920:1"}]},
  {"journalEntryId": 4, "date": "2026-05-12", "transactionType": "Salg", "lines": [{"amount": -40000, "account": "2701"}, {"amount": 40000, "account": "1500:1"}]},
  {"journalEntryId": 5, "date": "2026-06-30", "transactionType": "Mva-oppgjør", "description": "Overføring etter mva-melding for mai-jun 2026",
   "lines": [{"amount": 40000, "account": "2701"}, {"amount": -40000, "account": "2740"}]},
  {"journalEntryId": 6, "date": "2026-07-15", "transactionType": "Salg", "lines": [{"amount": -30000, "account": "2701"}, {"amount": 30000, "account": "1500:1"}]},
  {"journalEntryId": 7, "date": "2026-09-20", "transactionType": "Kjøp", "lines": [{"amount": 10000, "account": "2711"}, {"amount": -10000, "account": "2400:1"}]}
]`

func statusByTerm(rows []mvaTermStatus) map[string]mvaTermStatus {
	out := map[string]mvaTermStatus{}
	for _, r := range rows {
		out[r.Term] = r
	}
	return out
}

func TestBuildMvaStatus_NamesOpenAndOverdueTerms(t *testing.T) {
	// 2026-11-12: mai-jun (due 31 Aug) is delivered and unpaid, jul-aug (due
	// 12 Oct) is undelivered, sep-okt is ended but not due, nov-des is running.
	rows, account, findings := buildMvaStatus(vatTerms("bi-monthly", 2026), txRows(t, statusFixture), "2026-11-12")
	got := statusByTerm(rows)

	// The books begin on 10 March, so jan-feb is not this company's term.
	if r, listed := got["jan-feb 2026"]; listed {
		t.Errorf("jan-feb = %+v, want it left out: it ended before the first journal entry", r)
	}
	if r := got["mar-apr 2026"]; r.State != mvaStatePaid || !r.Closed || r.DeliveredDate != "2026-04-30" ||
		*r.DeliveredAmountOre != 25000 || r.PaidDate != "2026-06-08" || *r.PaidAmountOre != 25000 || r.OutstandingOre != 0 {
		t.Errorf("mar-apr = %+v, want paid: delivered 30 April for 250 kr, paid 8 June", r)
	}
	if r := got["mai-jun 2026"]; r.State != mvaStateOverdue || r.OverdueReason != "not_paid" || r.OutstandingOre != 40000 || !r.Closed {
		t.Errorf("mai-jun = %+v, want overdue: delivered, 400 kr unpaid past 31 August", r)
	}
	if r := got["jul-aug 2026"]; r.State != mvaStateOverdue || r.OverdueReason != "not_delivered" || r.LedgerVATOre != 30000 || r.Closed {
		t.Errorf("jul-aug = %+v, want overdue: not delivered, 300 kr on the VAT accounts", r)
	}
	if r := got["sep-okt 2026"]; r.State != mvaStateOpen || !r.PeriodEnded || r.LedgerVATOre != -10000 {
		t.Errorf("sep-okt = %+v, want open: ended, due 10 December, 100 kr to be refunded", r)
	}
	if r := got["nov-des 2026"]; r.State != mvaStateOpen || r.PeriodEnded {
		t.Errorf("nov-des = %+v, want open and still running", r)
	}

	if account.BalanceOre != 40000 || account.ExpectedOre != 40000 || account.ResidualOre != 0 {
		t.Errorf("settlement account = %+v, want 400 kr owed, all of it explained by mai-jun", account)
	}
	overdue := 0
	for _, f := range findings {
		if f.Kind != "mva_term_overdue" {
			t.Errorf("unexpected finding %+v", f)
			continue
		}
		overdue++
		if f.Severity != SeverityError {
			t.Errorf("an overdue term must be an error: %+v", f)
		}
	}
	if overdue != 2 {
		t.Errorf("overdue findings = %d, want one each for mai-jun and jul-aug", overdue)
	}
}

func TestBuildMvaStatus_Structure(t *testing.T) {
	terms := vatTerms("bi-monthly", 2026)
	delivery := `{"journalEntryId": 1, "date": "2026-02-28", "transactionType": "Mva-oppgjør", "description": "x", "lines": [{"amount": 50000, "account": "2701"}, {"amount": -50000, "account": "2740"}]}`
	sale := `{"journalEntryId": 9, "date": "2026-01-15", "transactionType": "Salg", "lines": [{"amount": -50000, "account": "2701"}]}`

	t.Run("delivered and not yet due", func(t *testing.T) {
		rows, _, findings := buildMvaStatus(terms, txRows(t, "["+sale+","+delivery+"]"), "2026-03-15")
		if r := statusByTerm(rows)["jan-feb 2026"]; r.State != mvaStateDelivered || r.OutstandingOre != 50000 {
			t.Errorf("jan-feb = %+v, want delivered with 500 kr outstanding", r)
		}
		if len(findings) != 0 {
			t.Errorf("findings = %+v, want none before the due date", findings)
		}
	})
	t.Run("a payment without a readable term settles the oldest unpaid term", func(t *testing.T) {
		rows, account, _ := buildMvaStatus(terms, txRows(t, "["+sale+","+delivery+`,
		  {"journalEntryId": 2, "date": "2026-04-08", "transactionType": "Mva-oppgjør", "description": "Betaling", "lines": [{"amount": 50000, "account": "2740"}, {"amount": -50000, "account": "1920:1"}]}]`), "2026-05-01")
		if r := statusByTerm(rows)["jan-feb 2026"]; r.State != mvaStatePaid || r.PaidDate != "2026-04-08" {
			t.Errorf("jan-feb = %+v, want paid by the unlabelled payment", r)
		}
		if account.BalanceOre != 0 {
			t.Errorf("2740 = %+v, want zero", account)
		}
	})
	t.Run("an annulled delivery leaves the term undelivered", func(t *testing.T) {
		rows, _, _ := buildMvaStatus(terms, txRows(t, "["+sale+","+delivery+`,
		  {"journalEntryId": 3, "date": "2026-02-28", "transactionType": "Mva-oppgjør", "description": "Annullering", "lines": [{"amount": -50000, "account": "2701"}, {"amount": 50000, "account": "2740"}]}]`), "2026-03-15")
		if r := statusByTerm(rows)["jan-feb 2026"]; r.State != mvaStateOpen || r.Closed || r.LedgerVATOre != 50000 {
			t.Errorf("jan-feb = %+v, want open again with the 500 kr back on the VAT accounts", r)
		}
	})
	t.Run("a return with nothing to pay is paid on delivery", func(t *testing.T) {
		rows, _, _ := buildMvaStatus(terms, txRows(t, `[{"journalEntryId": 1, "date": "2026-02-28", "transactionType": "Mva-oppgjør", "lines": [{"amount": 0, "account": "2701"}]}]`), "2026-06-01")
		if r := statusByTerm(rows)["jan-feb 2026"]; r.State != mvaStatePaid {
			t.Errorf("jan-feb = %+v, want paid", r)
		}
	})
	t.Run("a refund not yet received is not overdue", func(t *testing.T) {
		rows, _, findings := buildMvaStatus(terms, txRows(t, `[
		  {"journalEntryId": 9, "date": "2026-01-15", "transactionType": "Kjøp", "lines": [{"amount": 30000, "account": "2711"}]},
		  {"journalEntryId": 1, "date": "2026-02-28", "transactionType": "Mva-oppgjør", "lines": [{"amount": -30000, "account": "2711"}, {"amount": 30000, "account": "2740"}]}]`), "2026-06-01")
		if r := statusByTerm(rows)["jan-feb 2026"]; r.State != mvaStateDelivered || r.OutstandingOre != -30000 {
			t.Errorf("jan-feb = %+v, want delivered with 300 kr owed to the company", r)
		}
		for _, f := range findings {
			if f.Kind == "mva_term_overdue" && f.Detail["term"] == "jan-feb 2026" {
				t.Errorf("a refund the state has not paid is not the company's overdue term: %+v", f)
			}
		}
	})
	t.Run("posting in a delivered term is a finding", func(t *testing.T) {
		_, _, findings := buildMvaStatus(terms, txRows(t, "["+sale+","+delivery+`,
		  {"journalEntryId": 4, "date": "2026-02-10", "transactionType": "Kjøp", "lines": [{"amount": 12500, "account": "2711"}]}]`), "2026-03-15")
		found := false
		for _, f := range findings {
			found = found || (f.Kind == "posted_after_delivery" && f.ImpactOre == -12500)
		}
		if !found {
			t.Errorf("findings = %+v, want posted_after_delivery for the 125 kr booked into the delivered term", findings)
		}
	})
	t.Run("a balance on 2740 the terms do not explain is a finding", func(t *testing.T) {
		_, account, findings := buildMvaStatus(terms, txRows(t, `[{"journalEntryId": 1, "date": "2024-06-30", "transactionType": "Fri", "lines": [{"amount": -77700, "account": "2740"}]}]`), "2026-01-15")
		if account.ResidualOre != 77700 || len(findings) != 1 || findings[0].Kind != "settlement_account_residual" {
			t.Errorf("account = %+v findings = %+v, want a 777 kr residual", account, findings)
		}
	})
}

func TestMvaStatus_CommandOnAMirror(t *testing.T) {
	old := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = old })
	db := seedMirror(t, map[string]string{"journal_entries": statusFixture})

	var rep mvaStatusReport
	runOnMirror(t, db, &rep, "mva-status")
	if rep.VATType != "bi-monthly" || rep.Year != 2026 || len(rep.Terms) != 5 {
		t.Fatalf("report = %+v, want the five bi-monthly terms of 2026 since the books began, from the company's vatType", rep)
	}
	if got := listOrNone(rep.Open); got != "sep-okt 2026, nov-des 2026" {
		t.Errorf("open terms = %s", got)
	}
	if got := listOrNone(rep.Overdue); got != "mai-jun 2026, jul-aug 2026" {
		t.Errorf("overdue terms = %s", got)
	}

	// The purchase-date rule's question: is the term this date falls in closed?
	var closed, open mvaStatusReport
	runOnMirror(t, db, &closed, "mva-status", "--date", "2026-06-28")
	if closed.Date == nil || closed.Date.Term != "mai-jun 2026" || !closed.Date.Closed {
		t.Errorf("--date 2026-06-28 = %+v, want mai-jun 2026, closed", closed.Date)
	}
	runOnMirror(t, db, &open, "mva-status", "--date", "2026-07-20")
	if open.Date == nil || open.Date.Term != "jul-aug 2026" || open.Date.Closed {
		t.Errorf("--date 2026-07-20 = %+v, want jul-aug 2026, not closed", open.Date)
	}

	// A year whose terms are all in the past still lists last year's unpaid ones.
	var next mvaStatusReport
	runOnMirror(t, db, &next, "mva-status", "--year", "2027")
	carried := map[string]bool{}
	for _, r := range next.Terms {
		carried[r.Term] = true
	}
	if !carried["mai-jun 2026"] || carried["mar-apr 2026"] {
		t.Errorf("2027 lists %v, want the unpaid 2026 terms carried over and the paid one left out", carried)
	}
}

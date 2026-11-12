// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — tests for recurring-gaps (issue #31) on an invented
// book: monthly subscriptions booked through June, checked for July-August.
package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// monthlyPurchases renders one purchase per month for a supplier, on the 5th.
func monthlyPurchases(firstID int, contactID int, name, account string, grossOre int64, months ...string) string {
	var rows []string
	for i, m := range months {
		rows = append(rows, fmt.Sprintf(
			`{"purchaseId": %d, "date": "%s-05", "kind": "supplier", "supplier": {"contactId": %d, "name": %q},
			  "lines": [{"netPrice": %d, "vat": %d, "vatType": "HIGH", "account": %q}]}`,
			firstID+i, m, contactID, name, grossOre*4/5, grossOre/5, account))
	}
	return strings.Join(rows, ",")
}

func gapFixture() map[string]string {
	h1 := []string{"2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06"}
	purchases := strings.Join([]string{
		// Two subscriptions booked every month through June.
		monthlyPurchases(100, 1, "Skyhotell AS", "6540", 50000, h1...),
		monthlyPurchases(200, 2, "Regnskapsprogram AS", "6553", 99000, h1...),
		// Booked through June and again in July: a gap in August only.
		monthlyPurchases(300, 3, "Telefoni AS", "6900", 39900, append(h1, "2026-07")...),
		// Stopped in March: by July the series has ended.
		monthlyPurchases(400, 4, "Gammel Tjeneste AS", "6540", 20000, "2025-12", "2026-01", "2026-02", "2026-03"),
		// Two purchases ever: not a recurring series.
		monthlyPurchases(500, 5, "Engangskjøp AS", "6540", 70000, "2026-02", "2026-05"),
		// Still booked every month: no gap.
		monthlyPurchases(600, 6, "Husleie AS", "6300", 1200000, append(h1, "2026-07", "2026-08")...),
		// A cash purchase every month has no supplier to form a series.
		`{"purchaseId": 700, "date": "2026-06-10", "kind": "cash_purchase", "lines": [{"netPrice": 1000, "vat": 250, "vatType": "HIGH", "account": "6800"}]}`,
	}, ",")
	return map[string]string{
		"purchases": "[" + purchases + "]",
		"inbox": `[
		  {"documentId": 9001, "name": "Skyhotell faktura juli", "filename": "skyhotell-2026-07.pdf", "createdAt": "2026-07-06T08:00:00", "status": false},
		  {"documentId": 9002, "name": "Skyhotell faktura", "filename": "used.pdf", "createdAt": "2026-07-07T08:00:00", "status": true},
		  {"documentId": 9003, "name": "Regnskapsprogram", "filename": "r.pdf", "createdAt": "2026-03-02T08:00:00", "status": false}
		]`,
	}
}

func TestRecurringGaps_SubscriptionsBookedThroughJune(t *testing.T) {
	old := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = old })

	var rep Report
	runOnMirror(t, seedMirror(t, gapFixture()), &rep, "recurring-gaps", "--period", "2026-07-01:2026-08-31")

	got := map[string]Finding{}
	for _, f := range rep.Findings {
		got[fmt.Sprintf("%s %s %s", f.ContactName, f.Detail["month"], f.Kind)] = f
	}
	want := []string{
		"Skyhotell AS 2026-07 recurring_gap",
		"Skyhotell AS 2026-08 recurring_gap",
		"Regnskapsprogram AS 2026-07 recurring_gap",
		"Regnskapsprogram AS 2026-08 recurring_gap",
		"Telefoni AS 2026-08 recurring_gap",
		"Gammel Tjeneste AS 2026-07 recurring_ended",
	}
	if len(got) != len(want) {
		t.Errorf("findings = %d, want %d: %v", len(got), len(want), keysOf(got))
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("missing finding %q; got %v", k, keysOf(got))
		}
	}

	july := got["Skyhotell AS 2026-07 recurring_gap"]
	if july.ImpactOre != 50000 || july.Account != "6540" || july.Detail["last_booking_date"] != "2026-06-05" {
		t.Errorf("Skyhotell July = %+v, want the usual 500 kr on 6540, last booked 5 June", july)
	}
	if july.Severity != SeverityWarning || july.ContactID != 1 {
		t.Errorf("Skyhotell July = %+v, want a warning on contact 1", july)
	}
	// The unused inbox document from July is noted; the used one and the one
	// from another month are not.
	hints, _ := july.Detail["unused_inbox_documents"].([]any)
	if len(hints) != 1 || hints[0].(map[string]any)["document_id"] != float64(9001) {
		t.Errorf("Skyhotell July inbox hints = %v, want document 9001 only", july.Detail["unused_inbox_documents"])
	}
	if _, has := got["Regnskapsprogram AS 2026-07 recurring_gap"].Detail["unused_inbox_documents"]; has {
		t.Error("an inbox document from March was attached to the July gap")
	}
	if ended := got["Gammel Tjeneste AS 2026-07 recurring_ended"]; ended.Severity != SeverityInfo {
		t.Errorf("an ended series must be info, not a warning: %+v", ended)
	}
}

func keysOf(m map[string]Finding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestRecurringGapFindings_Thresholds(t *testing.T) {
	purchases := txRows(t, "["+monthlyPurchases(1, 1, "Skyhotell AS", "6540", 50000, "2026-01", "2026-02", "2026-03")+"]")
	defaults := recurringParams{MinMonths: 3, Lookback: 6, EndedAfter: 3}

	kinds := func(months []string, p recurringParams) map[string]string {
		out := map[string]string{}
		fs, _ := recurringGapFindings(purchases, nil, months, p)
		for _, f := range fs {
			out[f.Detail["month"].(string)] = f.Kind
		}
		return out
	}

	got := kinds([]string{"2026-03", "2026-04", "2026-06", "2026-07", "2026-10"}, defaults)
	// March is booked; April and June are gaps (1 and 3 months after the last
	// booking); July is 4 months after, so the series has ended; by October
	// fewer than three of the six months before were booked at all.
	want := map[string]string{"2026-04": gapKindMissing, "2026-06": gapKindMissing, "2026-07": gapKindEnded}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for m, k := range want {
		if got[m] != k {
			t.Errorf("%s = %q, want %q (all: %v)", m, got[m], k, got)
		}
	}

	if strict := kinds([]string{"2026-04"}, recurringParams{MinMonths: 4, Lookback: 6, EndedAfter: 3}); len(strict) != 0 {
		t.Errorf("three bookings must not qualify at --min-months 4: %v", strict)
	}
}

func TestRecurringGapFindings_SeriesIsPerAccountAndUsesTheMedian(t *testing.T) {
	// One supplier, two accounts; the 6540 series has one month at ten times
	// the usual amount.
	rows := "[" + monthlyPurchases(1, 1, "Skyhotell AS", "6540", 50000, "2026-01", "2026-02", "2026-04") + "," +
		monthlyPurchases(10, 1, "Skyhotell AS", "6540", 500000, "2026-03") + "," +
		monthlyPurchases(20, 1, "Skyhotell AS", "6790", 30000, "2026-03", "2026-04", "2026-05") + "]"
	fs, _ := recurringGapFindings(txRows(t, rows), nil, []string{"2026-05"}, recurringParams{MinMonths: 3, Lookback: 6, EndedAfter: 3})
	if len(fs) != 1 || fs[0].Account != "6540" || fs[0].ImpactOre != 50000 {
		t.Fatalf("findings = %+v, want one gap on 6540 at the median 500 kr; 6790 was booked in May", fs)
	}
}

func TestMonthsInWindow(t *testing.T) {
	got := monthsInWindow(Window{From: "2026-07-01", To: "2026-12-31"}, "2026-09-15")
	if strings.Join(got, ",") != "2026-07,2026-08,2026-09" {
		t.Errorf("months = %v, want July through the current month", got)
	}
	if got := monthsInWindow(Window{From: "2026-11-10", To: "2027-02-01"}, "2027-06-01"); strings.Join(got, ",") != "2026-11,2026-12,2027-01,2027-02" {
		t.Errorf("months across New Year = %v", got)
	}
	if got := supplierNameTokens("Sky AS Norge Hotell & Drift"); strings.Join(got, ",") != "hotell,drift" {
		t.Errorf("tokens = %v, want the identifying words only", got)
	}
	if medianOre([]int64{5, 1, 9}) != 5 || medianOre([]int64{4, 2}) != 3 || medianOre(nil) != 0 {
		t.Error("medianOre is wrong")
	}
}

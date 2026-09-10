// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the pure drift detector.
package cli

import "testing"

func TestDriftScan(t *testing.T) {
	tests := []struct {
		name      string
		docs      []driftScanDoc
		tolerance int64
		wantKinds []string // sorted by (date, docID) as driftScan emits
	}{
		{
			name:      "empty input yields no findings",
			docs:      nil,
			tolerance: 1,
			wantKinds: nil,
		},
		{
			name: "clean sale within tolerance yields nothing",
			docs: []driftScanDoc{{
				DocType: "sale", DocID: 1, Date: "2026-01-10", HasHeader: true,
				NetAmount: 10000, VATAmount: 2500,
				Lines: []driftScanLine{{Account: "3000:1", VATType: "HIGH", NetPrice: 10000, VAT: 2500}},
			}},
			tolerance: 1,
			wantKinds: nil,
		},
		{
			name: "header net mismatch beyond tolerance flags total_mismatch",
			docs: []driftScanDoc{{
				DocType: "sale", DocID: 2, Date: "2026-01-11", HasHeader: true,
				// header net 10000 vs summed line net 10050 -> 50 øre off (flag).
				// header vat 2513 matches the line vat, which matches the rate
				// (10050*0.25=2512.5 -> 2513), so only the NET total_mismatch fires.
				NetAmount: 10000, VATAmount: 2513,
				Lines: []driftScanLine{{Account: "3000:1", VATType: "HIGH", NetPrice: 10050, VAT: 2513}},
			}},
			tolerance: 1,
			wantKinds: []string{"total_mismatch"},
		},
		{
			name: "vat_rate mismatch on a line flags vat_rate",
			docs: []driftScanDoc{{
				// purchase: no header check, only the per-line rate check.
				DocType: "purchase", DocID: 7, Date: "2026-02-01",
				Lines: []driftScanLine{{Account: "4000:1", VATType: "HIGH", NetPrice: 10000, VAT: 1000}},
			}},
			tolerance: 1,
			// expected vat 2500, actual 1000 -> off by 1500.
			wantKinds: []string{"vat_rate"},
		},
		{
			name: "NONE vatType line must carry no VAT",
			docs: []driftScanDoc{{
				DocType: "purchase", DocID: 8, Date: "2026-02-02",
				Lines: []driftScanLine{{Account: "4000:1", VATType: "NONE", NetPrice: 10000, VAT: 999}},
			}},
			tolerance: 1,
			wantKinds: []string{"zero_rated_has_vat"},
		},
		{
			name: "a correct reverse-charge basis line yields nothing",
			docs: []driftScanDoc{{
				DocType: "purchase", DocID: 20, Date: "2026-03-01",
				// The VAT of a code-86 line is posted by Fiken as a 2702/2712
				// pair, never on the line: net alone, vat 0, is correct.
				Lines: []driftScanLine{{Account: "6553:1", VATType: "HIGH_FOREIGN_SERVICE_DEDUCTIBLE", NetPrice: 200000, VAT: 0}},
			}},
			tolerance: 5,
			wantKinds: nil,
		},
		{
			name: "a correct import-basis line yields nothing",
			docs: []driftScanDoc{{
				DocType: "purchase", DocID: 21, Date: "2026-03-02",
				Lines: []driftScanLine{{Account: "4130:1", VATType: "MEDIUM_BASIS", NetPrice: 500000, VAT: 0}},
			}},
			tolerance: 5,
			wantKinds: nil,
		},
		{
			name: "a basis line carrying VAT is flagged",
			docs: []driftScanDoc{{
				DocType: "purchase", DocID: 22, Date: "2026-03-03",
				Lines: []driftScanLine{{Account: "4130:1", VATType: "HIGH_BASIS", NetPrice: 200000, VAT: 50000}},
			}},
			tolerance: 5,
			wantKinds: []string{"basis_has_vat"},
		},
		{
			name: "a correct direct line (no basis, VAT is the amount) yields nothing",
			docs: []driftScanDoc{{
				DocType: "purchase", DocID: 23, Date: "2026-03-04",
				Lines: []driftScanLine{{Account: "2713:1", VATType: "MEDIUM_DIRECT", NetPrice: 0, VAT: 43210}},
			}},
			tolerance: 5,
			wantKinds: nil,
		},
		{
			name: "a direct line with a basis is flagged",
			docs: []driftScanDoc{{
				DocType: "purchase", DocID: 24, Date: "2026-03-05",
				Lines: []driftScanLine{{Account: "2713:1", VATType: "HIGH_DIRECT", NetPrice: 20000, VAT: 5000}},
			}},
			tolerance: 5,
			wantKinds: []string{"direct_has_net"},
		},
		{
			name: "a correct nondeductible reverse-charge line yields nothing",
			docs: []driftScanDoc{{
				DocType: "purchase", DocID: 25, Date: "2026-03-06",
				// netPrice is VAT-inclusive (125000 = 100000 * 1.25) and the
				// line vat is the negative embedded amount.
				Lines: []driftScanLine{{Account: "6553:1", VATType: "HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE", NetPrice: 125000, VAT: -25000}},
			}},
			tolerance: 5,
			wantKinds: nil,
		},
		{
			name: "a nondeductible line with the sign flipped is flagged",
			docs: []driftScanDoc{{
				DocType: "purchase", DocID: 26, Date: "2026-03-07",
				Lines: []driftScanLine{{Account: "6553:1", VATType: "HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE", NetPrice: 125000, VAT: 25000}},
			}},
			tolerance: 5,
			wantKinds: []string{"nondeductible_embedded_vat"},
		},
		{
			name: "an unknown vatType is reported, never skipped",
			docs: []driftScanDoc{{
				DocType: "purchase", DocID: 27, Date: "2026-03-08",
				Lines: []driftScanLine{{Account: "4000:1", VATType: "HIGH_INVENTED", NetPrice: 10000, VAT: 2500}},
			}},
			tolerance: 5,
			wantKinds: []string{"unknown_vat_type"},
		},
		{
			name: "an empty vatType is an unknown type too",
			docs: []driftScanDoc{{
				DocType: "purchase", DocID: 28, Date: "2026-03-09",
				Lines: []driftScanLine{{Account: "4000:1", VATType: "", NetPrice: 10000, VAT: 0}},
			}},
			tolerance: 5,
			wantKinds: []string{"unknown_vat_type"},
		},
		{
			name: "3 øre of aggregation rounding passes at the default tolerance 5",
			docs: []driftScanDoc{{
				DocType: "sale", DocID: 29, Date: "2026-03-10",
				// 3900 * 0.15 = 585; the invoice aggregates to 588.
				Lines: []driftScanLine{{Account: "3020:1", VATType: "MEDIUM", NetPrice: 3900, VAT: 588}},
			}},
			tolerance: 5,
			wantKinds: nil,
		},
		{
			name: "the same 3 øre is a finding at tolerance 1",
			docs: []driftScanDoc{{
				DocType: "sale", DocID: 30, Date: "2026-03-11",
				Lines: []driftScanLine{{Account: "3020:1", VATType: "MEDIUM", NetPrice: 3900, VAT: 588}},
			}},
			tolerance: 1,
			wantKinds: []string{"vat_rate"},
		},
		{
			name: "purchase never triggers total_mismatch (no header)",
			docs: []driftScanDoc{{
				DocType: "purchase", DocID: 9, Date: "2026-02-03", HasHeader: false,
				NetAmount: 99999, VATAmount: 88888, // ignored: HasHeader false
				Lines: []driftScanLine{{Account: "4000:1", VATType: "NONE", NetPrice: 100, VAT: 0}},
			}},
			tolerance: 1,
			wantKinds: nil,
		},
		{
			name: "rounding within tolerance does not flag vat_rate",
			docs: []driftScanDoc{{
				DocType: "sale", DocID: 3, Date: "2026-01-12", HasHeader: true,
				// netPrice 333 * 0.25 = 83.25 -> round 83; recorded 84 -> diff 1, tolerance 1 => OK.
				NetAmount: 333, VATAmount: 84,
				Lines: []driftScanLine{{Account: "3000:1", VATType: "HIGH", NetPrice: 333, VAT: 84}},
			}},
			tolerance: 1,
			wantKinds: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := driftScan(tt.docs, tt.tolerance, nil)
			if len(got) != len(tt.wantKinds) {
				t.Fatalf("got %d findings %+v, want %d (%v)", len(got), got, len(tt.wantKinds), tt.wantKinds)
			}
			for i, k := range tt.wantKinds {
				if got[i].Kind != k {
					t.Errorf("finding[%d].Kind = %q, want %q", i, got[i].Kind, k)
				}
			}
		})
	}
}

// TestDriftSeverity pins the grading rule: a regime breach is an error (the
// MVA return is wrong), a rate/total difference only a warning.
func TestDriftSeverity(t *testing.T) {
	tests := []struct {
		kind string
		want Severity
	}{
		{"total_mismatch", SeverityWarning},
		{"vat_rate", SeverityWarning},
		{"basis_has_vat", SeverityError},
		{"direct_has_net", SeverityError},
		{"nondeductible_embedded_vat", SeverityError},
		{"zero_rated_has_vat", SeverityError},
		{"unknown_vat_type", SeverityError},
		{"settled_residual", SeverityError},
		{"currency_mismatch", SeverityWarning},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			if got := driftSeverity(tt.kind); got != tt.want {
				t.Errorf("driftSeverity(%q) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

// TestDriftScanFindingFields checks that the scanned document's contact and
// description reach the Finding, that the impact is the signed difference, and
// that expected/actual survive in Detail as raw øre.
func TestDriftScanFindingFields(t *testing.T) {
	docs := []driftScanDoc{{
		DocType: "purchase", DocID: 7, Date: "2026-02-01",
		ContactID: 42, ContactName: "Acme AS", Description: "INV-1",
		Lines: []driftScanLine{{
			Account: "4000:1", VATType: "HIGH", Description: "Widgets",
			NetPrice: 10000, VAT: 1000,
		}},
	}}
	got, _ := driftScan(docs, 1, nil)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	f := got[0]
	if f.ContactID != 42 || f.ContactName != "Acme AS" {
		t.Errorf("contact = (%d, %q), want (42, \"Acme AS\")", f.ContactID, f.ContactName)
	}
	if f.Description != "Widgets" {
		t.Errorf("Description = %q, want the line description %q", f.Description, "Widgets")
	}
	// expected vat 2500, actual 1000 -> impact -1500.
	if f.ImpactOre != -1500 {
		t.Errorf("ImpactOre = %d, want -1500", f.ImpactOre)
	}
	if f.Detail["expected_ore"] != int64(2500) || f.Detail["actual_ore"] != int64(1000) {
		t.Errorf("Detail = %+v, want expected_ore 2500 / actual_ore 1000", f.Detail)
	}
	if note, _ := f.Detail["note"].(string); note == "" {
		t.Error("Detail[note] is empty, want an explanation")
	}
}

// TestDriftScanFallsBackToDocumentDescription: a header mismatch has no line,
// so the document's own description identifies it.
func TestDriftScanFallsBackToDocumentDescription(t *testing.T) {
	docs := []driftScanDoc{{
		DocType: "sale", DocID: 2, Date: "2026-01-11", Description: "XK455L", HasHeader: true,
		NetAmount: 10000, VATAmount: 2513,
		Lines: []driftScanLine{{Account: "3000:1", VATType: "HIGH", NetPrice: 10050, VAT: 2513}},
	}}
	got, _ := driftScan(docs, 1, nil)
	if len(got) != 1 || got[0].Description != "XK455L" {
		t.Fatalf("got %+v, want one finding described as XK455L", got)
	}
}

// driftTestIndex is the journal behind the settled tests: 5001 is a settled
// purchase whose supplier account nets to zero, 5002 one that still carries a
// credit of 200000 øre.
func driftTestIndex(t *testing.T) *txIndex {
	t.Helper()
	return buildTxIndex(txRows(t, `[
	  {"transactionId": 5001, "entries": [{"lines": [
	    {"amount": 100000, "account": "6553"},
	    {"amount": -100000, "account": "2400:20001"},
	    {"amount": 100000, "account": "2400:20001"},
	    {"amount": -100000, "account": "1920:10001"}
	  ]}]},
	  {"transactionId": 5002, "entries": [{"lines": [
	    {"amount": 200000, "account": "6553"},
	    {"amount": -200000, "account": "2400:20002"}
	  ]}]}
	]`))
}

// TestDriftScanSettledResidual: a document marked settled must have a zero
// ledger side — the supplier account for a purchase, outstandingBalance for a
// sale. A purchase whose transaction is not mirrored is skipped and counted.
func TestDriftScanSettledResidual(t *testing.T) {
	tests := []struct {
		name          string
		doc           driftScanDoc
		wantKinds     []string
		wantImpact    int64
		wantUnindexed int
	}{
		{
			name: "settled purchase with a zero supplier balance is clean",
			doc: driftScanDoc{
				DocType: "purchase", DocID: 1, Date: "2026-04-01",
				Settled: true, Paid: true, TransactionID: 5001,
			},
		},
		{
			name: "settled purchase whose supplier account still carries a balance",
			doc: driftScanDoc{
				DocType: "purchase", DocID: 2, Date: "2026-04-02",
				Settled: true, Paid: false, TransactionID: 5002,
			},
			wantKinds:  []string{"settled_residual"},
			wantImpact: -200000,
		},
		{
			name: "an unsettled purchase with a balance is not our business",
			doc: driftScanDoc{
				DocType: "purchase", DocID: 3, Date: "2026-04-03",
				Settled: false, TransactionID: 5002,
			},
		},
		{
			name: "settled purchase with no transactionId is skipped and counted",
			doc: driftScanDoc{
				DocType: "purchase", DocID: 4, Date: "2026-04-04",
				Settled: true, TransactionID: 0,
			},
			wantUnindexed: 1,
		},
		{
			name: "settled purchase whose transaction is not mirrored is skipped and counted",
			doc: driftScanDoc{
				DocType: "purchase", DocID: 5, Date: "2026-04-05",
				Settled: true, TransactionID: 8888,
			},
			wantUnindexed: 1,
		},
		{
			name: "settled sale with an outstanding balance is flagged",
			doc: driftScanDoc{
				DocType: "sale", DocID: 6, Date: "2026-04-06",
				Settled: true, OutstandingBalance: 14500,
			},
			wantKinds:  []string{"settled_residual"},
			wantImpact: 14500,
		},
		{
			name: "settled sale with nothing outstanding is clean, index or not",
			doc: driftScanDoc{
				DocType: "sale", DocID: 7, Date: "2026-04-07",
				Settled: true, OutstandingBalance: 0,
			},
		},
	}
	ix := driftTestIndex(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, unindexed := driftScan([]driftScanDoc{tt.doc}, 5, ix)
			if len(got) != len(tt.wantKinds) {
				t.Fatalf("got %d findings %+v, want %v", len(got), got, tt.wantKinds)
			}
			for i, k := range tt.wantKinds {
				if got[i].Kind != k {
					t.Errorf("finding[%d].Kind = %q, want %q", i, got[i].Kind, k)
				}
			}
			if len(got) == 1 {
				if got[0].ImpactOre != tt.wantImpact {
					t.Errorf("ImpactOre = %d, want %d", got[0].ImpactOre, tt.wantImpact)
				}
				if got[0].Severity != SeverityError {
					t.Errorf("Severity = %q, want error", got[0].Severity)
				}
				if got[0].Detail["settled"] != true {
					t.Errorf("Detail = %+v, want settled true", got[0].Detail)
				}
			}
			if unindexed != tt.wantUnindexed {
				t.Errorf("settledUnindexed = %d, want %d", unindexed, tt.wantUnindexed)
			}
		})
	}
}

// TestDriftScanSettledWithoutIndex: with no `transactions` synced at all the
// settled check on purchases must go quiet (and count), not fire on everything.
func TestDriftScanSettledWithoutIndex(t *testing.T) {
	docs := []driftScanDoc{
		{DocType: "purchase", DocID: 1, Date: "2026-04-01", Settled: true, TransactionID: 5002},
		{DocType: "purchase", DocID: 2, Date: "2026-04-02", Settled: true, TransactionID: 5001},
	}
	got, unindexed := driftScan(docs, 5, nil)
	if len(got) != 0 {
		t.Errorf("got %+v, want no findings when nothing is indexed", got)
	}
	if unindexed != 2 {
		t.Errorf("settledUnindexed = %d, want 2", unindexed)
	}
}

// TestDriftScanCurrencyMismatch: on a foreign-currency purchase, a line whose
// netPrice equals its netPriceInCurrency was never converted to NOK.
func TestDriftScanCurrencyMismatch(t *testing.T) {
	tests := []struct {
		name       string
		doc        driftScanDoc
		wantKinds  []string
		wantImpact int64
	}{
		{
			name: "unconverted foreign-currency line is flagged once per purchase",
			doc: driftScanDoc{
				DocType: "purchase", DocID: 1, Date: "2026-05-01", Currency: "USD",
				Lines: []driftScanLine{
					{Account: "6553", VATType: "NONE", NetPrice: 45000, NetPriceInCurrency: 45000, HasNetPriceInCurrency: true},
					{Account: "6553", VATType: "NONE", NetPrice: 12000, NetPriceInCurrency: 12000, HasNetPriceInCurrency: true},
				},
			},
			wantKinds:  []string{"currency_mismatch"},
			wantImpact: 45000,
		},
		{
			name: "a converted foreign-currency purchase is clean",
			doc: driftScanDoc{
				DocType: "purchase", DocID: 2, Date: "2026-05-02", Currency: "USD",
				Lines: []driftScanLine{
					{Account: "6553", VATType: "NONE", NetPrice: 495000, NetPriceInCurrency: 45000, HasNetPriceInCurrency: true},
				},
			},
		},
		{
			name: "NOK purchases are never a currency mismatch, equal or not",
			doc: driftScanDoc{
				DocType: "purchase", DocID: 3, Date: "2026-05-03", Currency: "NOK",
				Lines: []driftScanLine{
					{Account: "6553", VATType: "NONE", NetPrice: 45000, NetPriceInCurrency: 45000, HasNetPriceInCurrency: true},
				},
			},
		},
		{
			name: "an absent netPriceInCurrency is not a zero-equals-zero match",
			doc: driftScanDoc{
				DocType: "purchase", DocID: 4, Date: "2026-05-04", Currency: "USD",
				Lines: []driftScanLine{{Account: "6553", VATType: "NONE", NetPrice: 0, VAT: 0}},
			},
		},
		{
			name: "sales are out of scope for the currency check",
			doc: driftScanDoc{
				DocType: "sale", DocID: 5, Date: "2026-05-05", Currency: "USD",
				Lines: []driftScanLine{
					{Account: "3000", VATType: "NONE", NetPrice: 45000, NetPriceInCurrency: 45000, HasNetPriceInCurrency: true},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := driftScan([]driftScanDoc{tt.doc}, 5, nil)
			if len(got) != len(tt.wantKinds) {
				t.Fatalf("got %d findings %+v, want %v", len(got), got, tt.wantKinds)
			}
			if len(got) == 1 {
				if got[0].ImpactOre != tt.wantImpact {
					t.Errorf("ImpactOre = %d, want %d", got[0].ImpactOre, tt.wantImpact)
				}
				if got[0].Severity != SeverityWarning {
					t.Errorf("Severity = %q, want warning", got[0].Severity)
				}
				if got[0].Detail["currency"] != "USD" || got[0].Detail["net_price_ore"] != int64(45000) {
					t.Errorf("Detail = %+v, want the currency and the unconverted amount", got[0].Detail)
				}
			}
		})
	}
}

// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the pure MVA summarizer.
package cli

import (
	"encoding/json"
	"testing"

	"fiken-cli/internal/fikencore"
)

func TestSummarizeMVA_Empty(t *testing.T) {
	buckets, out, in := summarizeMVA(nil, fikencore.SideSales)
	if len(buckets) != 0 || out != 0 || in != 0 {
		t.Fatalf("empty summary = %+v out %d in %d; want no buckets, 0/0", buckets, out, in)
	}
}

func TestSummarizeMVA_SalesBucketsByType(t *testing.T) {
	lines := []mvaLine{
		{VATType: "HIGH", BasisOre: 10000, VATOre: 2500},
		{VATType: "HIGH", BasisOre: 5000, VATOre: 1250},
		{VATType: "LOW", BasisOre: 2000, VATOre: 240},
		{VATType: "NONE", BasisOre: 800, VATOre: 0},
	}
	buckets, out, in := summarizeMVA(lines, fikencore.SideSales)
	if len(buckets) != 3 {
		t.Fatalf("got %d buckets %+v, want 3 (HIGH, LOW, NONE)", len(buckets), buckets)
	}
	// Sorted by vatType: HIGH, LOW, NONE.
	if buckets[0].VATType != "HIGH" || buckets[0].BasisOre != 15000 || buckets[0].OutputVATOre != 3750 {
		t.Errorf("HIGH bucket = %+v; want basis 15000 output 3750", buckets[0])
	}
	if buckets[0].Regime != "line_vat" || buckets[0].MVACode != 3 {
		t.Errorf("HIGH bucket = %+v; want regime line_vat and the sales code 3", buckets[0])
	}
	if buckets[0].InputVATOre != 0 {
		t.Errorf("a sales line must not produce input VAT: %+v", buckets[0])
	}
	if buckets[1].VATType != "LOW" || buckets[1].OutputVATOre != 240 {
		t.Errorf("LOW bucket = %+v; want output 240", buckets[1])
	}
	if buckets[2].VATType != "NONE" || buckets[2].OutputVATOre != 0 || buckets[2].BasisOre != 800 {
		t.Errorf("NONE bucket = %+v; want basis 800, no VAT", buckets[2])
	}
	if out != 3990 || in != 0 {
		t.Errorf("totals = out %d in %d, want 3990/0", out, in)
	}
	// kr string formatting is part of the contract.
	if buckets[0].OutputVAT != "37.50" {
		t.Errorf("HIGH output kr = %q, want 37.50", buckets[0].OutputVAT)
	}
}

func TestSummarizeMVA_PurchaseRegimes(t *testing.T) {
	lines := []mvaLine{
		// Ordinary domestic purchase: deductible input VAT.
		{VATType: "HIGH", BasisOre: 100000, VATOre: 25000},
		// Reverse charge, deductible: line vat is 0, the return carries both
		// sides at 25% of the basis and they cancel.
		{VATType: "HIGH_FOREIGN_SERVICE_DEDUCTIBLE", BasisOre: 200000, VATOre: 0},
		// Import basis with no rate: basis only.
		{VATType: "NONE_IMPORT_BASIS", BasisOre: 500000, VATOre: 0},
		// Direct: no basis, the line vat is the input VAT.
		{VATType: "MEDIUM_DIRECT", BasisOre: 0, VATOre: 43210},
		// Nondeductible reverse charge: VAT-inclusive net, negative line vat.
		{VATType: "HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE", BasisOre: 125000, VATOre: -25000},
	}
	buckets, out, in := summarizeMVA(lines, fikencore.SidePurchases)
	got := map[string]mvaBucket{}
	for _, b := range buckets {
		got[b.VATType] = b
	}
	if b := got["HIGH"]; b.InputVATOre != 25000 || b.OutputVATOre != 0 || b.MVACode != 1 {
		t.Errorf("HIGH purchase bucket = %+v; want input 25000, no output, kode 1", b)
	}
	if b := got["HIGH_FOREIGN_SERVICE_DEDUCTIBLE"]; b.OutputVATOre != 50000 || b.InputVATOre != 50000 || b.BasisOre != 200000 || b.MVACode != 86 {
		t.Errorf("reverse-charge bucket = %+v; want basis 200000 and 50000 on both sides, kode 86", b)
	}
	if b := got["NONE_IMPORT_BASIS"]; b.BasisOre != 500000 || b.OutputVATOre != 0 || b.InputVATOre != 0 || b.Regime != "zero" {
		t.Errorf("import-basis bucket = %+v; want basis only", b)
	}
	if b := got["MEDIUM_DIRECT"]; b.BasisOre != 0 || b.InputVATOre != 43210 || b.Regime != "direct" {
		t.Errorf("direct bucket = %+v; want no basis and 43210 of input VAT", b)
	}
	// The nondeductible line's negative vat must never be summed as-is: the
	// basis is net/(1+r) and the VAT is output only.
	if b := got["HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE"]; b.BasisOre != 100000 || b.OutputVATOre != 25000 || b.InputVATOre != 0 {
		t.Errorf("nondeductible bucket = %+v; want basis 100000, output 25000, no input", b)
	}
	for _, b := range buckets {
		if b.OutputVATOre < 0 || b.InputVATOre < 0 {
			t.Errorf("bucket %s has a negative total: %+v", b.VATType, b)
		}
	}
	if out != 75000 {
		t.Errorf("purchase-side output VAT = %d, want 75000 (50000 reverse charge + 25000 nondeductible)", out)
	}
	if in != 118210 {
		t.Errorf("purchase-side input VAT = %d, want 118210 (25000 + 50000 + 43210)", in)
	}
}

func TestSummarizeMVA_UnknownTypeGetsItsOwnBucket(t *testing.T) {
	buckets, out, in := summarizeMVA([]mvaLine{{VATType: "HIGH_INVENTED", BasisOre: 10000, VATOre: 2500}}, fikencore.SidePurchases)
	if len(buckets) != 1 {
		t.Fatalf("got %d buckets, want 1", len(buckets))
	}
	b := buckets[0]
	if b.VATType != "HIGH_INVENTED" || b.Regime != "unknown" || b.MVACode != 0 {
		t.Errorf("unknown bucket = %+v; want regime unknown and no code", b)
	}
	if b.BasisOre != 10000 || b.InputVATOre != 2500 || out != 0 || in != 2500 {
		t.Errorf("unknown bucket = %+v (out %d in %d); the raw amounts must stay visible", b, out, in)
	}
}

func TestSummarizeMVA_NetPosition(t *testing.T) {
	// A reverse-charge purchase is VAT-neutral: it adds the same amount to
	// both sides, so it must not move the net position.
	_, salesOut, salesIn := summarizeMVA([]mvaLine{{VATType: "HIGH", BasisOre: 10000, VATOre: 2500}}, fikencore.SideSales)
	_, purchOut, purchIn := summarizeMVA([]mvaLine{
		{VATType: "HIGH", BasisOre: 4000, VATOre: 1000},
		{VATType: "HIGH_FOREIGN_SERVICE_DEDUCTIBLE", BasisOre: 200000, VATOre: 0},
	}, fikencore.SidePurchases)
	if net := (salesOut + purchOut) - (salesIn + purchIn); net != 1500 {
		t.Errorf("net VAT = %d, want 1500", net)
	}
}

// TestSummarizeMVA_BucketsByTypeAndCode: the same vatType on the two sides
// carries different MVA codes, and a return is read by code, so the code is
// part of the bucket key. Within one call the side is fixed, so the split shows
// up as one bucket per (vatType, code) pair across the two sides.
func TestSummarizeMVA_BucketsByTypeAndCode(t *testing.T) {
	salesBuckets, _, _ := summarizeMVA([]mvaLine{{VATType: "HIGH", BasisOre: 10000, VATOre: 2500}}, fikencore.SideSales)
	purchBuckets, _, _ := summarizeMVA([]mvaLine{{VATType: "HIGH", BasisOre: 10000, VATOre: 2500}}, fikencore.SidePurchases)
	if len(salesBuckets) != 1 || salesBuckets[0].MVACode != 3 {
		t.Fatalf("sales HIGH = %+v, want a single bucket on kode 3", salesBuckets)
	}
	if len(purchBuckets) != 1 || purchBuckets[0].MVACode != 1 {
		t.Fatalf("purchase HIGH = %+v, want a single bucket on kode 1", purchBuckets)
	}
}

// TestSummarizeMVA_BucketOrder pins the sort: vatType, then MVA code.
func TestSummarizeMVA_BucketOrder(t *testing.T) {
	buckets, _, _ := summarizeMVA([]mvaLine{
		{VATType: "NONE", BasisOre: 100},
		{VATType: "HIGH_BASIS", BasisOre: 100},
		{VATType: "HIGH", BasisOre: 100, VATOre: 25},
	}, fikencore.SidePurchases)
	var got []string
	for _, b := range buckets {
		got = append(got, b.VATType)
	}
	want := []string{"HIGH", "HIGH_BASIS", "NONE"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("bucket order = %v, want %v", got, want)
		}
	}
}

// crossCheckLines is the purchase side of the cross-check fixture: two
// reverse-charge (code 86) purchases, one per transaction.
var crossCheckLines = []mvaLine{
	{VATType: "HIGH_FOREIGN_SERVICE_DEDUCTIBLE", BasisOre: 100000, VATOre: 0, TxID: 7001},
	{VATType: "HIGH_FOREIGN_SERVICE_DEDUCTIBLE", BasisOre: 100000, VATOre: 0, TxID: 7002},
}

func TestCrossCheckReverseCharge(t *testing.T) {
	tests := []struct {
		name        string
		journal     string
		wantFinding bool
		wantImpact  int64
		wantMatched int
	}{
		{
			name: "journal agrees with the computed VAT",
			// 25% of 100000 on each transaction, credited to 2702.
			journal: `[
			  {"transactionId": 7001, "entries": [{"lines": [
			    {"amount": -25000, "account": "2702"}, {"amount": 25000, "account": "2712"}]}]},
			  {"transactionId": 7002, "entries": [{"lines": [
			    {"amount": -25000, "account": "2702"}, {"amount": 25000, "account": "2712"}]}]}
			]`,
		},
		{
			name: "a few øre of rounding is not a finding",
			journal: `[
			  {"transactionId": 7001, "entries": [{"lines": [{"amount": -25002, "account": "2702"}]}]},
			  {"transactionId": 7002, "entries": [{"lines": [{"amount": -24999, "account": "2702"}]}]}
			]`,
		},
		{
			name: "a missing 2702 posting on one transaction is a finding",
			journal: `[
			  {"transactionId": 7001, "entries": [{"lines": [{"amount": -25000, "account": "2702"}]}]},
			  {"transactionId": 7002, "entries": [{"lines": [{"amount": 200000, "account": "6553"}]}]}
			]`,
			wantFinding: true,
			wantImpact:  25000, // computed 50000, journal 25000
			wantMatched: 2,
		},
		{
			name: "only one of the two transactions is mirrored",
			journal: `[
			  {"transactionId": 7001, "entries": [{"lines": [{"amount": -25000, "account": "2702"}]}]}
			]`,
			wantFinding: true,
			wantImpact:  25000,
			wantMatched: 1,
		},
		{
			name:    "no transactions mirrored at all: nothing was compared, nothing is reported",
			journal: `[]`,
		},
	}
	buckets, _, _ := summarizeMVA(crossCheckLines, fikencore.SidePurchases)
	if buckets[0].OutputVATOre != 50000 {
		t.Fatalf("fixture bucket output = %d, want 50000", buckets[0].OutputVATOre)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ix := buildTxIndex(txRows(t, tt.journal))
			got := crossCheckReverseCharge(crossCheckLines, buckets, ix)
			if got == nil {
				t.Fatal("findings must be an empty slice, never nil")
			}
			if !tt.wantFinding {
				if len(got) != 0 {
					t.Fatalf("got %+v, want no findings", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("got %d findings %+v, want 1", len(got), got)
			}
			f := got[0]
			if f.Kind != "reverse_charge_journal_mismatch" || f.Severity != SeverityWarning || f.DocType != "bucket" {
				t.Errorf("finding envelope = %+v, want a bucket-level warning", f)
			}
			if f.VATType != "HIGH_FOREIGN_SERVICE_DEDUCTIBLE" || f.ImpactOre != tt.wantImpact {
				t.Errorf("finding = %+v, want impact %d on the code-86 bucket", f, tt.wantImpact)
			}
			if f.Detail["mva_code"] != 86 || f.Detail["bucket_output_vat_ore"] != int64(50000) {
				t.Errorf("Detail = %+v, want kode 86 and the bucket's own output VAT", f.Detail)
			}
			if f.Detail["transactions_matched"] != tt.wantMatched {
				t.Errorf("Detail[transactions_matched] = %v, want %d", f.Detail["transactions_matched"], tt.wantMatched)
			}
		})
	}
}

// TestCrossCheckReverseCharge_OnlyBasisRegime: ordinary domestic and
// nondeductible purchases post no 2702/2712 pair, so they are never
// cross-checked even when the journal is empty.
func TestCrossCheckReverseCharge_OnlyBasisRegime(t *testing.T) {
	lines := []mvaLine{
		{VATType: "HIGH", BasisOre: 100000, VATOre: 25000, TxID: 7001},
		{VATType: "HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE", BasisOre: 125000, VATOre: -25000, TxID: 7002},
		{VATType: "HIGH_INVENTED", BasisOre: 100000, VATOre: 25000, TxID: 7003},
	}
	buckets, _, _ := summarizeMVA(lines, fikencore.SidePurchases)
	ix := buildTxIndex(txRows(t, `[{"transactionId": 7001, "entries": [{"lines": []}]}]`))
	if got := crossCheckReverseCharge(lines, buckets, ix); len(got) != 0 {
		t.Errorf("got %+v, want no findings outside the reverse-charge regime", got)
	}
}

// Import of goods (codes 21-23) is a basis regime too, but its VAT is not
// booked on 2702/2712, so it must never be cross-checked against them.
func TestCrossCheckReverseCharge_SkipsImportBasis(t *testing.T) {
	lines := []mvaLine{
		{VATType: "HIGH_BASIS", BasisOre: 100000, VATOre: 0, TxID: 7001},
		{VATType: "MEDIUM_BASIS", BasisOre: 100000, VATOre: 0, TxID: 7002},
	}
	buckets, _, _ := summarizeMVA(lines, fikencore.SidePurchases)
	ix := buildTxIndex(nil)
	if got := crossCheckReverseCharge(lines, buckets, ix); len(got) != 0 {
		t.Fatalf("import-basis buckets must not be cross-checked, got %d findings", len(got))
	}
	for _, c := range []int{21, 22, 23, 85, 90} {
		if isForeignServiceCode(c) {
			t.Errorf("code %d wrongly treated as foreign service", c)
		}
	}
	for _, c := range []int{86, 87, 88, 89} {
		if !isForeignServiceCode(c) {
			t.Errorf("code %d should be foreign service", c)
		}
	}
}

// ---- Credit notes and the ledger cross-check (issue #24) ----

// creditFixture is one term of an invented book. Two invoices of 10 000 kr
// + 25 % each; the first is credited in full on its own date and the second
// in part. One domestic purchase. The journal carries the same postings, plus
// the settlement entry Fiken makes when the return is delivered.
var creditFixture = map[string]string{
	"sales": `[
	  {"saleId": 1, "date": "2026-03-10", "transactionId": 101, "lines": [{"netPrice": 1000000, "vat": 250000, "vatType": "HIGH", "account": "3000"}]},
	  {"saleId": 2, "date": "2026-03-20", "transactionId": 102, "lines": [{"netPrice": 1000000, "vat": 250000, "vatType": "HIGH", "account": "3000"}]}
	]`,
	"credit_notes": `[
	  {"creditNoteId": 11, "issueDate": "2026-03-10", "associatedInvoiceId": 1, "net": -1000000, "vat": -250000,
	   "sale": {"saleId": 91, "date": "2026-03-10", "transactionId": 191, "lines": [{"netPrice": -1000000, "vat": -250000, "vatType": "HIGH", "account": "3000"}]}},
	  {"creditNoteId": 12, "issueDate": "2026-04-02", "associatedInvoiceId": 2, "net": -200000, "vat": -50000,
	   "sale": {"saleId": 92, "date": "2026-04-02", "transactionId": 192, "lines": [{"netPrice": -200000, "vat": -50000, "vatType": "HIGH", "account": "3000"}]}},
	  {"creditNoteId": 13, "issueDate": "2026-06-01", "associatedInvoiceId": 2, "net": -100000, "vat": -25000,
	   "sale": {"saleId": 93, "date": "2026-06-01", "transactionId": 193, "lines": [{"netPrice": -100000, "vat": -25000, "vatType": "HIGH", "account": "3000"}]}}
	]`,
	"purchases": `[
	  {"purchaseId": 21, "date": "2026-03-15", "transactionId": 121, "lines": [{"netPrice": 400000, "vat": 100000, "vatType": "HIGH", "account": "6540"}]}
	]`,
	"journal_entries": `[
	  {"journalEntryId": 1, "date": "2026-03-10", "transactionType": "Salg", "lines": [{"amount": 1250000, "account": "1500:10001"}, {"amount": -250000, "account": "2701"}, {"amount": -1000000, "account": "3000"}]},
	  {"journalEntryId": 2, "date": "2026-03-20", "transactionType": "Salg", "lines": [{"amount": 1250000, "account": "1500:10002"}, {"amount": -250000, "account": "2701"}, {"amount": -1000000, "account": "3000"}]},
	  {"journalEntryId": 3, "date": "2026-03-10", "transactionType": "Kreditert salg", "lines": [{"amount": -1250000, "account": "1500:10001"}, {"amount": 250000, "account": "2701"}, {"amount": 1000000, "account": "3000"}]},
	  {"journalEntryId": 4, "date": "2026-04-02", "transactionType": "Kreditert salg", "lines": [{"amount": -250000, "account": "1500:10002"}, {"amount": 50000, "account": "2701"}, {"amount": 200000, "account": "3000"}]},
	  {"journalEntryId": 5, "date": "2026-03-15", "transactionType": "Kjøp", "lines": [{"amount": -500000, "account": "2400:20001"}, {"amount": 100000, "account": "2711"}, {"amount": 400000, "account": "6540"}]},
	  {"journalEntryId": 6, "date": "2026-04-30", "transactionType": "Mva-oppgjør", "description": "Overføring etter mva-melding for mar-apr 2026",
	   "lines": [{"amount": 200000, "account": "2701"}, {"amount": -100000, "account": "2711"}, {"amount": -100000, "account": "2740"}]},
	  {"journalEntryId": 7, "date": "2026-06-01", "transactionType": "Kreditert salg", "lines": [{"amount": -125000, "account": "1500:10002"}, {"amount": 25000, "account": "2701"}, {"amount": 100000, "account": "3000"}]}
	]`,
}

func TestMvaSummary_CreditNotesReduceOutputVATToTheLedger(t *testing.T) {
	var rep mvaSummaryReport
	runOnMirror(t, seedMirror(t, creditFixture), &rep, "mva-summary", "--period", "2026-03-01:2026-04-30")

	// Two sales at 2 500 kr VAT, one credited in full and one by 500 kr.
	if rep.TotalOutputOre != 200000 {
		t.Fatalf("output VAT = %d øre, want 200000 (500000 sold, 300000 credited)", rep.TotalOutputOre)
	}
	if rep.Ledger.OutputVATOre != rep.TotalOutputOre {
		t.Fatalf("summary output %d øre does not equal the ledger's 27xx movement %d øre", rep.TotalOutputOre, rep.Ledger.OutputVATOre)
	}
	if rep.CreditNotes.Count != 2 || rep.CreditNotes.OutputVATOre != -300000 || rep.CreditNotes.BasisOre != -1200000 {
		t.Fatalf("credit_notes = %+v, want the two notes in the window: basis -1200000, VAT -300000", rep.CreditNotes)
	}
	if len(rep.Output) != 1 || rep.Output[0].BasisOre != 800000 || rep.Output[0].OutputVATOre != 200000 {
		t.Fatalf("sales buckets = %+v, want one HIGH bucket of basis 800000 and VAT 200000", rep.Output)
	}
	if rep.NetVATOre != 100000 || rep.Ledger.NetVATOre != 100000 || rep.Ledger.DiffOre != 0 || !rep.Ledger.Compared {
		t.Fatalf("net = %d, ledger = %+v; want both 100000 and no difference", rep.NetVATOre, rep.Ledger)
	}
	if len(rep.Findings) != 0 {
		t.Fatalf("a summary that agrees with the ledger must carry no finding: %+v", rep.Findings)
	}
}

func TestMvaSummary_DisagreementWithTheLedgerIsAFinding(t *testing.T) {
	// The same book with the credit notes missing from the mirror: the
	// summary now overstates output VAT, and must say so.
	fixture := map[string]string{}
	for k, v := range creditFixture {
		fixture[k] = v
	}
	fixture["credit_notes"] = `[]`

	var rep mvaSummaryReport
	runOnMirror(t, seedMirror(t, fixture), &rep, "mva-summary", "--period", "2026-03-01:2026-04-30")
	if rep.TotalOutputOre != 500000 {
		t.Fatalf("output VAT = %d, want the uncredited 500000", rep.TotalOutputOre)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly the ledger mismatch", rep.Findings)
	}
	f := rep.Findings[0]
	if f.Kind != "ledger_vat_mismatch" || f.Severity != SeverityWarning || f.ImpactOre != 300000 {
		t.Fatalf("finding = %+v, want a ledger_vat_mismatch warning of 300000 øre", f)
	}
}

func TestCreditNoteLines(t *testing.T) {
	win := Window{From: "2026-03-01", To: "2026-04-30"}
	notes := func(s string) []map[string]json.RawMessage { return txRows(t, s) }

	t.Run("a note mixing signs keeps each line's sign", func(t *testing.T) {
		// A credited item and the reversal of the discount it was sold with.
		lines, counted, _, _ := creditNoteLines(notes(`[{"issueDate": "2026-03-05", "sale": {"saleId": 1, "date": "2026-03-05", "lines": [
		  {"netPrice": -100000, "vat": -25000, "vatType": "HIGH"}, {"netPrice": 10000, "vat": 2500, "vatType": "HIGH"}]}}]`), win, nil)
		if counted != 1 || len(lines) != 2 || lines[0].VATOre != -25000 || lines[1].VATOre != 2500 {
			t.Fatalf("lines = %+v", lines)
		}
	})
	t.Run("a note served positive is negated as a whole", func(t *testing.T) {
		lines, _, _, _ := creditNoteLines(notes(`[{"issueDate": "2026-03-05", "sale": {"saleId": 1, "date": "2026-03-05", "lines": [
		  {"netPrice": 100000, "vat": 25000, "vatType": "HIGH"}]}}]`), win, nil)
		if len(lines) != 1 || lines[0].BasisOre != -100000 || lines[0].VATOre != -25000 {
			t.Fatalf("lines = %+v, want the line negated", lines)
		}
	})
	t.Run("outside the window, undated, already a sale, without lines", func(t *testing.T) {
		lines, counted, withoutLines, undated := creditNoteLines(notes(`[
		  {"issueDate": "2026-05-01", "sale": {"saleId": 1, "date": "2026-05-01", "lines": [{"netPrice": -1, "vat": 0, "vatType": "HIGH"}]}},
		  {"sale": {"saleId": 2, "lines": [{"netPrice": -1, "vat": 0, "vatType": "HIGH"}]}},
		  {"issueDate": "2026-03-05", "sale": {"saleId": 3, "date": "2026-03-05", "lines": [{"netPrice": -1, "vat": 0, "vatType": "HIGH"}]}},
		  {"issueDate": "2026-03-06", "lines": [{"net": -100, "vat": -25, "vatInPercent": 25}]}
		]`), win, map[int64]bool{3: true})
		if len(lines) != 0 || counted != 0 || withoutLines != 1 || undated != 1 {
			t.Fatalf("lines=%d counted=%d withoutLines=%d undated=%d; want 0/0/1/1", len(lines), counted, withoutLines, undated)
		}
	})
	t.Run("the note's issue date places a sale that has no date", func(t *testing.T) {
		_, counted, _, _ := creditNoteLines(notes(`[{"issueDate": "2026-03-05", "sale": {"saleId": 1, "lines": [{"netPrice": -1, "vat": 0, "vatType": "HIGH"}]}}]`), win, nil)
		if counted != 1 {
			t.Fatalf("counted = %d, want 1", counted)
		}
	})
}

func TestLedgerVAT(t *testing.T) {
	win := Window{From: "2026-03-01", To: "2026-04-30"}
	entries := txRows(t, `[
	  {"journalEntryId": 1, "date": "2026-03-10", "transactionType": "Salg", "lines": [{"amount": -2500, "account": "2701"}, {"amount": -100, "account": "2703:5"}]},
	  {"journalEntryId": 2, "date": "2026-03-11", "transactionType": "Kjøp", "lines": [{"amount": 1000, "account": "2711"}, {"amount": -300, "account": "2702"}, {"amount": 300, "account": "2712"}]},
	  {"journalEntryId": 3, "date": "2026-04-30", "transactionType": "Mva-oppgjør", "lines": [{"amount": 2900, "account": "2701"}, {"amount": -1300, "account": "2711"}]},
	  {"journalEntryId": 4, "date": "2026-03-12", "transactionType": "Fri", "lines": [{"amount": -700, "account": "2701"}, {"amount": 9999, "account": "2740"}, {"amount": 5, "account": "27"}]},
	  {"journalEntryId": 5, "date": "2026-05-01", "transactionType": "Salg", "lines": [{"amount": -9000, "account": "2701"}]},
	  {"journalEntryId": 1, "date": "2026-03-10", "lines": [{"amount": -2500, "account": "2701"}, {"amount": -100, "account": "2703:5"}]},
	  {"journalEntryId": 3, "date": "2026-04-30", "lines": [{"amount": 2900, "account": "2701"}, {"amount": -1300, "account": "2711"}]},
	  {"journalEntryId": 6, "date": "2026-03-13", "transactionType": "Betalingsoppgjør", "lines": [{"amount": 100, "account": "1920"}]}
	]`)
	l := ledgerVAT(entries, win)
	// 2500 + 100 + 300 + 700 of output; 1000 + 300 of input. The settlement
	// entry, the entry outside the window, 2740 and the repeated copies of
	// entries 1 and 3 (one of them an untyped copy of the settlement) are out.
	if l.OutputVATOre != 3600 || l.InputVATOre != 1300 || l.NetVATOre != 2300 {
		t.Fatalf("ledger = %+v, want output 3600, input 1300, net 2300", l)
	}
	if !l.Compared || l.JournalEntries != 4 {
		t.Fatalf("journal entries = %d compared = %v, want 4 entries in the window", l.JournalEntries, l.Compared)
	}
	byType := map[string]mvaLedgerType{}
	for _, bt := range l.ByType {
		byType[bt.TransactionType] = bt
	}
	if len(byType) != 3 || !byType["Salg"].FromDocuments || !byType["Kjøp"].FromDocuments || byType["Fri"].FromDocuments || byType["Fri"].OutputVATOre != 700 {
		t.Fatalf("by_type = %+v, want Salg and Kjøp from documents and Fri (700 øre) not", l.ByType)
	}

	if empty := ledgerVAT(nil, win); empty.Compared || empty.ByType == nil {
		t.Fatalf("an empty journal must read as not compared with an empty by_type: %+v", empty)
	}
}

func TestCrossCheckLedger(t *testing.T) {
	tests := []struct {
		name           string
		out, in        int64
		ledger         mvaLedger
		wantFinding    bool
		wantImpactOre  int64
		wantLedgerDiff int64
	}{
		{"agrees", 5000, 2000, mvaLedger{Compared: true, NetVATOre: 3000}, false, 0, 0},
		{"under a krone is rounding", 5000, 2000, mvaLedger{Compared: true, NetVATOre: 2901}, false, 0, 99},
		{"over a krone is a finding", 5000, 2000, mvaLedger{Compared: true, NetVATOre: 2800}, true, 200, 200},
		{"the summary can be short as well", 5000, 2000, mvaLedger{Compared: true, NetVATOre: 9000}, true, -6000, -6000},
		{"nothing to compare, nothing reported", 5000, 2000, mvaLedger{}, false, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ledger := tt.ledger
			got := crossCheckLedger(tt.out, tt.in, &ledger)
			if (len(got) == 1) != tt.wantFinding {
				t.Fatalf("findings = %+v, want finding = %v", got, tt.wantFinding)
			}
			if ledger.DiffOre != tt.wantLedgerDiff {
				t.Fatalf("diff = %d, want %d", ledger.DiffOre, tt.wantLedgerDiff)
			}
			if tt.wantFinding && (got[0].Kind != "ledger_vat_mismatch" || got[0].ImpactOre != tt.wantImpactOre) {
				t.Fatalf("finding = %+v, want impact %d", got[0], tt.wantImpactOre)
			}
		})
	}
}

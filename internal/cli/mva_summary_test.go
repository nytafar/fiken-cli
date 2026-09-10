// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the pure MVA summarizer.
package cli

import (
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

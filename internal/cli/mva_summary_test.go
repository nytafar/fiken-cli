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

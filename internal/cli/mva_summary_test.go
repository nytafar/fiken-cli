// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the pure MVA summarizer.
package cli

import "testing"

func TestSummarizeMVA_Empty(t *testing.T) {
	buckets, total := summarizeMVA(nil)
	if len(buckets) != 0 || total != 0 {
		t.Fatalf("empty summary = %+v total %d; want no buckets, 0 total", buckets, total)
	}
}

func TestSummarizeMVA_BucketsByType(t *testing.T) {
	lines := []mvaLine{
		{VATType: "HIGH", BasisOre: 10000, VATOre: 2500},
		{VATType: "HIGH", BasisOre: 5000, VATOre: 1250},
		{VATType: "LOW", BasisOre: 2000, VATOre: 240},
		{VATType: "NONE", BasisOre: 800, VATOre: 0},
	}
	buckets, total := summarizeMVA(lines)
	if len(buckets) != 3 {
		t.Fatalf("got %d buckets %+v, want 3 (HIGH, LOW, NONE)", len(buckets), buckets)
	}
	// Sorted by vatType: HIGH, LOW, NONE.
	if buckets[0].VATType != "HIGH" || buckets[0].BasisOre != 15000 || buckets[0].VATOre != 3750 {
		t.Errorf("HIGH bucket = %+v; want basis 15000 vat 3750", buckets[0])
	}
	if buckets[1].VATType != "LOW" || buckets[1].VATOre != 240 {
		t.Errorf("LOW bucket = %+v; want vat 240", buckets[1])
	}
	if buckets[2].VATType != "NONE" || buckets[2].VATOre != 0 {
		t.Errorf("NONE bucket = %+v; want vat 0", buckets[2])
	}
	if total != 3990 {
		t.Errorf("total VAT = %d, want 3990", total)
	}
	// kr string formatting is part of the contract.
	if buckets[0].VAT != "37.50" {
		t.Errorf("HIGH vat kr = %q, want 37.50", buckets[0].VAT)
	}
}

func TestSummarizeMVA_NetPosition(t *testing.T) {
	_, outTotal := summarizeMVA([]mvaLine{{VATType: "HIGH", BasisOre: 10000, VATOre: 2500}})
	_, inTotal := summarizeMVA([]mvaLine{{VATType: "HIGH", BasisOre: 4000, VATOre: 1000}})
	if net := outTotal - inTotal; net != 1500 {
		t.Errorf("net VAT = %d, want 1500", net)
	}
}

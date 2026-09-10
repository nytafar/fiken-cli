// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the pure vendor-profile
// aggregator.
package cli

import "testing"

func TestBuildVendorProfile_Empty(t *testing.T) {
	count, total, last, acct, vat, combos := buildVendorProfile(nil)
	if count != 0 || total != 0 || last != "" || acct != "" || vat != "" || len(combos) != 0 {
		t.Fatalf("empty profile = count %d total %d last %q acct %q vat %q combos %+v; want all zero",
			count, total, last, acct, vat, combos)
	}
}

func TestBuildVendorProfile_Aggregates(t *testing.T) {
	purchases := []vendorProfilePurchase{
		{Date: "2026-01-01", GrossOre: 10000, Lines: []vendorProfileLine{{Account: "6300:1", VATType: "HIGH"}}},
		{Date: "2026-03-15", GrossOre: 5000, Lines: []vendorProfileLine{{Account: "6300:1", VATType: "HIGH"}}},
		{Date: "2026-02-01", GrossOre: 2500, Lines: []vendorProfileLine{{Account: "7140:1", VATType: "NONE"}}},
	}
	count, total, last, acct, vat, combos := buildVendorProfile(purchases)
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
	if total != 17500 {
		t.Errorf("total = %d, want 17500", total)
	}
	if last != "2026-03-15" {
		t.Errorf("lastDate = %q, want 2026-03-15", last)
	}
	if acct != "6300:1" {
		t.Errorf("modalAccount = %q, want 6300:1 (2 of 3 lines)", acct)
	}
	if vat != "HIGH" {
		t.Errorf("modalVAT = %q, want HIGH", vat)
	}
	if len(combos) == 0 || combos[0].Account != "6300:1" || combos[0].VATType != "HIGH" || combos[0].Count != 2 {
		t.Errorf("top combo = %+v, want {6300:1 HIGH 2}", combos)
	}
}

func TestBuildVendorProfile_TopThreeCap(t *testing.T) {
	// Four distinct combos -> only the top 3 are returned.
	purchases := []vendorProfilePurchase{
		{Date: "2026-01-01", GrossOre: 1, Lines: []vendorProfileLine{{Account: "a", VATType: "HIGH"}}},
		{Date: "2026-01-02", GrossOre: 1, Lines: []vendorProfileLine{{Account: "b", VATType: "HIGH"}}},
		{Date: "2026-01-03", GrossOre: 1, Lines: []vendorProfileLine{{Account: "c", VATType: "HIGH"}}},
		{Date: "2026-01-04", GrossOre: 1, Lines: []vendorProfileLine{{Account: "d", VATType: "HIGH"}}},
	}
	_, _, _, _, _, combos := buildVendorProfile(purchases)
	if len(combos) != 3 {
		t.Fatalf("combos length = %d, want 3 (capped)", len(combos))
	}
}

func TestBuildVendorProfile_ModalIsRecencyBounded(t *testing.T) {
	// Three old purchases on 6300:1/HIGH and two recent ones on 7140:1/NONE:
	// the modals follow the recent practice, while the descriptive top-3 combos
	// still rank the whole window.
	purchases := []vendorProfilePurchase{
		{Date: "2023-01-01", GrossOre: 1, Lines: []vendorProfileLine{{Account: "6300:1", VATType: "HIGH"}}},
		{Date: "2023-06-01", GrossOre: 1, Lines: []vendorProfileLine{{Account: "6300:1", VATType: "HIGH"}}},
		{Date: "2023-09-01", GrossOre: 1, Lines: []vendorProfileLine{{Account: "6300:1", VATType: "HIGH"}}},
		{Date: "2026-02-01", GrossOre: 1, Lines: []vendorProfileLine{{Account: "7140:1", VATType: "NONE"}}},
		{Date: "2026-05-01", GrossOre: 1, Lines: []vendorProfileLine{{Account: "7140:1", VATType: "NONE"}}},
	}
	_, _, last, acct, vat, combos := buildVendorProfile(purchases)
	if last != "2026-05-01" {
		t.Fatalf("lastDate = %q, want 2026-05-01", last)
	}
	if acct != "7140:1" || vat != "NONE" {
		t.Errorf("modals = %q/%q; want 7140:1/NONE (12 months up to the last purchase)", acct, vat)
	}
	if len(combos) != 2 || combos[0].Account != "6300:1" || combos[0].Count != 3 {
		t.Errorf("combos = %+v; want the whole-window ranking led by 6300:1 ×3", combos)
	}
}

func TestBuildVendorProfile_ModalIncludesTheLatestPurchase(t *testing.T) {
	// `at` is the day after the last purchase, so that purchase votes too.
	purchases := []vendorProfilePurchase{
		{Date: "2026-05-01", GrossOre: 1, Lines: []vendorProfileLine{{Account: "7140:1", VATType: "NONE"}}},
	}
	_, _, _, acct, vat, _ := buildVendorProfile(purchases)
	if acct != "7140:1" || vat != "NONE" {
		t.Errorf("modals = %q/%q; want 7140:1/NONE", acct, vat)
	}
}

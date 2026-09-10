// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the pure rollup aggregator.
package cli

import (
	"encoding/json"
	"testing"
)

func TestAggregateRollup_Empty(t *testing.T) {
	if got := aggregateRollup(nil); len(got) != 0 {
		t.Fatalf("got %d rows, want 0", len(got))
	}
}

func TestAggregateRollup_GroupsAndMargin(t *testing.T) {
	contribs := []rollupContribution{
		{Key: "2026-01", Label: "2026-01", SalesOre: 10000},
		{Key: "2026-01", Label: "2026-01", PurchasesOre: 4000},
		{Key: "2026-02", Label: "2026-02", SalesOre: 5000},
	}
	rows := aggregateRollup(contribs)
	if len(rows) != 2 {
		t.Fatalf("got %d rows %+v, want 2", len(rows), rows)
	}
	// Sorted by key: 2026-01 first.
	if rows[0].Key != "2026-01" {
		t.Fatalf("first key = %q, want 2026-01", rows[0].Key)
	}
	if rows[0].SalesOre != 10000 || rows[0].PurchasesOre != 4000 {
		t.Errorf("row0 sales/purch = %d/%d, want 10000/4000", rows[0].SalesOre, rows[0].PurchasesOre)
	}
	if rows[0].MarginOre != 6000 {
		t.Errorf("row0 margin = %d, want 6000", rows[0].MarginOre)
	}
	if rows[0].Margin != "60.00" {
		t.Errorf("row0 margin kr = %q, want 60.00", rows[0].Margin)
	}
	if rows[1].Key != "2026-02" || rows[1].MarginOre != 5000 {
		t.Errorf("row1 = %+v; want key 2026-02 margin 5000", rows[1])
	}
}

func TestAggregateRollup_NegativeMargin(t *testing.T) {
	// Purchases exceeding sales for a group -> negative margin.
	contribs := []rollupContribution{
		{Key: "6300:1", Label: "6300:1", PurchasesOre: 9000},
		{Key: "6300:1", Label: "6300:1", SalesOre: 1000},
	}
	rows := aggregateRollup(contribs)
	if len(rows) != 1 || rows[0].MarginOre != -8000 {
		t.Fatalf("got %+v; want single row margin -8000", rows)
	}
	if rows[0].Margin != "-80.00" {
		t.Errorf("margin kr = %q, want -80.00", rows[0].Margin)
	}
}

func TestMonthKey(t *testing.T) {
	cases := map[string]string{
		"2026-05-17": "2026-05",
		"2026-12-01": "2026-12",
		"2026-05":    "2026-05",
		"":           "",
		"bad":        "bad",
	}
	for in, want := range cases {
		if got := monthKey(in); got != want {
			t.Errorf("monthKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// The gross a purchase contributes is regime-dependent: net+vat only for
// ordinary domestic VAT. A direct line's gross is the VAT alone, a basis or
// nondeductible line's is its net, and an unrecognised vatType falls back to
// net+vat.
func TestPurchaseGross_RegimeAware(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want int64
	}{
		{"ordinary domestic", `{"lines":[{"vatType":"HIGH","netPrice":100000,"vat":25000}]}`, 125000},
		{"reverse-charge basis", `{"lines":[{"vatType":"HIGH_FOREIGN_SERVICE_DEDUCTIBLE","netPrice":200000,"vat":0}]}`, 200000},
		{"direct: the VAT is the whole invoice line", `{"lines":[{"vatType":"MEDIUM_DIRECT","netPrice":0,"vat":43210}]}`, 43210},
		{"nondeductible: net is already inclusive, never net−vat", `{"lines":[{"vatType":"HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE","netPrice":125000,"vat":-25000}]}`, 125000},
		{"zero-rated", `{"lines":[{"vatType":"NONE","netPrice":80000,"vat":0}]}`, 80000},
		{"unknown type falls back to net+vat", `{"lines":[{"vatType":"HIGH_INVENTED","netPrice":10000,"vat":2500}]}`, 12500},
		{"mixed lines sum per regime", `{"lines":[{"vatType":"HIGH","netPrice":100000,"vat":25000},{"vatType":"HIGH_DIRECT","netPrice":0,"vat":5000}]}`, 130000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var doc map[string]json.RawMessage
			if err := json.Unmarshal([]byte(c.doc), &doc); err != nil {
				t.Fatalf("bad fixture: %v", err)
			}
			if got := purchaseGross(doc); got != c.want {
				t.Errorf("purchaseGross = %d, want %d", got, c.want)
			}
		})
	}
}

func TestRollupTotals(t *testing.T) {
	tests := []struct {
		name                     string
		rows                     []rollupRow
		sales, purchases, margin int64
	}{
		{name: "empty"},
		{
			name:      "sums both sides and derives margin",
			rows:      []rollupRow{{SalesOre: 10000, PurchasesOre: 4000}, {SalesOre: 2500, PurchasesOre: 500}},
			sales:     12500,
			purchases: 4500,
			margin:    8000,
		},
		{
			name:      "negative margin",
			rows:      []rollupRow{{SalesOre: 1000, PurchasesOre: 9000}},
			sales:     1000,
			purchases: 9000,
			margin:    -8000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, p, m := rollupTotals(tt.rows)
			if s != tt.sales || p != tt.purchases || m != tt.margin {
				t.Fatalf("got (%d, %d, %d), want (%d, %d, %d)", s, p, m, tt.sales, tt.purchases, tt.margin)
			}
		})
	}
}

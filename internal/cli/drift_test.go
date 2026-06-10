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
			name: "NONE vatType line is never rate-checked",
			docs: []driftScanDoc{{
				DocType: "purchase", DocID: 8, Date: "2026-02-02",
				Lines: []driftScanLine{{Account: "4000:1", VATType: "NONE", NetPrice: 10000, VAT: 999}},
			}},
			tolerance: 1,
			wantKinds: nil,
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
			got := driftScan(tt.docs, tt.tolerance)
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

func TestVatRateForType(t *testing.T) {
	cases := map[string]float64{
		"HIGH":        0.25,
		"HIGH_DIRECT": 0.25,
		"MEDIUM":      0.15,
		"LOW":         0.12,
		"RAW_FISH":    0.1111,
		"NONE":        0,
		"EXEMPT":      0,
		"":            0,
	}
	for in, want := range cases {
		if got := vatRateForType(in); got != want {
			t.Errorf("vatRateForType(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRoundOre(t *testing.T) {
	cases := []struct {
		in   float64
		want int64
	}{
		{83.25, 83}, {83.5, 84}, {2512.5, 2513}, {-83.5, -84}, {0, 0},
	}
	for _, c := range cases {
		if got := roundOre(c.in); got != c.want {
			t.Errorf("roundOre(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

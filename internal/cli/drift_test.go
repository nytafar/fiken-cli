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

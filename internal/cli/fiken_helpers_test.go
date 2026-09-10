// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the shared helpers, notably
// the recency-bounded modal that vat-anomaly, vendor-profile and prepare share.
package cli

import "testing"

func TestRecentModal(t *testing.T) {
	tests := []struct {
		name        string
		obs         []dated
		at          string
		months      int
		wantValue   string
		wantSupport int
	}{
		{name: "no observations", at: "2026-06-01", months: 12},
		{
			name: "plain majority",
			obs: []dated{
				{"2026-01-10", "HIGH"}, {"2026-02-10", "HIGH"}, {"2026-03-10", "HIGH"},
				{"2026-04-10", "LOW"},
			},
			at: "2026-06-01", months: 12, wantValue: "HIGH", wantSupport: 3,
		},
		{
			name: "tie broken by lexicographically smallest value",
			obs: []dated{
				{"2026-01-10", "LOW"}, {"2026-02-10", "LOW"},
				{"2026-03-10", "HIGH"}, {"2026-04-10", "HIGH"},
			},
			at: "2026-06-01", months: 12, wantValue: "HIGH", wantSupport: 2,
		},
		{
			name: "observations older than the window do not vote",
			obs: []dated{
				{"2024-01-10", "OLD"}, {"2024-02-10", "OLD"}, {"2024-03-10", "OLD"},
				{"2026-05-10", "NEW"},
			},
			at: "2026-06-01", months: 12, wantValue: "NEW", wantSupport: 1,
		},
		{
			name:      "window edge is inclusive at at-months",
			obs:       []dated{{"2025-06-01", "EDGE"}},
			at:        "2026-06-01",
			months:    12,
			wantValue: "EDGE", wantSupport: 1,
		},
		{
			name:   "the day before the edge is out",
			obs:    []dated{{"2025-05-31", "EDGE"}},
			at:     "2026-06-01",
			months: 12,
		},
		{
			name:   "at itself is exclusive, so same-day siblings do not vote",
			obs:    []dated{{"2026-06-01", "SAME"}, {"2026-06-02", "LATER"}},
			at:     "2026-06-01",
			months: 12,
		},
		{
			name:   "empty values and unparseable dates are ignored",
			obs:    []dated{{"2026-01-01", ""}, {"", "HIGH"}, {"not-a-date", "HIGH"}},
			at:     "2026-06-01",
			months: 12,
		},
		{
			name:   "unparseable at yields nothing",
			obs:    []dated{{"2026-01-01", "HIGH"}},
			at:     "",
			months: 12,
		},
		{
			name:      "months <= 0 means the whole history before at",
			obs:       []dated{{"2014-01-10", "OLD"}, {"2014-02-10", "OLD"}, {"2026-05-10", "NEW"}},
			at:        "2026-06-01",
			months:    0,
			wantValue: "OLD", wantSupport: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotValue, gotSupport := recentModal(tc.obs, tc.at, tc.months)
			if gotValue != tc.wantValue || gotSupport != tc.wantSupport {
				t.Errorf("recentModal = %q,%d; want %q,%d", gotValue, gotSupport, tc.wantValue, tc.wantSupport)
			}
		})
	}
}

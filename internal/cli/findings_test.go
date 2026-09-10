// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the shared finding envelope:
// the period resolver, the window predicate, the sort/summarize/limit pipeline
// and the text renderer.
package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// pinNow fixes the resolver's clock for the duration of a test.
func pinNow(t *testing.T, iso string) {
	t.Helper()
	ts, err := time.Parse("2006-01-02", iso)
	if err != nil {
		t.Fatalf("bad pinned date %q: %v", iso, err)
	}
	prev := nowFunc
	nowFunc = func() time.Time { return ts }
	t.Cleanup(func() { nowFunc = prev })
}

func TestResolvePeriod(t *testing.T) {
	tests := []struct {
		name       string
		flag       string
		wantFrom   string
		wantTo     string
		wantSource string
		wantErr    bool
	}{
		{name: "unset defaults to the current calendar year", flag: "", wantFrom: "2026-01-01", wantTo: "2026-12-31", wantSource: "default_current_year"},
		{name: "whitespace is still unset", flag: "  ", wantFrom: "2026-01-01", wantTo: "2026-12-31", wantSource: "default_current_year"},
		{name: "all is unbounded", flag: "all", wantSource: "all"},
		{name: "ALL with padding is unbounded too", flag: "ALL ", wantSource: "all"},
		{name: "a quarter delegates to parsePeriod", flag: "2026-Q2", wantFrom: "2026-04-01", wantTo: "2026-06-30", wantSource: "flag"},
		{name: "a year delegates to parsePeriod", flag: "2025", wantFrom: "2025-01-01", wantTo: "2025-12-31", wantSource: "flag"},
		{name: "an explicit range delegates to parsePeriod", flag: "2026-03-01:2026-03-15", wantFrom: "2026-03-01", wantTo: "2026-03-15", wantSource: "flag"},
		{name: "an invalid token errors", flag: "not-a-period", wantErr: true},
		{name: "an invalid quarter errors", flag: "2026-Q9", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pinNow(t, "2026-09-10")
			got, err := resolvePeriod(tt.flag)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolvePeriod(%q) = %+v, want an error", tt.flag, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePeriod(%q): %v", tt.flag, err)
			}
			if got.From != tt.wantFrom || got.To != tt.wantTo || got.Source != tt.wantSource {
				t.Errorf("resolvePeriod(%q) = %+v, want {%q %q %q}", tt.flag, got, tt.wantFrom, tt.wantTo, tt.wantSource)
			}
		})
	}
}

func TestWindowContains(t *testing.T) {
	bounded := Window{From: "2026-01-01", To: "2026-12-31"}
	tests := []struct {
		name string
		win  Window
		date string
		want bool
	}{
		{"an empty date is never contained", bounded, "", false},
		{"an empty date is not contained by the unbounded window either", Window{Source: "all"}, "", false},
		{"the lower bound is inclusive", bounded, "2026-01-01", true},
		{"the upper bound is inclusive", bounded, "2026-12-31", true},
		{"a day before the window is out", bounded, "2025-12-31", false},
		{"a day after the window is out", bounded, "2027-01-01", false},
		{"an unbounded window contains any date", Window{Source: "all"}, "1999-05-05", true},
		{"only a lower bound is open-ended above", Window{From: "2026-01-01"}, "2099-01-01", true},
		{"only a lower bound still rejects earlier", Window{From: "2026-01-01"}, "2025-12-31", false},
		{"only an upper bound is open-ended below", Window{To: "2026-12-31"}, "1999-01-01", true},
		{"only an upper bound still rejects later", Window{To: "2026-12-31"}, "2027-01-01", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.win.Contains(tt.date); got != tt.want {
				t.Errorf("%+v.Contains(%q) = %v, want %v", tt.win, tt.date, got, tt.want)
			}
		})
	}
}

func TestSortFindings(t *testing.T) {
	fs := []Finding{
		{Kind: "b", DocType: "sale", DocID: 2, Date: "2026-03-01", ImpactOre: 100},
		{Kind: "a", DocType: "sale", DocID: 1, Date: "2026-01-01", ImpactOre: -100},
		{Kind: "c", DocType: "purchase", DocID: 3, Date: "2026-02-01", ImpactOre: -900},
		{Kind: "d", DocType: "purchase", DocID: 4, Date: "2026-02-01", ImpactOre: 900},
	}
	SortFindings(fs)
	var got []int64
	for _, f := range fs {
		got = append(got, f.DocID)
	}
	// |900| first (tie broken by date, then docType, then docID: both are
	// 2026-02-01 purchases, so 3 before 4), then |100| (2026-01-01 before
	// 2026-03-01).
	want := []int64{3, 4, 1, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestSummarize(t *testing.T) {
	keyFn := func(f Finding) map[string]string {
		return map[string]string{"kind": f.Kind, "vat_type": f.VATType}
	}
	fs := []Finding{
		{Kind: "vat_rate", VATType: "HIGH", Severity: SeverityWarning, ImpactOre: 100},
		{Kind: "vat_rate", VATType: "HIGH", Severity: SeverityError, ImpactOre: 200},
		{Kind: "total_mismatch", Severity: SeverityWarning, ImpactOre: -5000},
		{Kind: "total_mismatch", Severity: SeverityInfo, ImpactOre: -1000},
	}
	groups := Summarize(fs, keyFn)
	if len(groups) != 2 {
		t.Fatalf("got %d groups %+v, want 2", len(groups), groups)
	}
	// Biggest |impact| first: total_mismatch sums to -6000.
	if groups[0].Key["kind"] != "total_mismatch" {
		t.Errorf("groups[0].Key = %v, want kind=total_mismatch first", groups[0].Key)
	}
	if _, ok := groups[0].Key["vat_type"]; ok {
		t.Errorf("groups[0].Key = %v, want the empty vat_type dropped", groups[0].Key)
	}
	if groups[0].Count != 2 || groups[0].ImpactOre != -6000 {
		t.Errorf("groups[0] = %+v, want count 2 impact -6000", groups[0])
	}
	if groups[0].Severity != SeverityWarning {
		t.Errorf("groups[0].Severity = %q, want the worst of its members (warning)", groups[0].Severity)
	}
	if groups[1].Count != 2 || groups[1].ImpactOre != 300 {
		t.Errorf("groups[1] = %+v, want count 2 impact 300", groups[1])
	}
	if groups[1].Severity != SeverityError {
		t.Errorf("groups[1].Severity = %q, want error (the worst member)", groups[1].Severity)
	}
	if got := Summarize(nil, keyFn); got != nil {
		t.Errorf("Summarize(nil) = %+v, want nil", got)
	}
}

func TestBuildReport(t *testing.T) {
	keyFn := func(f Finding) map[string]string { return map[string]string{"kind": f.Kind} }
	fs := []Finding{
		{Kind: "a", Severity: SeverityError, DocID: 1, Date: "2026-01-01", ImpactOre: 10},
		{Kind: "a", Severity: SeverityError, DocID: 2, Date: "2026-01-02", ImpactOre: -5000},
		{Kind: "b", Severity: SeverityInfo, DocID: 3, Date: "2026-01-03", ImpactOre: 900},
	}
	tests := []struct {
		name        string
		minImpact   int64
		limit       int
		wantTotal   int
		wantShown   int
		wantGroups  int
		wantFirstID int64
	}{
		{name: "no threshold, no truncation", minImpact: 0, limit: 200, wantTotal: 3, wantShown: 3, wantGroups: 2, wantFirstID: 2},
		{name: "min-impact drops the small one and its group", minImpact: 100, limit: 200, wantTotal: 2, wantShown: 2, wantGroups: 2, wantFirstID: 2},
		{name: "min-impact can empty a group", minImpact: 1000, limit: 200, wantTotal: 1, wantShown: 1, wantGroups: 1, wantFirstID: 2},
		{name: "limit truncates findings but not total or summary", minImpact: 0, limit: 1, wantTotal: 3, wantShown: 1, wantGroups: 2, wantFirstID: 2},
		{name: "limit 0 means no truncation", minImpact: 0, limit: 0, wantTotal: 3, wantShown: 3, wantGroups: 2, wantFirstID: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := make([]Finding, len(fs))
			copy(in, fs)
			r := buildReport(Report{Company: "agensia", Detector: "drift"}, in, keyFn, tt.minImpact, tt.limit)
			if r.Total != tt.wantTotal {
				t.Errorf("Total = %d, want %d", r.Total, tt.wantTotal)
			}
			if len(r.Findings) != tt.wantShown {
				t.Errorf("len(Findings) = %d, want %d", len(r.Findings), tt.wantShown)
			}
			if len(r.Summary) != tt.wantGroups {
				t.Errorf("len(Summary) = %d, want %d", len(r.Summary), tt.wantGroups)
			}
			if len(r.Findings) > 0 && r.Findings[0].DocID != tt.wantFirstID {
				t.Errorf("Findings[0].DocID = %d, want %d (biggest |impact| first)", r.Findings[0].DocID, tt.wantFirstID)
			}
		})
	}
}

func TestRenderText(t *testing.T) {
	keyFn := func(f Finding) map[string]string { return map[string]string{"kind": f.Kind} }
	fs := []Finding{
		{Kind: "vat_rate", Severity: SeverityWarning, DocType: "purchase", DocID: 7, Date: "2026-02-01", ImpactOre: -1500,
			Account: "4000:1", VATType: "HIGH", ContactName: "Acme AS", Detail: map[string]any{"note": "vat differs"}},
		{Kind: "basis_has_vat", Severity: SeverityError, DocType: "purchase", DocID: 8, Date: "2026-02-02", ImpactOre: 500},
	}
	r := buildReport(Report{
		Company:  "agensia",
		Detector: "drift",
		Window:   Window{From: "2026-01-01", To: "2026-12-31", Source: "default_current_year"},
		Undated:  2,
		Params:   map[string]any{"tolerance_ore": int64(5)},
	}, fs, keyFn, 0, 1)

	var buf bytes.Buffer
	RenderText(&buf, r, 1)
	out := buf.String()
	for _, want := range []string{
		"drift — agensia — 2026-01-01..2026-12-31 (default_current_year)",
		"2 findings, 2 undated document(s) skipped",
		"tolerance_ore 5",
		"Summary",
		"kind=vat_rate",
		"Findings (1 of 2)",
		"purchase #7",
		"-15.00 kr",
		"account 4000:1  vatType HIGH  Acme AS  vat differs",
		"1 more findings not shown (--limit 1).",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "#8") {
		t.Errorf("output shows the truncated finding:\n%s", out)
	}
}

func TestRenderTextEmpty(t *testing.T) {
	var buf bytes.Buffer
	RenderText(&buf, Report{Company: "agensia", Detector: "missing_bilag", Window: Window{Source: "all"}}, 200)
	out := buf.String()
	if !strings.Contains(out, "all dates (all)") || !strings.Contains(out, "Nothing found.") {
		t.Errorf("empty report rendered as:\n%s", out)
	}
}

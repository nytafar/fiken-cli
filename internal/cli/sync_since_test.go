// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Pins the incremental window (issue #13): which resources carry
// lastModifiedGe, what value goes on the wire for --since and for the stored
// watermark, that a resource without a filter still says so out loud, and that
// a windowed pull stays out of the recorded result count and out of any future
// deletion pass. Reuses the pageServer / rowServer harnesses from
// pagination_zero_based_test.go and pagination_headers_test.go.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

// contactRows builds dependent rows the contacts upsert path can key on.
func contactRows(n int) []string {
	rows := make([]string, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, fmt.Sprintf(`{"contactId":%d,"name":"Contact %d","lastModifiedDate":"2026-09-10"}`, 1000+i, i))
	}
	return rows
}

// contactsDep is the dependent definition the real sync pass uses.
func contactsDep() dependentResourceDef {
	return dependentResourceDef{
		Name:          "contacts",
		ParentTable:   "companies",
		ParentIDParam: "companySlug",
		PathTemplate:  "/companies/{companySlug}/contacts",
		KeyField:      "slug",
		PathParams:    []dependentPathParamDef{{Param: "companySlug", Field: "slug"}},
	}
}

func accountsDep() dependentResourceDef {
	return dependentResourceDef{
		Name:          "accounts",
		ParentTable:   "companies",
		ParentIDParam: "companySlug",
		PathTemplate:  "/companies/{companySlug}/accounts",
		KeyField:      "slug",
		PathParams:    []dependentPathParamDef{{Param: "companySlug", Field: "slug"}},
	}
}

// seedCompanies puts parent rows in the mirror so a dependent walk has
// something to iterate.
func seedCompanies(t *testing.T, db interface {
	UpsertBatch(string, []json.RawMessage) (int, int, error)
}, slugs ...string) {
	t.Helper()
	items := make([]json.RawMessage, 0, len(slugs))
	for i, slug := range slugs {
		items = append(items, json.RawMessage(fmt.Sprintf(`{"slug":%q,"name":"Company %d"}`, slug, i)))
	}
	if _, _, err := db.UpsertBatch("companies", items); err != nil {
		t.Fatalf("seed companies: %v", err)
	}
}

// warningsWithReason returns the sync_warning events on an NDJSON stream that
// carry the given reason.
func warningsWithReason(events, reason string) []map[string]any {
	var out []map[string]any
	for _, line := range strings.Split(events, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev["event"] == "sync_warning" && ev["reason"] == reason {
			out = append(out, ev)
		}
	}
	return out
}

// dayBefore is what the wire value must be for a timestamp: its own day,
// stepped back one.
func dayBefore(t *testing.T, ts time.Time) string {
	t.Helper()
	day, err := time.Parse("2006-01-02", ts.Format("2006-01-02"))
	if err != nil {
		t.Fatalf("parse day of %s: %v", ts, err)
	}
	return day.AddDate(0, 0, -1).Format("2006-01-02")
}

// dateParamsOf returns every query key that looks like a date filter, so a
// test can assert the window is the ONLY one the request carries.
func dateParamsOf(q url.Values) []string {
	var out []string
	for key := range q {
		if strings.Contains(strings.ToLower(key), "date") || strings.Contains(key, "lastModified") ||
			key == "since" || key == "createdAfter" {
			out = append(out, key)
		}
	}
	return out
}

// TestSyncResourceSinceParam_MapsOnlyTheFilteredResources is the data table
// issue #13 was missing: five synced list operations declare lastModifiedGe in
// spec.yaml and the rest declare no date filter at all. Sending a synthetic
// parameter to the rest would be a 400, which is why "" has to stay "".
func TestSyncResourceSinceParam_MapsOnlyTheFilteredResources(t *testing.T) {
	mapped := []string{"contacts", "journal_entries", "transactions", "products", "sales"}
	for _, resource := range mapped {
		if got := syncResourceSinceParam(resource); got != "lastModifiedGe" {
			t.Errorf("syncResourceSinceParam(%q) = %q, want lastModifiedGe", resource, got)
		}
		if got := syncResourceSinceParamFormat(resource); got != "date" {
			t.Errorf("syncResourceSinceParamFormat(%q) = %q, want date", resource, got)
		}
	}
	// purchases declares no filter and carries no lastModifiedDate; the rest
	// declare no date filter on their list operation.
	for _, resource := range []string{"accounts", "bank_accounts", "companies", "inbox", "projects", "purchases"} {
		if got := syncResourceSinceParam(resource); got != "" {
			t.Errorf("syncResourceSinceParam(%q) = %q, want \"\"", resource, got)
		}
		if got := syncResourceSinceParamFormat(resource); got != "" {
			t.Errorf("syncResourceSinceParamFormat(%q) = %q, want \"\"", resource, got)
		}
	}
}

// TestSyncSinceWindowValue_FloorsToDayAndStepsBackOne pins the value rule:
// lastModifiedGe is inclusive and lastModifiedDate is a day with no zone, so
// the window starts the day before the watermark's own day.
func TestSyncSinceWindowValue_FloorsToDayAndStepsBackOne(t *testing.T) {
	cases := []struct {
		name   string
		value  string
		format string
		want   string
	}{
		{"timestamp floors and steps back", "2026-09-11T14:03:00Z", "date", "2026-09-10"},
		{"day value steps back", "2026-09-11", "date", "2026-09-10"},
		{"month boundary", "2026-09-01T00:00:00Z", "date", "2026-08-31"},
		{"year boundary", "2026-01-01T08:00:00Z", "date", "2025-12-31"},
		{"no format is passed through", "2026-09-11T14:03:00Z", "", "2026-09-11T14:03:00Z"},
		{"unparseable value is passed through", "yesterday", "date", "yesterday"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := syncSinceWindowValue(tc.value, tc.format); got != tc.want {
				t.Fatalf("syncSinceWindowValue(%q, %q) = %q, want %q", tc.value, tc.format, got, tc.want)
			}
		})
	}
}

// TestSyncPullWindow_WindowedPullForbidsDeletion is the contract issue #17 has
// to honour: a pull that asked for a window saw only the rows that changed, so
// "the mirror holds it and the walk did not return it" says nothing.
func TestSyncPullWindow_WindowedPullForbidsDeletion(t *testing.T) {
	full := newSyncPullWindow("", "", nil, "contacts", true)
	if full.Windowed || !full.AllowsDeletion() {
		t.Fatalf("full pull = %+v, want not windowed and deletion-safe", full)
	}
	windowed := newSyncPullWindow("lastModifiedGe", "2026-09-10", nil, "contacts", true)
	if !windowed.Windowed || windowed.AllowsDeletion() {
		t.Fatalf("windowed pull = %+v, want windowed and deletion-unsafe", windowed)
	}
	if windowed.Param != "lastModifiedGe" || windowed.Value != "2026-09-10" {
		t.Fatalf("windowed pull carries %q=%q, want lastModifiedGe=2026-09-10", windowed.Param, windowed.Value)
	}
	// A user filter windows the pull without any since value at all.
	byUserParam := newSyncPullWindow("", "", &syncUserParams{perResource: map[string]map[string]string{
		"contacts": {"lastModifiedGe": "2026-09-01"},
	}}, "contacts", true)
	if !byUserParam.Windowed || byUserParam.AllowsDeletion() {
		t.Fatalf("--resource-param pull = %+v, want windowed and deletion-unsafe", byUserParam)
	}
}

// TestSyncDependentResource_SinceSendsDayFlooredLastModifiedGe is issue #13's
// acceptance on a mapped resource: `--since 7d` reaches the API as
// lastModifiedGe of (now - 7d, floored to the day, minus one), and nothing else
// about dates rides along.
func TestSyncDependentResource_SinceSendsDayFlooredLastModifiedGe(t *testing.T) {
	ts, err := parseSinceDuration("7d")
	if err != nil {
		t.Fatalf("parseSinceDuration: %v", err)
	}
	want := dayBefore(t, ts)

	rs, c := newRowServer(t, contactRows(2), 100, 2)
	db := openTestStore(t)
	seedCompanies(t, db, "testco")

	var events bytes.Buffer
	res := syncDependentResource(context.Background(), c, db, contactsDep(), ts.Format(time.RFC3339), false, 0, false, nil, &events)
	if res.Err != nil {
		t.Fatalf("syncDependentResource: %v", res.Err)
	}
	params := rs.requestParams()
	if len(params) == 0 {
		t.Fatalf("no request reached the server")
	}
	for i, q := range params {
		if got := q.Get("lastModifiedGe"); got != want {
			t.Fatalf("request %d lastModifiedGe = %q, want %q (params %v)", i, got, want, q)
		}
		if dates := dateParamsOf(q); len(dates) != 1 || dates[0] != "lastModifiedGe" {
			t.Fatalf("request %d carried date params %v, want only lastModifiedGe", i, dates)
		}
	}
	if warns := warningsWithReason(events.String(), "resource_not_incremental"); len(warns) != 0 {
		t.Fatalf("mapped resource warned resource_not_incremental: %s", events.String())
	}
}

// TestSyncDependentResource_UnmappedResourceStillWarnsNotIncremental is the
// other half: accounts has no date filter, so --since sends nothing extra and
// the run says out loud that it degraded to a full pull.
func TestSyncDependentResource_UnmappedResourceStillWarnsNotIncremental(t *testing.T) {
	ts, err := parseSinceDuration("7d")
	if err != nil {
		t.Fatalf("parseSinceDuration: %v", err)
	}
	rs, c := newRowServer(t, []string{`{"code":"1500","name":"Account"}`}, 100, 1)
	db := openTestStore(t)
	seedCompanies(t, db, "testco")

	var events bytes.Buffer
	res := syncDependentResource(context.Background(), c, db, accountsDep(), ts.Format(time.RFC3339), false, 0, false, nil, &events)
	if res.Err != nil {
		t.Fatalf("syncDependentResource: %v", res.Err)
	}
	for i, q := range rs.requestParams() {
		if got := q.Get("lastModifiedGe"); got != "" {
			t.Fatalf("request %d sent lastModifiedGe=%q to an endpoint that declares no date filter", i, got)
		}
		if dates := dateParamsOf(q); len(dates) != 0 {
			t.Fatalf("request %d carried date params %v, want none", i, dates)
		}
	}
	warns := warningsWithReason(events.String(), "resource_not_incremental")
	if len(warns) != 1 {
		t.Fatalf("got %d resource_not_incremental warnings, want 1\n%s", len(warns), events.String())
	}
	if warns[0]["resource"] != "accounts" {
		t.Fatalf("warning names %v, want accounts", warns[0]["resource"])
	}
}

// TestSyncDependentResource_WindowedPullKeepsRecordedResultCount: a windowed
// pull gets the FILTERED Result-Count back, so it must still report a short
// pull as a mismatch and must still leave sync_state.result_count alone —
// otherwise doctor reads rows 577 / result_count 2 for good (issue #15's
// reason for the flag, now reachable through the real #13 mapping).
func TestSyncDependentResource_WindowedPullKeepsRecordedResultCount(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "testco")
	if err := db.SaveSyncResultCount("contacts", 577); err != nil {
		t.Fatalf("SaveSyncResultCount: %v", err)
	}

	ts := time.Date(2026, 9, 11, 14, 3, 0, 0, time.UTC)
	// Claims 3 changed rows, serves 2: short, so the comparison must speak up.
	_, c := newRowServer(t, contactRows(2), 100, 3)
	var events bytes.Buffer
	res := syncDependentResource(context.Background(), c, db, contactsDep(), ts.Format(time.RFC3339), false, 0, false, nil, &events)
	if res.Err != nil {
		t.Fatalf("syncDependentResource: %v", res.Err)
	}
	if anomalies := countAnomalies(t, events.String(), "result_count_mismatch"); len(anomalies) != 1 {
		t.Fatalf("windowed short pull emitted %d result_count_mismatch events, want 1\n%s", len(anomalies), events.String())
	}
	if got, ok := db.SyncResultCount("contacts"); !ok || got != 577 {
		t.Fatalf("recorded result count after a windowed pull = (%d, %v), want (577, true)", got, ok)
	}
}

// TestSyncResource_WatermarkSendsWindowedLastModifiedGe is the automatic path:
// with no --since, a resource that has synced before sends the stored
// last_synced_at as its window, day-floored and stepped back one, and --full
// sends no window at all.
//
// companies is the only flat-walked resource and declares no date filter, so
// the mapping seams stand in for a filtered flat resource; the value rule and
// the watermark plumbing under test are the generic ones.
func TestSyncResource_WatermarkSendsWindowedLastModifiedGe(t *testing.T) {
	prevParam, prevFormat := syncSinceParamResolver, syncSinceParamFormatResolver
	syncSinceParamResolver = func(resource string) string {
		if resource == "companies" {
			return "lastModifiedGe"
		}
		return ""
	}
	syncSinceParamFormatResolver = func(resource string) string {
		if resource == "companies" {
			return "date"
		}
		return ""
	}
	t.Cleanup(func() {
		syncSinceParamResolver, syncSinceParamFormatResolver = prevParam, prevFormat
	})

	db := openTestStore(t)
	seedCompanies(t, db, "co-000", "co-001")
	if err := db.SaveSyncState("companies", "", 2); err != nil {
		t.Fatalf("SaveSyncState: %v", err)
	}
	_, lastSynced, _, err := db.GetSyncState("companies")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if lastSynced.IsZero() {
		t.Fatalf("last_synced_at was not stored")
	}
	want := dayBefore(t, lastSynced)

	rs, c := newRowServer(t, companyRows("co-000", "co-001"), 100, 2)
	if res := syncResource(context.Background(), c, db, "companies", "", false, 0, false, nil, &bytes.Buffer{}); res.Err != nil {
		t.Fatalf("syncResource: %v", res.Err)
	}
	params := rs.requestParams()
	if len(params) == 0 {
		t.Fatalf("no request reached the server")
	}
	if got := params[0].Get("lastModifiedGe"); got != want {
		t.Fatalf("watermark request lastModifiedGe = %q, want %q (params %v)", got, want, params[0])
	}

	// --full is the escape hatch: no window, whatever the watermark says.
	rsFull, cFull := newRowServer(t, companyRows("co-000", "co-001"), 100, 2)
	if res := syncResource(context.Background(), cFull, db, "companies", "", true, 0, false, nil, &bytes.Buffer{}); res.Err != nil {
		t.Fatalf("syncResource --full: %v", res.Err)
	}
	fullParams := rsFull.requestParams()
	if len(fullParams) == 0 {
		t.Fatalf("no request reached the server under --full")
	}
	for i, q := range fullParams {
		if got := q.Get("lastModifiedGe"); got != "" {
			t.Fatalf("--full request %d sent lastModifiedGe=%q, want none", i, got)
		}
	}
}

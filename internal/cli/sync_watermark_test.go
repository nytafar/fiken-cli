// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Pins the default run's incremental window (issue #22): the first pull of a
// (company, resource) pair carries no date filter and leaves a watermark, the
// next one sends lastModifiedGe from that watermark, a --company run moves only
// that company's mark, a pair whose pull failed or came up short keeps its old
// one, and --full ignores and resets it.
//
// The server here is company-aware, unlike the shared rowServer: the whole
// point of the change is that two companies have two independent watermarks, so
// the harness has to be able to serve — and fail — one company at a time.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"fiken-cli/internal/client"
	"fiken-cli/internal/config"
	"fiken-cli/internal/store"
)

// companyServer serves each company's own rows as 0-based pages of bare JSON
// arrays with Fiken's header block, recording the query of every request under
// the company slug from the path. failFor names companies that answer 500.
type companyServer struct {
	rows    map[string][]string
	failFor map[string]bool
	// claimed overrides the Fiken-Api-Result-Count for a company; absent means
	// "claim exactly what is served", i.e. a pull with no mismatch.
	claimed map[string]int

	mu       sync.Mutex
	requests map[string][]url.Values
}

func (s *companyServer) companyOf(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "companies" {
		return ""
	}
	return parts[1]
}

func (s *companyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	slug := s.companyOf(r.URL.Path)
	s.mu.Lock()
	if s.requests == nil {
		s.requests = map[string][]url.Values{}
	}
	s.requests[slug] = append(s.requests[slug], r.URL.Query())
	s.mu.Unlock()

	if s.failFor[slug] {
		http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
		return
	}

	rows := s.rows[slug]
	page := 0
	if raw := r.URL.Query().Get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			http.Error(w, "bad page", http.StatusBadRequest)
			return
		}
		page = n
	}
	size := 100
	if v := r.URL.Query().Get("pageSize"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			size = n
		}
	}
	start := page * size
	if start > len(rows) {
		start = len(rows)
	}
	end := start + size
	if end > len(rows) {
		end = len(rows)
	}
	items := make([]json.RawMessage, 0, end-start)
	for _, row := range rows[start:end] {
		items = append(items, json.RawMessage(row))
	}
	body, _ := json.Marshal(items)

	claimed := len(rows)
	if n, ok := s.claimed[slug]; ok {
		claimed = n
	}
	pageCount := 0
	if size > 0 {
		pageCount = (claimed + size - 1) / size
	}
	w.Header().Set(client.HeaderPage, strconv.Itoa(page))
	w.Header().Set(client.HeaderPageSize, strconv.Itoa(size))
	w.Header().Set(client.HeaderPageCount, strconv.Itoa(pageCount))
	w.Header().Set(client.HeaderResultCount, strconv.Itoa(claimed))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (s *companyServer) queriesFor(slug string) []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]url.Values, len(s.requests[slug]))
	copy(out, s.requests[slug])
	return out
}

func newCompanyServer(t *testing.T, cs *companyServer) *client.Client {
	t.Helper()
	srv := httptest.NewServer(cs)
	t.Cleanup(srv.Close)
	c := client.New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)
	c.NoCache = true
	return c
}

// contactRowsFor builds distinct contact rows for one company.
func contactRowsFor(slug string, n int) []string {
	rows := make([]string, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, fmt.Sprintf(`{"contactId":%d,"name":"%s contact %d","lastModifiedDate":"2026-09-10"}`, hashSlugBase(slug)+i, slug, i))
	}
	return rows
}

// hashSlugBase keeps two companies' contact ids apart without depending on the
// storage key's company suffix.
func hashSlugBase(slug string) int {
	base := 1000
	for _, r := range slug {
		base += int(r)
	}
	return base * 10
}

// windowEvents returns the sync_window events on an NDJSON stream.
func windowEvents(events string) []map[string]any {
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
		if ev["event"] == "sync_window" {
			out = append(out, ev)
		}
	}
	return out
}

// pinRunStart fixes (or clears) the run-start global a completed pull records.
// Tests that call the walker directly need it: the global survives whichever
// `sync` command ran earlier in the same process, and zero means "use the
// walk's own start", which is what a direct call should measure against.
func pinRunStart(t *testing.T, ts time.Time) {
	t.Helper()
	prev := syncRunStartedAt
	syncRunStartedAt = ts
	t.Cleanup(func() { syncRunStartedAt = prev })
}

// unwindowNextPull drops the watermarks a seeding pull left behind, so the pull
// the test is actually about starts from "never pulled" and asks for the whole
// collection.
//
// Deletion detection (issue #17) needs this since issue #22: a second default
// run is windowed from the watermark, and a windowed pull can never conclude
// that an absent row was deleted. In production the same applies — deletions
// are found by the first complete pull of a pair and by `sync --full`, not by
// the cheap daily run.
func unwindowNextPull(t *testing.T, db *store.Store, resource string, companies ...string) {
	t.Helper()
	for _, company := range companies {
		if err := db.ClearSyncWatermark(company, resource); err != nil {
			t.Fatalf("ClearSyncWatermark(%s, %s): %v", company, resource, err)
		}
	}
}

// TestSyncDependentResource_FirstRunIsFullAndLeavesAWatermark: a pair the
// mirror has never completed a pull for has no watermark, so it must ask for
// everything — and then record where it got to, as the run's start in UTC
// RFC3339.
func TestSyncDependentResource_FirstRunIsFullAndLeavesAWatermark(t *testing.T) {
	pinRunStart(t, time.Time{})
	db := openTestStore(t)
	seedCompanies(t, db, "testco")
	cs := &companyServer{rows: map[string][]string{"testco": contactRowsFor("testco", 3)}}
	c := newCompanyServer(t, cs)

	before := time.Now().UTC().Add(-time.Second)
	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), c, db, contactsDep(), "", false, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("syncDependentResource: %v", res.Err)
	}

	queries := cs.queriesFor("testco")
	if len(queries) == 0 {
		t.Fatalf("no request reached the server")
	}
	for i, q := range queries {
		if dates := dateParamsOf(q); len(dates) != 0 {
			t.Fatalf("first run request %d carried date params %v; a pair with no watermark must be pulled in full", i, dates)
		}
	}
	if evs := windowEvents(events.String()); len(evs) != 0 {
		t.Fatalf("first run announced %d sync_window events, want 0: %s", len(evs), events.String())
	}

	mark := db.SyncWatermark("testco", "contacts")
	if mark == "" {
		t.Fatalf("no watermark recorded after a complete first pull")
	}
	ts, err := time.Parse(time.RFC3339, mark)
	if err != nil {
		t.Fatalf("watermark %q is not RFC3339: %v", mark, err)
	}
	if ts.Before(before) || ts.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("watermark %s is not this run's start time", mark)
	}
}

// TestSyncDependentResource_SecondRunSendsTheWatermark: the second run of the
// same pair asks Fiken only for what changed, as lastModifiedGe with the
// watermark's day stepped back one, and says so on the event stream.
func TestSyncDependentResource_SecondRunSendsTheWatermark(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "testco")
	mark := time.Date(2026, 9, 11, 14, 3, 0, 0, time.UTC)
	if err := db.SetSyncWatermark("testco", "contacts", mark.Format(time.RFC3339)); err != nil {
		t.Fatalf("SetSyncWatermark: %v", err)
	}
	want := dayBefore(t, mark)

	cs := &companyServer{rows: map[string][]string{"testco": contactRowsFor("testco", 2)}}
	c := newCompanyServer(t, cs)

	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), c, db, contactsDep(), "", false, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("syncDependentResource: %v", res.Err)
	}

	queries := cs.queriesFor("testco")
	if len(queries) == 0 {
		t.Fatalf("no request reached the server")
	}
	for i, q := range queries {
		if got := q.Get("lastModifiedGe"); got != want {
			t.Fatalf("request %d lastModifiedGe = %q, want %q (query %v)", i, got, want, q)
		}
		if dates := dateParamsOf(q); len(dates) != 1 || dates[0] != "lastModifiedGe" {
			t.Fatalf("request %d carried date params %v, want only lastModifiedGe", i, dates)
		}
	}

	evs := windowEvents(events.String())
	if len(evs) != 1 {
		t.Fatalf("got %d sync_window events, want 1: %s", len(evs), events.String())
	}
	if evs[0]["company"] != "testco" || evs[0]["resource"] != "contacts" || evs[0]["since"] != want {
		t.Fatalf("sync_window = %v, want company testco, resource contacts, since %s", evs[0], want)
	}
	// The window must not be read as a complete view of the collection.
	if _, ok := db.SyncResultCount("contacts"); ok {
		t.Fatalf("a windowed run recorded a result_count; the header described the window, not the collection")
	}
}

// TestSyncDependentResource_CompanyScopeLeavesOtherCompaniesAlone is the reason
// sync_state could not carry this watermark: a --company A run must not move a
// number company B's next pull reads, or B's changes in that gap are never
// fetched again.
func TestSyncDependentResource_CompanyScopeLeavesOtherCompaniesAlone(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "alpha", "beta")
	old := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC).Format(time.RFC3339)
	for _, slug := range []string{"alpha", "beta"} {
		if err := db.SetSyncWatermark(slug, "contacts", old); err != nil {
			t.Fatalf("SetSyncWatermark(%s): %v", slug, err)
		}
	}

	cs := &companyServer{rows: map[string][]string{
		"alpha": contactRowsFor("alpha", 2),
		"beta":  contactRowsFor("beta", 2),
	}}
	c := newCompanyServer(t, cs)

	prevScope := syncCompanyScope
	syncCompanyScope = "alpha"
	t.Cleanup(func() { syncCompanyScope = prevScope })

	if res := syncDependentResource(context.Background(), c, db, contactsDep(), "", false, 0, false, nil, &bytes.Buffer{}); res.Err != nil {
		t.Fatalf("syncDependentResource: %v", res.Err)
	}

	if got := len(cs.queriesFor("beta")); got != 0 {
		t.Fatalf("%d request(s) reached beta under --company alpha", got)
	}
	if got := db.SyncWatermark("beta", "contacts"); got != old {
		t.Fatalf("beta watermark = %q, want the untouched %q: a scoped run must not move a company it never pulled", got, old)
	}
	if got := db.SyncWatermark("alpha", "contacts"); got == old || got == "" {
		t.Fatalf("alpha watermark = %q, want a fresh stamp after its own complete pull", got)
	}
}

// TestSyncDependentResource_FailedPairKeepsItsWatermark: beta's pull errors, so
// beta keeps the old mark and will re-pull that window next run; alpha's
// succeeded and moves. A watermark advanced over rows nobody fetched would lose
// them permanently, which is why failure costs a re-pull rather than a skip.
func TestSyncDependentResource_FailedPairKeepsItsWatermark(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "alpha", "beta")
	old := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if err := db.SetSyncWatermark("beta", "contacts", old); err != nil {
		t.Fatalf("SetSyncWatermark: %v", err)
	}

	cs := &companyServer{
		rows:    map[string][]string{"alpha": contactRowsFor("alpha", 2), "beta": contactRowsFor("beta", 2)},
		failFor: map[string]bool{"beta": true},
	}
	c := newCompanyServer(t, cs)

	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), c, db, contactsDep(), "", false, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("syncDependentResource: %v", res.Err)
	}

	if got := db.SyncWatermark("beta", "contacts"); got != old {
		t.Fatalf("beta watermark = %q after a failed pull, want the untouched %q", got, old)
	}
	if got := db.SyncWatermark("alpha", "contacts"); got == "" {
		t.Fatalf("alpha watermark is empty after a complete pull; one company's failure must not block another")
	}
	if !strings.Contains(events.String(), `"event":"sync_error"`) {
		t.Fatalf("the failing pair produced no sync_error: %s", events.String())
	}
}

// TestSyncDependentResource_ShortPullKeepsItsWatermark: the API says the window
// held three rows and serves two. That is issue #15's result_count_mismatch,
// and a pull that cannot account for every row it was promised may not claim
// the window is done.
func TestSyncDependentResource_ShortPullKeepsItsWatermark(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "testco")
	cs := &companyServer{
		rows:    map[string][]string{"testco": contactRowsFor("testco", 2)},
		claimed: map[string]int{"testco": 3},
	}
	c := newCompanyServer(t, cs)

	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), c, db, contactsDep(), "", false, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("syncDependentResource: %v", res.Err)
	}
	if anomalies := countAnomalies(t, events.String(), "result_count_mismatch"); len(anomalies) != 1 {
		t.Fatalf("short pull emitted %d result_count_mismatch events, want 1\n%s", len(anomalies), events.String())
	}
	if got := db.SyncWatermark("testco", "contacts"); got != "" {
		t.Fatalf("watermark = %q after a short pull, want none recorded", got)
	}
}

// TestSyncDependentResource_FullResetsTheWatermark: --full ignores the stored
// mark (no date filter on the wire) and replaces it with one describing the
// full pull it just made.
func TestSyncDependentResource_FullResetsTheWatermark(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "testco", "failco")
	old := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC).Format(time.RFC3339)
	for _, slug := range []string{"testco", "failco"} {
		if err := db.SetSyncWatermark(slug, "contacts", old); err != nil {
			t.Fatalf("SetSyncWatermark(%s): %v", slug, err)
		}
	}

	cs := &companyServer{
		rows:    map[string][]string{"testco": contactRowsFor("testco", 3), "failco": contactRowsFor("failco", 3)},
		failFor: map[string]bool{"failco": true},
	}
	c := newCompanyServer(t, cs)

	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), c, db, contactsDep(), "", true, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("syncDependentResource --full: %v", res.Err)
	}
	for i, q := range cs.queriesFor("testco") {
		if dates := dateParamsOf(q); len(dates) != 0 {
			t.Fatalf("--full request %d carried date params %v, want none", i, dates)
		}
	}
	if evs := windowEvents(events.String()); len(evs) != 0 {
		t.Fatalf("--full announced %d sync_window events, want 0: %s", len(evs), events.String())
	}
	got := db.SyncWatermark("testco", "contacts")
	if got == "" || got == old {
		t.Fatalf("watermark after --full = %q, want a fresh stamp replacing %q", got, old)
	}
	// failco's --full pull never completed. It must be left saying "pull me in
	// full" rather than keeping a mark for a collection this run cleared and
	// then only partly refetched: --full clears before it refetches, and only a
	// complete pull writes a new one.
	if got := db.SyncWatermark("failco", "contacts"); got != "" {
		t.Fatalf("watermark after a failed --full = %q, want none: a stale mark must not survive the refetch it interrupted", got)
	}
}

// TestSyncDependentResource_SinceDoesNotTouchTheWatermark: --since is a window
// the caller chose, not evidence about what has been pulled. It must neither
// read the watermark nor write one, in either direction.
func TestSyncDependentResource_SinceDoesNotTouchTheWatermark(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "testco")
	old := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	if err := db.SetSyncWatermark("testco", "contacts", old.Format(time.RFC3339)); err != nil {
		t.Fatalf("SetSyncWatermark: %v", err)
	}

	cs := &companyServer{rows: map[string][]string{"testco": contactRowsFor("testco", 2)}}
	c := newCompanyServer(t, cs)

	since := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if res := syncDependentResource(context.Background(), c, db, contactsDep(), since.Format(time.RFC3339), false, 0, false, nil, &bytes.Buffer{}); res.Err != nil {
		t.Fatalf("syncDependentResource --since: %v", res.Err)
	}

	want := dayBefore(t, since)
	for i, q := range cs.queriesFor("testco") {
		if got := q.Get("lastModifiedGe"); got != want {
			t.Fatalf("--since request %d lastModifiedGe = %q, want the caller's %q, not the watermark", i, got, want)
		}
	}
	if got := db.SyncWatermark("testco", "contacts"); got != old.Format(time.RFC3339) {
		t.Fatalf("watermark = %q after --since, want the untouched %q", got, old.Format(time.RFC3339))
	}
}

// TestSyncWatermark_UnmappedResourceNeitherWindowsNorRecords: accounts declares
// no date filter, so the default path pulls it in full — silently, because
// nobody asked for a window here — and records nothing that would never be
// read.
func TestSyncWatermark_UnmappedResourceNeitherWindowsNorRecords(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "testco")
	cs := &companyServer{rows: map[string][]string{"testco": {`{"code":"1500","name":"Account"}`}}}
	c := newCompanyServer(t, cs)

	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), c, db, accountsDep(), "", false, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("syncDependentResource: %v", res.Err)
	}
	if warns := warningsWithReason(events.String(), "resource_not_incremental"); len(warns) != 0 {
		t.Fatalf("the default path warned resource_not_incremental; no window was requested: %s", events.String())
	}
	if got := db.SyncWatermark("testco", "accounts"); got != "" {
		t.Fatalf("watermark %q recorded for a resource with no temporal filter", got)
	}
}

// TestSyncWatermarkStore_KeyedByCompanyAndResource pins the store contract the
// CLI policy rests on: two companies and two resources are four independent
// rows, an unknown pair reads as "", and a clear returns it to that state.
func TestSyncWatermarkStore_KeyedByCompanyAndResource(t *testing.T) {
	db := openTestStore(t)
	pairs := []struct{ company, resource, mark string }{
		{"alpha", "contacts", "2026-09-01T00:00:00Z"},
		{"alpha", "sales", "2026-09-02T00:00:00Z"},
		{"beta", "contacts", "2026-09-03T00:00:00Z"},
		{"beta", "sales", "2026-09-04T00:00:00Z"},
	}
	for _, p := range pairs {
		if err := db.SetSyncWatermark(p.company, p.resource, p.mark); err != nil {
			t.Fatalf("SetSyncWatermark(%s, %s): %v", p.company, p.resource, err)
		}
	}
	for _, p := range pairs {
		if got := db.SyncWatermark(p.company, p.resource); got != p.mark {
			t.Fatalf("SyncWatermark(%s, %s) = %q, want %q", p.company, p.resource, got, p.mark)
		}
	}
	if got := db.SyncWatermark("gamma", "contacts"); got != "" {
		t.Fatalf("unknown pair = %q, want \"\" (pull it in full)", got)
	}
	if err := db.ClearSyncWatermark("alpha", "contacts"); err != nil {
		t.Fatalf("ClearSyncWatermark: %v", err)
	}
	if got := db.SyncWatermark("alpha", "contacts"); got != "" {
		t.Fatalf("cleared pair = %q, want \"\"", got)
	}
	if got := db.SyncWatermark("beta", "contacts"); got != "2026-09-03T00:00:00Z" {
		t.Fatalf("clearing alpha/contacts changed beta/contacts to %q", got)
	}
	if err := db.SetSyncWatermark("", "contacts", "2026-09-01T00:00:00Z"); err == nil {
		t.Fatalf("an unscoped watermark write was accepted; it would window every company")
	}
}

// TestSync_DefaultRunWindowsTheSecondPass drives the real `sync` command end to
// end: the first run pulls in full, the second sends lastModifiedGe for the
// same pair and announces it, and the mirror keeps its rows through both.
func TestSync_DefaultRunWindowsTheSecondPass(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")

	cs := &companyServer{rows: map[string][]string{"testco": contactRowsFor("testco", 2)}}
	srv := httptest.NewServer(cs)
	t.Cleanup(srv.Close)
	t.Setenv("FIKEN_BASE_URL", srv.URL)
	t.Setenv("FIKEN_API_TOKEN", "test-token")

	dbPath := home + "/data.db"
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, _, err := db.UpsertBatch("companies", []json.RawMessage{
		json.RawMessage(`{"slug":"testco","name":"Test Company","testCompany":true}`),
	}); err != nil {
		t.Fatalf("seed companies: %v", err)
	}
	_ = db.Close()

	prevScope := syncCompanyScope
	syncCompanyScope = "testco"
	t.Cleanup(func() { syncCompanyScope = prevScope })

	run := func() string {
		var out bytes.Buffer
		cmd := newSyncCmd(&rootFlags{timeout: 10 * time.Second, dataSource: "auto"})
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		cmd.SetArgs([]string{"--db", dbPath, "--company", "testco", "--resources", "contacts"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("sync: %v", err)
		}
		return out.String()
	}

	if evs := windowEvents(run()); len(evs) != 0 {
		t.Fatalf("first run announced %d sync_window events, want 0: %v", len(evs), evs)
	}
	firstRunRequests := len(cs.queriesFor("testco"))
	if firstRunRequests == 0 {
		t.Fatalf("no request reached the server on the first run")
	}

	evs := windowEvents(run())
	if len(evs) != 1 {
		t.Fatalf("second run announced %d sync_window events, want 1: %v", len(evs), evs)
	}
	if evs[0]["company"] != "testco" || evs[0]["resource"] != "contacts" {
		t.Fatalf("sync_window = %v, want the testco/contacts pair", evs[0])
	}
	for i, q := range cs.queriesFor("testco")[firstRunRequests:] {
		if q.Get("lastModifiedGe") == "" {
			t.Fatalf("second-run request %d carried no lastModifiedGe: %v", i, q)
		}
	}

	reopened, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if got, err := reopened.CountCompanyResources("contacts", "testco"); err != nil || got != 2 {
		t.Fatalf("contacts rows for testco = (%d, %v), want (2, nil): an incremental run must not drop unchanged rows", got, err)
	}
}

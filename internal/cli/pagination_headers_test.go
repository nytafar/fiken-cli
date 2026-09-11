// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Pins the result-count headers end to end (issue #15): the provenance meta of
// a paginated read, the sync completeness anomaly that would have caught issue
// #12 on the day it landed, and the doctor cache report that shows both counts.
// Reuses the pageServer harness from pagination_zero_based_test.go.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"fiken-cli/internal/client"
	"fiken-cli/internal/config"
	"fiken-cli/internal/store"
)

// countingPageServer is the pageServer plus Fiken's pagination header block.
// claimedResultCount is what the API says the collection holds, which is not
// necessarily what it serves — that divergence is the whole point of issue #15.
type countingPageServer struct {
	*pageServer
	claimedResultCount int
}

func (c *countingPageServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	page := 0
	if raw := r.URL.Query().Get("page"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			page = n
		}
	}
	size := c.pageSize
	if v := r.URL.Query().Get("pageSize"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			size = n
		}
	}
	pageCount := (c.claimedResultCount + size - 1) / size
	w.Header().Set(client.HeaderPage, strconv.Itoa(page))
	w.Header().Set(client.HeaderPageSize, strconv.Itoa(size))
	w.Header().Set(client.HeaderPageCount, strconv.Itoa(pageCount))
	w.Header().Set(client.HeaderResultCount, strconv.Itoa(c.claimedResultCount))
	c.pageServer.ServeHTTP(w, r)
}

// newCountingPageServer serves `served` rows while claiming `claimed` in the
// Fiken-Api-Result-Count header.
func newCountingPageServer(t *testing.T, served, claimed, pageSize int) (*countingPageServer, *client.Client) {
	t.Helper()
	cps := &countingPageServer{
		pageServer: &pageServer{total: served, pageSize: pageSize, row: func(i int) string {
			return fmt.Sprintf(`{"code":"%d","name":"Account %d"}`, 1500+i, i)
		}},
		claimedResultCount: claimed,
	}
	srv := httptest.NewServer(cps)
	t.Cleanup(srv.Close)
	c := client.New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)
	c.NoCache = true
	return cps, c
}

func metaOf(t *testing.T, envelope json.RawMessage) map[string]any {
	t.Helper()
	var parsed struct {
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(envelope, &parsed); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	return parsed.Meta
}

// TestPaginatedRead_ProvenanceCarriesResultAndPageCount is the read half of
// issue #15: a consumer of the envelope can now tell whether the page it got
// is the whole collection.
func TestPaginatedRead_ProvenanceCarriesResultAndPageCount(t *testing.T) {
	_, c := newCountingPageServer(t, 250, 250, 100)
	flags := &rootFlags{dataSource: "live"}

	data, prov, err := resolvePaginatedReadWithStrategy(context.Background(), c, flags, "live", "accounts",
		"/companies/testco/accounts", map[string]string{"page": "0", "pageSize": "100"}, nil,
		false, "page", "page", "pageSize", "", "", io.Discard)
	if err != nil {
		t.Fatalf("resolvePaginatedReadWithStrategy: %v", err)
	}
	if prov.ResultCount == nil || *prov.ResultCount != 250 {
		t.Fatalf("provenance result count = %v, want 250", prov.ResultCount)
	}
	if prov.PageCount == nil || *prov.PageCount != 3 {
		t.Fatalf("provenance page count = %v, want 3", prov.PageCount)
	}

	envelope, err := wrapWithProvenance(data, prov)
	if err != nil {
		t.Fatalf("wrapWithProvenance: %v", err)
	}
	meta := metaOf(t, envelope)
	if got, ok := meta["result_count"].(float64); !ok || int(got) != 250 {
		t.Fatalf("meta.result_count = %v, want 250", meta["result_count"])
	}
	if got, ok := meta["page_count"].(float64); !ok || int(got) != 3 {
		t.Fatalf("meta.page_count = %v, want 3", meta["page_count"])
	}
}

// TestPaginatedRead_AllReportsFirstPageCounts: under --all the walk fetches
// every page, and the reported counts stay the collection totals.
func TestPaginatedRead_AllReportsFirstPageCounts(t *testing.T) {
	ps, c := newCountingPageServer(t, 250, 250, 100)
	flags := &rootFlags{dataSource: "live"}

	data, prov, err := resolvePaginatedReadWithStrategy(context.Background(), c, flags, "live", "accounts",
		"/companies/testco/accounts", map[string]string{"page": "0", "pageSize": "100"}, nil,
		true, "page", "page", "pageSize", "", "", io.Discard)
	if err != nil {
		t.Fatalf("resolvePaginatedReadWithStrategy: %v", err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("unmarshal items: %v", err)
	}
	if len(items) != 250 {
		t.Fatalf("--all returned %d items, want 250", len(items))
	}
	if len(ps.pagesRequested()) != 3 {
		t.Fatalf("pages requested = %v, want three pages", ps.pagesRequested())
	}
	if prov.ResultCount == nil || *prov.ResultCount != 250 {
		t.Fatalf("provenance result count = %v, want 250", prov.ResultCount)
	}
}

// TestPaginatedRead_NoHeadersOmitsCounts: a header-less response must publish
// no counts at all rather than zeros.
func TestPaginatedRead_NoHeadersOmitsCounts(t *testing.T) {
	_, c := newPageServer(t, 10, 100, func(i int) string {
		return fmt.Sprintf(`{"code":"%d"}`, 1500+i)
	})
	flags := &rootFlags{dataSource: "live"}

	data, prov, err := resolvePaginatedReadWithStrategy(context.Background(), c, flags, "live", "accounts",
		"/companies/testco/accounts", map[string]string{"page": "0", "pageSize": "100"}, nil,
		false, "page", "page", "pageSize", "", "", io.Discard)
	if err != nil {
		t.Fatalf("resolvePaginatedReadWithStrategy: %v", err)
	}
	if prov.ResultCount != nil || prov.PageCount != nil {
		t.Fatalf("provenance carried counts without headers: %v / %v", prov.ResultCount, prov.PageCount)
	}
	envelope, err := wrapWithProvenance(data, prov)
	if err != nil {
		t.Fatalf("wrapWithProvenance: %v", err)
	}
	meta := metaOf(t, envelope)
	if _, ok := meta["result_count"]; ok {
		t.Fatalf("meta carried result_count without headers: %v", meta)
	}
	if _, ok := meta["page_count"]; ok {
		t.Fatalf("meta carried page_count without headers: %v", meta)
	}
}

// countAnomalies returns the sync_anomaly events with the given reason.
func countAnomalies(t *testing.T, events string, reason string) []map[string]any {
	t.Helper()
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
		if ev["event"] == "sync_anomaly" && ev["reason"] == reason {
			out = append(out, ev)
		}
	}
	return out
}

// TestSyncResource_ShortPullEmitsResultCountMismatch is the anomaly issue #15
// exists for: the API says 250 rows, the walk landed 150, and exactly one
// event says so with both numbers.
func TestSyncResource_ShortPullEmitsResultCountMismatch(t *testing.T) {
	_, c := newCountingPageServer(t, 150, 250, 100)
	db := openTestStore(t)
	var events bytes.Buffer

	res := syncResource(context.Background(), c, db, "companies", "", true, 0, false, nil, &events)
	if res.Err != nil {
		t.Fatalf("syncResource: %v", res.Err)
	}
	if res.Count != 150 {
		t.Fatalf("synced rows = %d, want 150", res.Count)
	}

	anomalies := countAnomalies(t, events.String(), "result_count_mismatch")
	if len(anomalies) != 1 {
		t.Fatalf("result_count_mismatch events = %d, want exactly 1\n%s", len(anomalies), events.String())
	}
	ev := anomalies[0]
	if ev["resource"] != "companies" {
		t.Fatalf("anomaly resource = %v, want companies", ev["resource"])
	}
	if got, _ := ev["result_count"].(float64); int(got) != 250 {
		t.Fatalf("anomaly result_count = %v, want 250", ev["result_count"])
	}
	if got, _ := ev["rows"].(float64); int(got) != 150 {
		t.Fatalf("anomaly rows = %v, want 150", ev["rows"])
	}
}

// TestSyncResource_CompletePullEmitsNoAnomaly is the control: rows landed ==
// Fiken-Api-Result-Count means silence, and the count is recorded for doctor.
func TestSyncResource_CompletePullEmitsNoAnomaly(t *testing.T) {
	_, c := newCountingPageServer(t, 250, 250, 100)
	db := openTestStore(t)
	var events bytes.Buffer

	res := syncResource(context.Background(), c, db, "companies", "", true, 0, false, nil, &events)
	if res.Err != nil {
		t.Fatalf("syncResource: %v", res.Err)
	}
	if res.Count != 250 {
		t.Fatalf("synced rows = %d, want 250", res.Count)
	}
	if anomalies := countAnomalies(t, events.String(), "result_count_mismatch"); len(anomalies) != 0 {
		t.Fatalf("complete pull emitted %d result_count_mismatch events\n%s", len(anomalies), events.String())
	}
	if got, ok := db.SyncResultCount("companies"); !ok || got != 250 {
		t.Fatalf("recorded result count = (%d, %v), want (250, true)", got, ok)
	}
}

// TestSyncResource_CappedWalkSkipsComparison keeps --max-pages from producing
// a mismatch on every resource: a deliberately capped walk is short by
// construction and says nothing about completeness.
func TestSyncResource_CappedWalkSkipsComparison(t *testing.T) {
	_, c := newCountingPageServer(t, 250, 250, 100)
	db := openTestStore(t)
	var events bytes.Buffer

	res := syncResource(context.Background(), c, db, "companies", "", true, 1, false, nil, &events)
	if res.Err != nil {
		t.Fatalf("syncResource: %v", res.Err)
	}
	if anomalies := countAnomalies(t, events.String(), "result_count_mismatch"); len(anomalies) != 0 {
		t.Fatalf("capped walk emitted %d result_count_mismatch events\n%s", len(anomalies), events.String())
	}
	if _, ok := db.SyncResultCount("companies"); ok {
		t.Fatalf("capped walk recorded a result count; want none")
	}
}

// TestSyncDependentResource_ShortPullEmitsPerCompanyMismatch pins the
// dependent half: the anomaly names the company whose collection came up short.
func TestSyncDependentResource_ShortPullEmitsPerCompanyMismatch(t *testing.T) {
	_, c := newCountingPageServer(t, 150, 250, 100)
	db := openTestStore(t)
	if _, _, err := db.UpsertBatch("companies", []json.RawMessage{
		json.RawMessage(`{"slug":"testco","name":"Test Company"}`),
	}); err != nil {
		t.Fatalf("seed companies: %v", err)
	}
	var events bytes.Buffer

	dep := dependentResourceDef{
		Name:          "accounts",
		ParentTable:   "companies",
		ParentIDParam: "companySlug",
		PathTemplate:  "/companies/{companySlug}/accounts",
		KeyField:      "slug",
		PathParams:    []dependentPathParamDef{{Param: "companySlug", Field: "slug"}},
	}
	res := syncDependentResource(context.Background(), c, db, dep, "", true, 0, false, nil, &events)
	if res.Err != nil {
		t.Fatalf("syncDependentResource: %v", res.Err)
	}

	anomalies := countAnomalies(t, events.String(), "result_count_mismatch")
	if len(anomalies) != 1 {
		t.Fatalf("result_count_mismatch events = %d, want exactly 1\n%s", len(anomalies), events.String())
	}
	ev := anomalies[0]
	if ev["resource"] != "accounts" || ev["company"] != "testco" {
		t.Fatalf("anomaly = %v, want accounts/testco", ev)
	}
	if got, _ := ev["result_count"].(float64); int(got) != 250 {
		t.Fatalf("anomaly result_count = %v, want 250", ev["result_count"])
	}
	if got, _ := ev["rows"].(float64); int(got) != 150 {
		t.Fatalf("anomaly rows = %v, want 150", ev["rows"])
	}
}

// TestDoctorCacheReport_ShowsRecordedResultCount: doctor surfaces the API's own
// count next to the mirror's, so a short resource is visible without reading
// the sync stream. HOME is redirected so the real mirror is never touched.
func TestDoctorCacheReport_ShowsRecordedResultCount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := defaultDBPath("fiken-cli")
	if !strings.HasPrefix(dbPath, home) {
		t.Fatalf("test DB path %q escaped the temp HOME %q", dbPath, home)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := db.SaveSyncState("accounts", "", 150); err != nil {
		t.Fatalf("SaveSyncState: %v", err)
	}
	if err := db.SaveSyncResultCount("accounts", 250); err != nil {
		t.Fatalf("SaveSyncResultCount: %v", err)
	}
	if err := db.SaveSyncState("companies", "", 4); err != nil {
		t.Fatalf("SaveSyncState: %v", err)
	}
	_ = db.Close()

	report := collectCacheReport(context.Background(), "6h")
	resources, ok := report["resources"].([]map[string]any)
	if !ok {
		t.Fatalf("cache report has no resources: %v", report)
	}
	var accounts, companies map[string]any
	for _, r := range resources {
		switch r["type"] {
		case "accounts":
			accounts = r
		case "companies":
			companies = r
		}
	}
	if accounts == nil || companies == nil {
		t.Fatalf("cache report resources = %v, want accounts and companies", resources)
	}
	if got, ok := accounts["result_count"].(int64); !ok || got != 250 {
		t.Fatalf("accounts result_count = %v, want 250", accounts["result_count"])
	}
	if _, ok := companies["result_count"]; ok {
		t.Fatalf("companies carried a result_count without a recorded one: %v", companies)
	}
}

// rowServer serves a fixed list of row bodies as 0-based pages of bare JSON
// arrays, with Fiken's header block claiming `claimed` results. Unlike
// pageServer it takes the rows verbatim, so a test can serve the same id twice
// or two ids that collapse onto one storage key.
type rowServer struct {
	rows     []string
	pageSize int
	claimed  int

	mu     sync.Mutex
	params []url.Values
}

func (s *rowServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.params = append(s.params, r.URL.Query())
	s.mu.Unlock()

	page := 0
	if raw := r.URL.Query().Get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			http.Error(w, "bad page", http.StatusBadRequest)
			return
		}
		page = n
	}
	size := s.pageSize
	if v := r.URL.Query().Get("pageSize"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			size = n
		}
	}
	start := page * size
	if start > len(s.rows) {
		start = len(s.rows)
	}
	end := start + size
	if end > len(s.rows) {
		end = len(s.rows)
	}
	items := make([]json.RawMessage, 0, end-start)
	for _, row := range s.rows[start:end] {
		items = append(items, json.RawMessage(row))
	}
	body, _ := json.Marshal(items)
	pageCount := 0
	if size > 0 {
		pageCount = (s.claimed + size - 1) / size
	}
	w.Header().Set(client.HeaderPage, strconv.Itoa(page))
	w.Header().Set(client.HeaderPageSize, strconv.Itoa(size))
	w.Header().Set(client.HeaderPageCount, strconv.Itoa(pageCount))
	w.Header().Set(client.HeaderResultCount, strconv.Itoa(s.claimed))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (s *rowServer) requestParams() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]url.Values, len(s.params))
	copy(out, s.params)
	return out
}

func newRowServer(t *testing.T, rows []string, pageSize, claimed int) (*rowServer, *client.Client) {
	t.Helper()
	rs := &rowServer{rows: rows, pageSize: pageSize, claimed: claimed}
	srv := httptest.NewServer(rs)
	t.Cleanup(srv.Close)
	c := client.New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)
	c.NoCache = true
	return rs, c
}

func companyRows(slugs ...string) []string {
	rows := make([]string, 0, len(slugs))
	for i, slug := range slugs {
		rows = append(rows, fmt.Sprintf(`{"slug":%q,"name":"Company %d"}`, slug, i))
	}
	return rows
}

// TestSyncResource_DuplicateIDAcrossPagesIsNotAnAnomaly: the API serves 251
// items whose 251st repeats an id from page one, so 250 distinct rows land
// against a claimed 250. Counting items (UpsertBatch's `stored`) reported 251
// and invented an anomaly on a perfect mirror; counting distinct storage keys
// reports 250 and stays quiet.
func TestSyncResource_DuplicateIDAcrossPagesIsNotAnAnomaly(t *testing.T) {
	slugs := make([]string, 0, 251)
	for i := 0; i < 250; i++ {
		slugs = append(slugs, fmt.Sprintf("co-%03d", i))
	}
	// The duplicate sits on the far side of a page boundary, which is how a
	// collection that shifts under a concurrent write serves one twice.
	slugs = append(slugs[:100], append([]string{"co-000"}, slugs[100:]...)...)
	_, c := newRowServer(t, companyRows(slugs...), 100, 250)
	db := openTestStore(t)
	var events bytes.Buffer

	res := syncResource(context.Background(), c, db, "companies", "", true, 0, false, nil, &events)
	if res.Err != nil {
		t.Fatalf("syncResource: %v", res.Err)
	}
	if anomalies := countAnomalies(t, events.String(), "result_count_mismatch"); len(anomalies) != 0 {
		t.Fatalf("a duplicate id produced %d result_count_mismatch events on a complete mirror\n%s", len(anomalies), events.String())
	}
	if got, err := db.Count("companies"); err != nil || got != 250 {
		t.Fatalf("mirror rows = (%d, %v), want (250, nil)", got, err)
	}
	if got, ok := db.SyncResultCount("companies"); !ok || got != 250 {
		t.Fatalf("recorded result count = (%d, %v), want (250, true)", got, ok)
	}
}

// TestSyncResource_CollapsedStorageKeysEmitAnomaly is the issue #12 failure
// class the item counter could not see: the API serves 250 items, two of them
// collapse onto one storage key, the mirror holds 249 — and the anomaly says
// so, with rows 249.
func TestSyncResource_CollapsedStorageKeysEmitAnomaly(t *testing.T) {
	slugs := make([]string, 0, 250)
	for i := 0; i < 250; i++ {
		slugs = append(slugs, fmt.Sprintf("co-%03d", i))
	}
	slugs[249] = slugs[0] // two items, one row
	_, c := newRowServer(t, companyRows(slugs...), 100, 250)
	db := openTestStore(t)
	var events bytes.Buffer

	res := syncResource(context.Background(), c, db, "companies", "", true, 0, false, nil, &events)
	if res.Err != nil {
		t.Fatalf("syncResource: %v", res.Err)
	}
	if got, err := db.Count("companies"); err != nil || got != 249 {
		t.Fatalf("mirror rows = (%d, %v), want (249, nil)", got, err)
	}
	anomalies := countAnomalies(t, events.String(), "result_count_mismatch")
	if len(anomalies) != 1 {
		t.Fatalf("result_count_mismatch events = %d, want exactly 1\n%s", len(anomalies), events.String())
	}
	if got, _ := anomalies[0]["result_count"].(float64); int(got) != 250 {
		t.Fatalf("anomaly result_count = %v, want 250", anomalies[0]["result_count"])
	}
	if got, _ := anomalies[0]["rows"].(float64); int(got) != 249 {
		t.Fatalf("anomaly rows = %v, want 249", anomalies[0]["rows"])
	}
}

// TestShouldRecordResultCount pins the recording decision as a pure function:
// only a complete, unscoped, unwindowed walk that saw the header may overwrite
// sync_state.result_count.
func TestShouldRecordResultCount(t *testing.T) {
	cases := []struct {
		name                      string
		seen, truncated, windowed bool
		companyScope              string
		want                      bool
	}{
		{name: "complete unwindowed walk", seen: true, want: true},
		{name: "no header seen", want: false},
		{name: "truncated walk", seen: true, truncated: true, want: false},
		{name: "windowed walk", seen: true, windowed: true, want: false},
		{name: "company scoped", seen: true, companyScope: "testco", want: false},
		{name: "windowed and truncated", seen: true, truncated: true, windowed: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := syncCompanyScope
			syncCompanyScope = tc.companyScope
			t.Cleanup(func() { syncCompanyScope = prev })
			if got := shouldRecordResultCount(tc.seen, tc.truncated, tc.windowed); got != tc.want {
				t.Fatalf("shouldRecordResultCount(%v, %v, %v) with scope %q = %v, want %v",
					tc.seen, tc.truncated, tc.windowed, tc.companyScope, got, tc.want)
			}
		})
	}
}

// TestSyncResource_WindowedPullKeepsRecordedResultCount is the reason the
// windowed flag exists: Fiken answers a filtered request with the Result-Count
// of the FILTERED set, so an incremental run must not overwrite the
// full-collection number with it (doctor would read rows 577 / result_count 3).
// The per-pull mismatch check still runs — header and rows describe the same
// filtered set — and an unwindowed run still records.
func TestSyncResource_WindowedPullKeepsRecordedResultCount(t *testing.T) {
	db := openTestStore(t)
	if err := db.SaveSyncResultCount("companies", 577); err != nil {
		t.Fatalf("SaveSyncResultCount: %v", err)
	}

	// The temporal filter issue #13 will map. Installed here so the windowed
	// path can be driven before that mapping exists.
	prev := syncSinceParamResolver
	syncSinceParamResolver = func(resource string) string {
		if resource == "companies" {
			return "lastModifiedGe"
		}
		return ""
	}
	t.Cleanup(func() { syncSinceParamResolver = prev })

	// The window claims 3 changed rows and serves 2: short, so the comparison
	// must still speak up.
	rs, c := newRowServer(t, companyRows("co-000", "co-001"), 100, 3)
	var events bytes.Buffer
	res := syncResource(context.Background(), c, db, "companies", "2026-01-01T00:00:00Z", true, 0, false, nil, &events)
	if res.Err != nil {
		t.Fatalf("syncResource: %v", res.Err)
	}
	params := rs.requestParams()
	if len(params) == 0 || params[0].Get("lastModifiedGe") != "2026-01-01T00:00:00Z" {
		t.Fatalf("request params = %v, want lastModifiedGe on the first request", params)
	}
	if anomalies := countAnomalies(t, events.String(), "result_count_mismatch"); len(anomalies) != 1 {
		t.Fatalf("windowed short pull emitted %d result_count_mismatch events, want 1\n%s", len(anomalies), events.String())
	}
	if got, ok := db.SyncResultCount("companies"); !ok || got != 577 {
		t.Fatalf("recorded result count after a windowed pull = (%d, %v), want (577, true)", got, ok)
	}

	// The control: without a window the same walker records what it sees.
	syncSinceParamResolver = nil
	_, c2 := newRowServer(t, companyRows("co-000", "co-001"), 100, 2)
	var fullEvents bytes.Buffer
	if res := syncResource(context.Background(), c2, db, "companies", "", true, 0, false, nil, &fullEvents); res.Err != nil {
		t.Fatalf("unwindowed syncResource: %v", res.Err)
	}
	if got, ok := db.SyncResultCount("companies"); !ok || got != 2 {
		t.Fatalf("recorded result count after an unwindowed pull = (%d, %v), want (2, true)", got, ok)
	}
}

// TestPaginatedRead_EmptyCollectionPublishesZeroCounts: Fiken answers an empty
// collection with Page-Count: 0 / Result-Count: 0. Both are real claims about
// the collection, so both must be published — gating page_count on "> 0"
// silently dropped the one case where the count matters most.
func TestPaginatedRead_EmptyCollectionPublishesZeroCounts(t *testing.T) {
	_, c := newRowServer(t, nil, 100, 0)
	flags := &rootFlags{dataSource: "live"}

	data, prov, err := resolvePaginatedReadWithStrategy(context.Background(), c, flags, "live", "accounts",
		"/companies/testco/accounts", map[string]string{"page": "0", "pageSize": "100"}, nil,
		false, "page", "page", "pageSize", "", "", io.Discard)
	if err != nil {
		t.Fatalf("resolvePaginatedReadWithStrategy: %v", err)
	}
	if prov.PageCount == nil || *prov.PageCount != 0 {
		t.Fatalf("provenance page count = %v, want a published 0", prov.PageCount)
	}
	if prov.ResultCount == nil || *prov.ResultCount != 0 {
		t.Fatalf("provenance result count = %v, want a published 0", prov.ResultCount)
	}
	envelope, err := wrapWithProvenance(data, prov)
	if err != nil {
		t.Fatalf("wrapWithProvenance: %v", err)
	}
	meta := metaOf(t, envelope)
	if got, ok := meta["page_count"].(float64); !ok || int(got) != 0 {
		t.Fatalf("meta.page_count = %v, want 0", meta["page_count"])
	}
	if got, ok := meta["result_count"].(float64); !ok || int(got) != 0 {
		t.Fatalf("meta.result_count = %v, want 0", meta["result_count"])
	}
}

// scalarBodyServer answers with a valid JSON body that is neither an array nor
// an object — a shape the flat walker routes to its single-object branch — and
// still sets the result-count headers.
type scalarBodyServer struct {
	body    string
	claimed int
}

func (s *scalarBodyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(client.HeaderPage, "0")
	w.Header().Set(client.HeaderPageSize, "100")
	w.Header().Set(client.HeaderPageCount, "1")
	w.Header().Set(client.HeaderResultCount, strconv.Itoa(s.claimed))
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, s.body)
}

// TestSyncResource_SingleObjectBodyIsNotComparable: the single-object branch
// does not upsert through UpsertBatch, so store.StorageKeyOf cannot say what it
// landed under — feeding landedIDs from there reported rows 0 for a body that
// did land, invented a result_count_mismatch on a healthy resource, and
// recorded that same header as the collection total. A response with no
// comparable row count must produce no anomaly and record nothing.
func TestSyncResource_SingleObjectBodyIsNotComparable(t *testing.T) {
	srv := httptest.NewServer(&scalarBodyServer{body: `"ok"`, claimed: 1})
	t.Cleanup(srv.Close)
	c := client.New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)
	c.NoCache = true

	db := openTestStore(t)
	var events bytes.Buffer
	res := syncResource(context.Background(), c, db, "companies", "", true, 0, false, nil, &events)
	if res.Err != nil {
		t.Fatalf("syncResource: %v", res.Err)
	}
	if anomalies := countAnomalies(t, events.String(), "result_count_mismatch"); len(anomalies) != 0 {
		t.Fatalf("a single-object body produced %d result_count_mismatch events, want 0\n%s", len(anomalies), events.String())
	}
	if got, ok := db.SyncResultCount("companies"); ok {
		t.Fatalf("recorded result count = (%d, true) after a non-comparable walk, want nothing recorded", got)
	}
}

// TestSyncResource_ResourceParamPullKeepsRecordedResultCount is the other half
// of the windowed rule: --resource-param is a filter the user sent, so Fiken
// answers with the FILTERED Result-Count. windowedPull was computed before
// userParams.applyTo injected the parameter, so that filtered number was
// recorded as the collection total.
func TestSyncResource_ResourceParamPullKeepsRecordedResultCount(t *testing.T) {
	db := openTestStore(t)
	if err := db.SaveSyncResultCount("companies", 577); err != nil {
		t.Fatalf("SaveSyncResultCount: %v", err)
	}

	userParams, err := parseSyncUserParams(nil, []string{"companies:lastModifiedGe=2026-09-01"}, nil)
	if err != nil {
		t.Fatalf("parseSyncUserParams: %v", err)
	}

	rs, c := newRowServer(t, companyRows("co-000", "co-001"), 100, 2)
	var events bytes.Buffer
	if res := syncResource(context.Background(), c, db, "companies", "", true, 0, false, userParams, &events); res.Err != nil {
		t.Fatalf("syncResource: %v", res.Err)
	}

	// The parameter still reaches the API: this is about what gets recorded,
	// not about suppressing the filter.
	params := rs.requestParams()
	if len(params) == 0 || params[0].Get("lastModifiedGe") != "2026-09-01" {
		t.Fatalf("request params = %v, want lastModifiedGe on the first request", params)
	}
	if got, ok := db.SyncResultCount("companies"); !ok || got != 577 {
		t.Fatalf("recorded result count after a --resource-param pull = (%d, %v), want (577, true)", got, ok)
	}

	// The control: the same walk without user params records what it saw.
	_, c2 := newRowServer(t, companyRows("co-000", "co-001"), 100, 2)
	if res := syncResource(context.Background(), c2, db, "companies", "", true, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("unfiltered syncResource: %v", res.Err)
	}
	if got, ok := db.SyncResultCount("companies"); !ok || got != 2 {
		t.Fatalf("recorded result count after an unfiltered pull = (%d, %v), want (2, true)", got, ok)
	}
}

// deniedParentServer answers one company's dependent endpoint with 403 (a Fiken
// book whose API module is not activated) and serves a complete, header-carrying
// collection for every other company.
type deniedParentServer struct {
	deniedSlug string
	rows       []string
}

func (s *deniedParentServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/"+s.deniedSlug+"/") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"API module not activated"}`)
		return
	}
	items := make([]json.RawMessage, 0, len(s.rows))
	if page := r.URL.Query().Get("page"); page == "" || page == "0" {
		for _, row := range s.rows {
			items = append(items, json.RawMessage(row))
		}
	}
	body, _ := json.Marshal(items)
	w.Header().Set(client.HeaderPage, "0")
	w.Header().Set(client.HeaderPageSize, "100")
	w.Header().Set(client.HeaderPageCount, "1")
	w.Header().Set(client.HeaderResultCount, strconv.Itoa(len(s.rows)))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// TestSyncDependentResource_AccessDeniedParentDoesNotBlockRecording is what the
// first full resync found: one of four companies answers 403 on every dependent
// endpoint, so it carries no result-count header, and the "headers present for
// every parent" rule left result_count NULL for every dependent resource in the
// mirror. A parent that denied access before serving a page landed zero rows and
// added zero to the total, so it is accounted for — the recorded number is the
// sum over the parents that did answer.
func TestSyncDependentResource_AccessDeniedParentDoesNotBlockRecording(t *testing.T) {
	srv := httptest.NewServer(&deniedParentServer{
		deniedSlug: "denied-co",
		rows:       []string{`{"code":"1500","name":"Account A"}`, `{"code":"1501","name":"Account B"}`},
	})
	t.Cleanup(srv.Close)
	c := client.New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)
	c.NoCache = true

	db := openTestStore(t)
	if _, _, err := db.UpsertBatch("companies", []json.RawMessage{
		json.RawMessage(`{"slug":"denied-co","name":"No API Module"}`),
		json.RawMessage(`{"slug":"good-co","name":"Complete Book"}`),
	}); err != nil {
		t.Fatalf("seed companies: %v", err)
	}

	dep := dependentResourceDef{
		Name:          "accounts",
		ParentTable:   "companies",
		ParentIDParam: "companySlug",
		PathTemplate:  "/companies/{companySlug}/accounts",
		KeyField:      "slug",
		PathParams:    []dependentPathParamDef{{Param: "companySlug", Field: "slug"}},
	}
	var events bytes.Buffer
	res := syncDependentResource(context.Background(), c, db, dep, "", true, 0, false, nil, &events)
	if res.Err != nil {
		t.Fatalf("syncDependentResource: %v", res.Err)
	}
	if anomalies := countAnomalies(t, events.String(), "result_count_mismatch"); len(anomalies) != 0 {
		t.Fatalf("result_count_mismatch events = %v, want none\n%s", anomalies, events.String())
	}
	if got, ok := db.SyncResultCount("accounts"); !ok || got != 2 {
		t.Fatalf("recorded result count = (%d, %v), want (2, true): the denied parent must not block recording\n%s", got, ok, events.String())
	}
	// The denial itself is still reported.
	if !strings.Contains(events.String(), `"reason":"forbidden"`) {
		t.Fatalf("the 403 parent was not reported\n%s", events.String())
	}
}

// TestSyncDependentResource_DeniedParentWithMirrorRowsBlocksRecording is the
// other half of the denied-parent rule (issue #15 review). The recorded number
// is compared against sync_state.total_count, which counts EVERY company's rows
// including the ones an earlier sync landed for a company whose API module has
// since lapsed. Counting such a parent as "accounted for with zero" would
// rewrite the recorded total down by exactly the rows still sitting in the
// mirror, and doctor would report rows N / result_count N-minus-those forever.
// So a denied parent that still holds rows blocks recording for this run, and
// the previously recorded value survives untouched.
func TestSyncDependentResource_DeniedParentWithMirrorRowsBlocksRecording(t *testing.T) {
	srv := httptest.NewServer(&deniedParentServer{
		deniedSlug: "denied-co",
		rows:       []string{`{"code":"1500","name":"Account A"}`, `{"code":"1501","name":"Account B"}`},
	})
	t.Cleanup(srv.Close)
	c := client.New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)
	c.NoCache = true

	db := openTestStore(t)
	if _, _, err := db.UpsertBatch("companies", []json.RawMessage{
		json.RawMessage(`{"slug":"denied-co","name":"Lapsed API Module"}`),
		json.RawMessage(`{"slug":"good-co","name":"Complete Book"}`),
	}); err != nil {
		t.Fatalf("seed companies: %v", err)
	}
	// What an earlier sync landed for the company that now denies access.
	if _, _, err := db.UpsertBatch("accounts", []json.RawMessage{
		json.RawMessage(`{"code":"3000","name":"Old Account","parent_id":"denied-co"}`),
		json.RawMessage(`{"code":"3001","name":"Older Account","parent_id":"denied-co"}`),
	}); err != nil {
		t.Fatalf("seed denied parent's rows: %v", err)
	}
	if rows, err := db.CountCompanyResources("accounts", "denied-co"); err != nil || rows != 2 {
		t.Fatalf("seeded rows for denied-co = (%d, %v), want (2, nil)", rows, err)
	}
	// What the last run that could see every book recorded.
	if err := db.SaveSyncResultCount("accounts", 4); err != nil {
		t.Fatalf("seed recorded result count: %v", err)
	}

	dep := dependentResourceDef{
		Name:          "accounts",
		ParentTable:   "companies",
		ParentIDParam: "companySlug",
		PathTemplate:  "/companies/{companySlug}/accounts",
		KeyField:      "slug",
		PathParams:    []dependentPathParamDef{{Param: "companySlug", Field: "slug"}},
	}
	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), c, db, dep, "", true, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("syncDependentResource: %v", res.Err)
	}
	if got, ok := db.SyncResultCount("accounts"); !ok || got != 4 {
		t.Fatalf("recorded result count = (%d, %v), want (4, true): a denied parent that still holds rows must not let the run overwrite it\n%s", got, ok, events.String())
	}
}

// allDeniedServer answers every dependent request with 403, the shape of a run
// whose only books have no API module activated.
type allDeniedServer struct{}

func (allDeniedServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = io.WriteString(w, `{"message":"API module not activated"}`)
}

// TestSyncDependentResource_AllParentsDeniedRecordsNothing: with no parent
// serving a header the sum is a literal 0, which SyncResultCount and doctor
// report as "the API says this collection is empty". Nobody answered, so
// nothing is recorded and result_count stays NULL.
func TestSyncDependentResource_AllParentsDeniedRecordsNothing(t *testing.T) {
	srv := httptest.NewServer(allDeniedServer{})
	t.Cleanup(srv.Close)
	c := client.New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)
	c.NoCache = true

	db := openTestStore(t)
	if _, _, err := db.UpsertBatch("companies", []json.RawMessage{
		json.RawMessage(`{"slug":"denied-one","name":"No API Module"}`),
		json.RawMessage(`{"slug":"denied-two","name":"No API Module Either"}`),
	}); err != nil {
		t.Fatalf("seed companies: %v", err)
	}

	dep := dependentResourceDef{
		Name:          "accounts",
		ParentTable:   "companies",
		ParentIDParam: "companySlug",
		PathTemplate:  "/companies/{companySlug}/accounts",
		KeyField:      "slug",
		PathParams:    []dependentPathParamDef{{Param: "companySlug", Field: "slug"}},
	}
	var events bytes.Buffer
	res := syncDependentResource(context.Background(), c, db, dep, "", true, 0, false, nil, &events)
	if res.Warn == nil {
		t.Fatalf("an all-denied dependent walk must warn, got %+v\n%s", res, events.String())
	}
	if got, ok := db.SyncResultCount("accounts"); ok {
		t.Fatalf("recorded result count = (%d, %v), want no recorded value: no parent served a result count\n%s", got, ok, events.String())
	}
}

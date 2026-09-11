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
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	if got, ok := accounts["result_count"].(int); !ok || got != 250 {
		t.Fatalf("accounts result_count = %v, want 250", accounts["result_count"])
	}
	if _, ok := companies["result_count"]; ok {
		t.Fatalf("companies carried a result_count without a recorded one: %v", companies)
	}
}

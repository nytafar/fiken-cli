// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Pins the 0-based page walk (issue #12). Fiken's `page` parameter has spec
// default 0, so the cursor-less first request is served page 0 and the page
// after it is 1. The generated page-int fallback read the empty cursor as
// page 1 and asked for page 2 next, silently dropping page 1 of every
// paginated resource; the generated --all helper advanced only offset-type
// pagination, so a bare-array page API returned one page.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"fiken-cli/internal/client"
	"fiken-cli/internal/config"
	"fiken-cli/internal/store"
)

// pageServer is a fake Fiken list endpoint: bare JSON arrays, `page` is
// 0-based and absent means page 0, `pageSize` bounds the slice. It records
// every page value it was asked for, using "<absent>" for a request that
// carried no page parameter at all.
type pageServer struct {
	total    int
	pageSize int
	row      func(i int) string

	mu       sync.Mutex
	requests []string
}

func (p *pageServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, ok := r.URL.Query()["page"]
	recorded := "<absent>"
	page := 0
	if ok && len(raw) > 0 {
		recorded = raw[0]
		n, err := strconv.Atoi(raw[0])
		if err != nil {
			http.Error(w, "bad page", http.StatusBadRequest)
			return
		}
		page = n
	}
	p.mu.Lock()
	p.requests = append(p.requests, recorded)
	p.mu.Unlock()

	size := p.pageSize
	if v := r.URL.Query().Get("pageSize"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			size = n
		}
	}

	start := page * size
	end := start + size
	if start > p.total {
		start = p.total
	}
	if end > p.total {
		end = p.total
	}
	items := make([]json.RawMessage, 0, end-start)
	for i := start; i < end; i++ {
		items = append(items, json.RawMessage(p.row(i)))
	}
	body, err := json.Marshal(items)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (p *pageServer) pagesRequested() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.requests))
	copy(out, p.requests)
	return out
}

// countPage returns how many times the page parameter had the given value.
func countPage(requests []string, want string) int {
	n := 0
	for _, r := range requests {
		if r == want {
			n++
		}
	}
	return n
}

func newPageServer(t *testing.T, total, pageSize int, row func(i int) string) (*pageServer, *client.Client) {
	t.Helper()
	ps := &pageServer{total: total, pageSize: pageSize, row: row}
	srv := httptest.NewServer(ps)
	t.Cleanup(srv.Close)

	cfg := &config.Config{BaseURL: srv.URL, AccessToken: "test-token"}
	c := client.New(cfg, 10*time.Second, 0)
	c.NoCache = true
	return ps, c
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestSyncResource_WalksZeroBasedPagesFromPageZero is the flat-resource half
// of issue #12: 250 rows at page size 100 must land as 250 rows fetched over
// pages 0, 1 and 2, with page 1 requested exactly once.
func TestSyncResource_WalksZeroBasedPagesFromPageZero(t *testing.T) {
	ps, c := newPageServer(t, 250, 100, func(i int) string {
		return fmt.Sprintf(`{"slug":"co-%03d","name":"Company %d"}`, i, i)
	})
	db := openTestStore(t)

	res := syncResource(context.Background(), c, db, "companies", "", true, 0, false, nil, io.Discard)
	if res.Err != nil {
		t.Fatalf("syncResource: %v", res.Err)
	}
	if res.Count != 250 {
		t.Fatalf("synced rows = %d, want 250", res.Count)
	}
	stored, err := db.Count("companies")
	if err != nil {
		t.Fatalf("count companies: %v", err)
	}
	if stored != 250 {
		t.Fatalf("stored rows = %d, want 250", stored)
	}

	requests := ps.pagesRequested()
	want := []string{"<absent>", "1", "2"}
	if len(requests) != len(want) {
		t.Fatalf("pages requested = %v, want %v", requests, want)
	}
	for i, w := range want {
		if requests[i] != w {
			t.Fatalf("pages requested = %v, want %v", requests, want)
		}
	}
	if got := countPage(requests, "1"); got != 1 {
		t.Fatalf("page 1 requested %d times, want exactly 1", got)
	}
}

// TestSyncDependentResource_WalksZeroBasedPagesFromPageZero is the same
// assertion for the dependent (per-parent) walker, which carries its own copy
// of the page-int fallback.
func TestSyncDependentResource_WalksZeroBasedPagesFromPageZero(t *testing.T) {
	ps, c := newPageServer(t, 250, 100, func(i int) string {
		return fmt.Sprintf(`{"contactId":%d,"name":"Contact %d"}`, 1000+i, i)
	})
	db := openTestStore(t)

	if _, _, err := db.UpsertBatch("companies", []json.RawMessage{
		json.RawMessage(`{"slug":"testco","name":"Test Company"}`),
	}); err != nil {
		t.Fatalf("seed companies: %v", err)
	}

	dep := dependentResourceDef{
		Name:          "contacts",
		ParentTable:   "companies",
		ParentIDParam: "companySlug",
		PathTemplate:  "/companies/{companySlug}/contacts",
		KeyField:      "slug",
		PathParams:    []dependentPathParamDef{{Param: "companySlug", Field: "slug"}},
	}
	res := syncDependentResource(context.Background(), c, db, dep, "", true, 0, false, nil, io.Discard)
	if res.Err != nil {
		t.Fatalf("syncDependentResource: %v", res.Err)
	}
	if res.Count != 250 {
		t.Fatalf("synced rows = %d, want 250", res.Count)
	}

	requests := ps.pagesRequested()
	want := []string{"<absent>", "1", "2"}
	if len(requests) != len(want) {
		t.Fatalf("pages requested = %v, want %v", requests, want)
	}
	for i, w := range want {
		if requests[i] != w {
			t.Fatalf("pages requested = %v, want %v", requests, want)
		}
	}
	if got := countPage(requests, "1"); got != 1 {
		t.Fatalf("page 1 requested %d times, want exactly 1", got)
	}
}

// TestPaginatedGet_AllFollowsBareArrayPages is the --all half of issue #12:
// a page-type endpoint answering with a bare array must be walked to the end
// instead of returning the first page.
func TestPaginatedGet_AllFollowsBareArrayPages(t *testing.T) {
	ps, c := newPageServer(t, 250, 100, func(i int) string {
		return fmt.Sprintf(`{"code":"%d","name":"Account %d"}`, 1500+i, i)
	})

	// Mirrors a generated read command: page defaults to "0", pageSize to the
	// spec's page size, pagination type and cursor param are both "page".
	data, err := paginatedGet(context.Background(), c, "/companies/testco/accounts", map[string]string{
		"page":     "0",
		"pageSize": "100",
	}, nil, true, "page", "page", "pageSize", "", "")
	if err != nil {
		t.Fatalf("paginatedGet: %v", err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(items) != 250 {
		t.Fatalf("--all returned %d items, want 250", len(items))
	}

	requests := ps.pagesRequested()
	want := []string{"0", "1", "2"}
	if len(requests) != len(want) {
		t.Fatalf("pages requested = %v, want %v", requests, want)
	}
	for i, w := range want {
		if requests[i] != w {
			t.Fatalf("pages requested = %v, want %v", requests, want)
		}
	}
	if got := countPage(requests, "1"); got != 1 {
		t.Fatalf("page 1 requested %d times, want exactly 1", got)
	}
}

// TestNextClientSidePaginationCursor_UnsetPageUsesSpecDefault pins the
// helper used by the object-shaped has_more branch: an unset page cursor
// means the spec default page (0 here), so the next page is 1, not 2.
func TestNextClientSidePaginationCursor_UnsetPageUsesSpecDefault(t *testing.T) {
	next, ok := nextClientSidePaginationCursor(map[string]string{"pageSize": "100"}, "page", "page", "pageSize")
	if !ok {
		t.Fatalf("nextClientSidePaginationCursor(unset page) not ok")
	}
	if want := strconv.Itoa(determinePaginationDefaults().firstPage + 1); next != want {
		t.Fatalf("next page = %q, want %q", next, want)
	}
	if next != "1" {
		t.Fatalf("next page = %q, want \"1\" (spec default page is 0)", next)
	}
}

// TestNextFullPagePageCursor_StopsOnShortPage keeps the --all walk from
// requesting a page past the end: a short page is the end-of-collection
// signal for a bare-array page API.
func TestNextFullPagePageCursor_StopsOnShortPage(t *testing.T) {
	params := map[string]string{"page": "2", "pageSize": "100"}
	if _, ok := nextFullPagePageCursor(params, "page", "page", "pageSize", 50); ok {
		t.Fatalf("short page advanced the cursor, want stop")
	}
	if _, ok := nextFullPagePageCursor(params, "page", "page", "pageSize", 0); ok {
		t.Fatalf("empty page advanced the cursor, want stop")
	}
	next, ok := nextFullPagePageCursor(params, "page", "page", "pageSize", 100)
	if !ok || next != "3" {
		t.Fatalf("full page = (%q, %v), want (\"3\", true)", next, ok)
	}
	if _, ok := nextFullPagePageCursor(params, "page", "offset", "pageSize", 100); ok {
		t.Fatalf("offset pagination advanced through the page helper, want stop")
	}
}

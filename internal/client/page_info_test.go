// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Pins the result-count header capture (issue #15): the four Fiken-Api-*
// headers are the only completeness signal a bare-array list response carries,
// and the generated request path discarded them.

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"fiken-cli/internal/config"
)

// headerServer serves a bare JSON array page and the Fiken pagination header
// block, exactly as Fiken's list endpoints do.
func headerServer(t *testing.T, page, pageSize, pageCount, resultCount int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderPage, strconv.Itoa(page))
		w.Header().Set(HeaderPageSize, strconv.Itoa(pageSize))
		w.Header().Set(HeaderPageCount, strconv.Itoa(pageCount))
		w.Header().Set(HeaderResultCount, strconv.Itoa(resultCount))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)
	c.NoCache = true
	return c
}

// TestSinkedGet_ParsesFikenPaginationHeaders is the core of issue #15: the
// body comes back as before and the four headers arrive parsed alongside it,
// through the one patched line in the generated request path.
func TestSinkedGet_ParsesFikenPaginationHeaders(t *testing.T) {
	c := headerServer(t, 0, 100, 3, 250, `[{"code":"1500"},{"code":"1501"}]`)

	ctx, sink := NewPageInfoContext(context.Background())
	data, err := c.Get(ctx, "/companies/testco/accounts", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	info := sink.PageInfo()
	if !info.Present || !info.HasResultCount || !info.HasPageCount {
		t.Fatalf("PageInfo = %+v, want Present, HasResultCount and HasPageCount", info)
	}
	if info.Page != 0 || info.PageSize != 100 || info.PageCount != 3 || info.ResultCount != 250 {
		t.Fatalf("PageInfo = %+v, want {0 100 3 250}", info)
	}
}

// TestSinkedGet_NoHeadersIsAbsentNotZero keeps a header-less response from
// claiming the collection holds zero rows.
func TestSinkedGet_NoHeadersIsAbsentNotZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	c := New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)
	c.NoCache = true

	ctx, sink := NewPageInfoContext(context.Background())
	if _, err := c.Get(ctx, "/companies/testco/accounts", nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	info := sink.PageInfo()
	if info.Present || info.HasResultCount || info.HasPageCount || info != (PageInfo{}) {
		t.Fatalf("PageInfo = %+v, want the zero value", info)
	}
}

// TestParsePageInfo_ZeroCountsArePresent is the empty-collection case: Fiken
// answers an empty collection with Page-Count: 0 / Result-Count: 0, which is a
// real claim about the collection and not the same as a missing header.
func TestParsePageInfo_ZeroCountsArePresent(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderPage, "0")
	h.Set(HeaderPageSize, "100")
	h.Set(HeaderPageCount, "0")
	h.Set(HeaderResultCount, "0")

	info := ParsePageInfo(h)
	if !info.Present || !info.HasPageCount || !info.HasResultCount {
		t.Fatalf("PageInfo = %+v, want every presence flag set", info)
	}
	if info.PageCount != 0 || info.ResultCount != 0 {
		t.Fatalf("PageInfo = %+v, want zero counts", info)
	}

	bare := ParsePageInfo(http.Header{})
	if bare.HasPageCount || bare.HasResultCount || bare.Present {
		t.Fatalf("PageInfo of an empty header block = %+v, want the zero value", bare)
	}
}

// TestPageInfoSink_KeepsFirstResponseAndResets pins the sink semantics the
// multi-page walkers rely on: page one wins, Reset starts a new collection.
func TestPageInfoSink_KeepsFirstResponseAndResets(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderPage, strconv.Itoa(page))
		w.Header().Set(HeaderResultCount, strconv.Itoa(250-page))
		w.Header().Set("Content-Type", "application/json")
		page++
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	c := New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)
	c.NoCache = true

	ctx, sink := NewPageInfoContext(context.Background())
	for i := 0; i < 3; i++ {
		if _, err := c.GetNoCache(ctx, "/companies/testco/accounts", nil); err != nil {
			t.Fatalf("GetNoCache: %v", err)
		}
	}
	if got := sink.PageInfo(); got.Page != 0 || got.ResultCount != 250 {
		t.Fatalf("after three pages PageInfo = %+v, want page 0 / result count 250", got)
	}
	sink.Reset()
	if got := sink.PageInfo(); got.Present {
		t.Fatalf("after Reset PageInfo = %+v, want the zero value", got)
	}
	if _, err := c.GetNoCache(ctx, "/companies/testco/accounts", nil); err != nil {
		t.Fatalf("GetNoCache: %v", err)
	}
	if got := sink.PageInfo(); got.Page != 3 || got.ResultCount != 247 {
		t.Fatalf("after Reset PageInfo = %+v, want page 3 / result count 247", got)
	}
}

// TestPageInfoSink_NilAndUnsetContextAreNoOps keeps the patched line in
// doInternal harmless for every caller that never asked for headers.
func TestPageInfoSink_NilAndUnsetContextAreNoOps(t *testing.T) {
	var nilSink *PageInfoSink
	if got := nilSink.PageInfo(); got.Present {
		t.Fatalf("nil sink PageInfo = %+v, want the zero value", got)
	}
	nilSink.Reset()
	h := http.Header{}
	h.Set(HeaderResultCount, "250")
	capturePageInfo(context.Background(), h) // no sink installed
	capturePageInfo(nil, h)
}

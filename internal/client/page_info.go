// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Result-count headers (issue #15). Fiken's list endpoints answer with a bare
// JSON array and no envelope, so the only completeness signal on the wire is
// the response header block `Fiken-Api-Page`, `Fiken-Api-Page-Size`,
// `Fiken-Api-Page-Count`, `Fiken-Api-Result-Count` (spec.yaml:247-255). The
// generated client threw the whole header away except Content-Type, which is
// why a short pull (issue #12: 477 of 577 accounts) could not be detected by
// anything — not sync, not doctor, not a paginated read's provenance envelope.
//
// The capture path is a context-value sink so the generated request path needs
// exactly one patched line (`capturePageInfo(ctx, resp.Header)` in
// (*Client).doInternal): a caller that wants the headers installs a sink on the
// context it passes down, everyone else pays a nil map lookup. The sink keeps
// the FIRST response that carried headers, so a multi-page walk reports the
// page-one counts (Result-Count is the collection total on every page) rather
// than whichever page happened to finish last; Reset starts a new collection,
// e.g. the next parent of a dependent sync.
//
// A cached response never reaches the wire, so it yields no headers: PageInfo
// is then the zero value with Present false and consumers omit the fields.
// There is deliberately no Get-plus-headers convenience method: the sink IS
// the read path (resolvePaginatedReadWithStrategy and both sync walkers install
// one), and a second entry point that bypassed the cache would be a parallel
// read path nothing in the CLI asks for.
package client

import (
	"context"
	"net/http"
	"strconv"
	"sync"
)

// Fiken's pagination header block, declared on 27 of the 40 list operations.
const (
	HeaderPage        = "Fiken-Api-Page"
	HeaderPageSize    = "Fiken-Api-Page-Size"
	HeaderPageCount   = "Fiken-Api-Page-Count"
	HeaderResultCount = "Fiken-Api-Result-Count"
)

// PageInfo is the parsed pagination header block of one list response.
// The zero value means "no headers were seen" (cache hit, non-list endpoint,
// or an operation that does not declare them), which is why every consumer
// must gate on Present before reading a count: a missing header and a real
// zero are not the same claim.
type PageInfo struct {
	Page        int
	PageSize    int
	PageCount   int
	ResultCount int
	// Present is true when at least one of the four headers was parsed.
	Present bool
	// HasResultCount is true when Fiken-Api-Result-Count specifically was
	// parsed. It gates the sync completeness check: a response carrying
	// Fiken-Api-Page but no result count must not be read as "the API says
	// this collection holds 0 rows".
	HasResultCount bool
	// HasPageCount is the same distinction for Fiken-Api-Page-Count: an
	// empty collection legitimately reports Page-Count: 0, so presence has
	// to be tracked separately from the value or that zero is unpublishable.
	HasPageCount bool
}

// ParsePageInfo reads the four Fiken-Api-* headers off a response header block.
// Unparseable or absent values leave their field at zero.
func ParsePageInfo(h http.Header) PageInfo {
	var info PageInfo
	read := func(name string, dst *int) bool {
		raw := h.Get(name)
		if raw == "" {
			return false
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			return false
		}
		*dst = n
		return true
	}
	if read(HeaderPage, &info.Page) {
		info.Present = true
	}
	if read(HeaderPageSize, &info.PageSize) {
		info.Present = true
	}
	if read(HeaderPageCount, &info.PageCount) {
		info.Present = true
		info.HasPageCount = true
	}
	if read(HeaderResultCount, &info.ResultCount) {
		info.Present = true
		info.HasResultCount = true
	}
	return info
}

// PageInfoSink collects the pagination headers of the responses made under one
// context. Safe for concurrent use; a nil sink is a no-op so callers never have
// to nil-check before reading.
type PageInfoSink struct {
	mu   sync.Mutex
	info PageInfo
}

type pageInfoSinkKey struct{}

// NewPageInfoContext returns a child context whose successful responses feed
// the returned sink. Install it once per collection walk.
func NewPageInfoContext(ctx context.Context) (context.Context, *PageInfoSink) {
	if ctx == nil {
		ctx = context.Background()
	}
	sink := &PageInfoSink{}
	return context.WithValue(ctx, pageInfoSinkKey{}, sink), sink
}

// PageInfo returns the headers of the first response in this collection that
// carried them.
func (s *PageInfoSink) PageInfo() PageInfo {
	if s == nil {
		return PageInfo{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info
}

// Reset drops the collected headers so the next response starts a new
// collection (the next parent of a dependent sync, say).
func (s *PageInfoSink) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.info = PageInfo{}
}

func (s *PageInfoSink) capture(h http.Header) {
	if s == nil {
		return
	}
	info := ParsePageInfo(h)
	if !info.Present {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.Present {
		return
	}
	s.info = info
}

// capturePageInfo is the one call the generated request path makes. It is a
// map lookup on a context without a sink.
func capturePageInfo(ctx context.Context, h http.Header) {
	if ctx == nil || h == nil {
		return
	}
	sink, _ := ctx.Value(pageInfoSinkKey{}).(*PageInfoSink)
	sink.capture(h)
}

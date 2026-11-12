// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The Location header of a create (issue #26). Fiken answers a create with
// 201, an empty body and `Location: <base>/companies/<slug>/purchases/<id>`.
// The generated client returned the body and the status and dropped the
// header, so nothing that created a purchase, sale, payment or journal entry
// could say which one it had made: every caller listed by date afterwards and
// matched on the invoice number, which picks the wrong row when two purchases
// share a date.
//
// Same shape as page_info.go: a context-value sink, so the generated request
// path needs one line (`captureLocation(ctx, resp.Header)` in doInternal) and
// no signature changes. The sink keeps the LAST Location seen, because a
// command that posts more than once reports the resource its final request
// created.
package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// LocationSink collects the Location header of the responses made under one
// context. Safe for concurrent use; a nil sink is a no-op.
type LocationSink struct {
	mu       sync.Mutex
	location string
}

type locationSinkKey struct{}

// NewLocationContext returns a child context whose successful responses feed
// the returned sink.
func NewLocationContext(ctx context.Context) (context.Context, *LocationSink) {
	if ctx == nil {
		ctx = context.Background()
	}
	sink := &LocationSink{}
	return context.WithValue(ctx, locationSinkKey{}, sink), sink
}

// Location returns the last Location header seen, or "" when none was.
func (s *LocationSink) Location() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.location
}

// ID returns the last path segment of the Location, which is the id of the
// resource a create made. Empty when no Location was seen.
func (s *LocationSink) ID() string {
	return LocationID(s.Location())
}

func (s *LocationSink) capture(h http.Header) {
	if s == nil {
		return
	}
	loc := strings.TrimSpace(h.Get("Location"))
	if loc == "" {
		return
	}
	s.mu.Lock()
	s.location = loc
	s.mu.Unlock()
}

// captureLocation is the one call the generated request path makes. It is a
// map lookup on a context without a sink.
func captureLocation(ctx context.Context, h http.Header) {
	if ctx == nil || h == nil {
		return
	}
	sink, _ := ctx.Value(locationSinkKey{}).(*LocationSink)
	sink.capture(h)
}

// LocationID extracts the created resource's id from a Location value: the
// last non-empty path segment, with any query or fragment dropped. Works on an
// absolute URL and on a bare path.
func LocationID(location string) string {
	location = strings.TrimSpace(location)
	if location == "" {
		return ""
	}
	path := location
	if u, err := url.Parse(location); err == nil && u.Path != "" {
		path = u.Path
	}
	path = strings.TrimRight(path, "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		path = path[i+1:]
	}
	return path
}

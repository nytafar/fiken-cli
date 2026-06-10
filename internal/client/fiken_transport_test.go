// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL). The first test is the load-bearing tripwire: the
// cross-process lock in fiken_transport.go only intercepts requests because the
// generated client leaves http.Client.Transport nil (so it uses the wrapped
// http.DefaultTransport). If a generator change ever sets an explicit
// Transport, this test fails — surfacing a silent lock bypass before it ships.
package client

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewHTTPClientTransportNil(t *testing.T) {
	c := newHTTPClient(5*time.Second, nil)
	if c.Transport != nil {
		t.Fatalf("newHTTPClient().Transport = %T, want nil so requests flow through the wrapped http.DefaultTransport single-flight lock; "+
			"a non-nil Transport silently bypasses fiken_transport.go", c.Transport)
	}
}

// recordingRT is a stub base transport that records the last request and
// returns an empty 200 without touching the network.
type recordingRT struct{ last *http.Request }

func (r *recordingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.last = req
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func TestFikenRoundTripperStampsRequestIDForFikenHost(t *testing.T) {
	t.Setenv("FIKEN_NO_LOCK", "1") // exercise stamping + body-wrap without a real file lock
	base := &recordingRT{}
	rt := &fikenRoundTripper{base: base}

	req, _ := http.NewRequest("GET", "https://api.fiken.no/api/v2/companies", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()

	if got := base.last.Header.Get("X-Request-ID"); got == "" {
		t.Error("X-Request-ID not stamped for Fiken host")
	}
	if req.Header.Get("X-Request-ID") != "" {
		t.Error("RoundTrip mutated the input request; it must clone before stamping")
	}
	if _, ok := resp.Body.(*fikenLockBody); !ok {
		t.Errorf("response body = %T, want *fikenLockBody so the lock releases on Close", resp.Body)
	}
}

func TestFikenRoundTripperPassesThroughNonFikenHost(t *testing.T) {
	base := &recordingRT{}
	rt := &fikenRoundTripper{base: base}

	req, _ := http.NewRequest("GET", "https://example.com/health", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()

	if base.last.Header.Get("X-Request-ID") != "" {
		t.Error("non-Fiken host should pass through without an X-Request-ID stamp")
	}
	if _, ok := resp.Body.(*fikenLockBody); ok {
		t.Error("non-Fiken host should not be wrapped in the lock body")
	}
}

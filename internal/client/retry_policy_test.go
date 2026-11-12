// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — a write is sent once; a read is retried.
package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"fiken-cli/internal/config"
)

func retryClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("FIKEN_NO_LOCK", "1")
	c := New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)
	c.NoCache = true
	return c
}

func TestWriteIsNotRetriedAfterAServerError(t *testing.T) {
	var posts atomic.Int32
	c := retryClient(t, func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		// The create was carried out, and the answer is a gateway error.
		http.Error(w, "bad gateway", http.StatusBadGateway)
	})
	if _, _, err := c.Post(context.Background(), "/companies/agensia/purchases", map[string]any{"x": 1}); err == nil {
		t.Fatal("a 502 must be returned as an error")
	}
	if got := posts.Load(); got != 1 {
		t.Fatalf("the POST was sent %d times; a write whose outcome is unknown must be sent once", got)
	}
}

func TestWriteIsNotRetriedAfterALostAnswer(t *testing.T) {
	var posts atomic.Int32
	c := retryClient(t, func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		// Carried out, then the connection drops before any answer.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("server cannot hijack")
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	})
	if _, _, err := c.Post(context.Background(), "/companies/agensia/purchases", map[string]any{"x": 1}); err == nil {
		t.Fatal("a dropped connection must be returned as an error")
	}
	if got := posts.Load(); got != 1 {
		t.Fatalf("the POST was sent %d times after a lost answer, want 1", got)
	}
}

func TestReadIsStillRetriedAfterAServerError(t *testing.T) {
	var gets atomic.Int32
	c := retryClient(t, func(w http.ResponseWriter, r *http.Request) {
		if gets.Add(1) == 1 {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	if _, err := c.Get(context.Background(), "/companies/agensia/purchases", nil); err != nil {
		t.Fatalf("a read must survive one 502: %v", err)
	}
	if got := gets.Load(); got != 2 {
		t.Fatalf("the GET was sent %d times, want 2", got)
	}
}

func TestRetryAfterAmbiguousFailure(t *testing.T) {
	for method, want := range map[string]bool{"GET": true, "HEAD": true, "POST": false, "PUT": false, "PATCH": false, "DELETE": false} {
		if got := retryAfterAmbiguousFailure(method); got != want {
			t.Errorf("%s = %v, want %v", method, got, want)
		}
	}
}

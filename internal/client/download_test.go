// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// DownloadFile (issue #23): the bytes, the name, and the one rule that keeps
// a download from leaking the bearer token — auth rides only to the host the
// client is configured for.

package client

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"fiken-cli/internal/config"
)

func TestDownloadFile_BytesNameAndAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="faktura-123.pdf"`)
		_, _ = w.Write([]byte("%PDF-1.4 body"))
	}))
	defer srv.Close()

	c := New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)

	var buf bytes.Buffer
	got, err := c.DownloadFile(context.Background(), srv.URL+"/download/1", &buf)
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if buf.String() != "%PDF-1.4 body" {
		t.Fatalf("body = %q", buf.String())
	}
	if got.Bytes != int64(len("%PDF-1.4 body")) {
		t.Fatalf("bytes = %d", got.Bytes)
	}
	if got.Filename != "faktura-123.pdf" {
		t.Fatalf("filename = %q, want the Content-Disposition name", got.Filename)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

func TestDownloadFile_ForeignHostGetsNoCredential(t *testing.T) {
	var gotAuth = "unset"
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("bytes"))
	}))
	defer foreign.Close()

	// Base URL points somewhere else entirely: the token must not follow the
	// URL off the API host.
	c := New(&config.Config{BaseURL: "https://api.fiken.no/api/v2", AccessToken: "test-token"}, 10*time.Second, 0)

	var buf bytes.Buffer
	if _, err := c.DownloadFile(context.Background(), foreign.URL+"/x", &buf); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization sent to a foreign host: %q", gotAuth)
	}
}

func TestDownloadFile_ErrorStatusIsAnAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)

	var buf bytes.Buffer
	_, err := c.DownloadFile(context.Background(), srv.URL+"/missing", &buf)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("err = %v (%T), want *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", apiErr.StatusCode)
	}
	if buf.Len() != 0 {
		t.Fatalf("error body was written to the sink: %q", buf.String())
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"faktura.pdf":         "faktura.pdf",
		"../../etc/passwd":    "passwd",
		`..\..\windows\x.pdf`: "x.pdf",
		"":                    "",
		"   ":                 "",
		"..":                  "",
		"/":                   "",
	}
	for in, want := range cases {
		if got := SanitizeFilename(in); got != want {
			t.Fatalf("SanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

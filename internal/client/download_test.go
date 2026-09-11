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
	"strings"
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

// A Fiken document URL ends in the document id and the response often
// carries no Content-Disposition, so the name resolves to "42". Writing that
// to disk gives an extensionless file no viewer opens, when the response said
// application/pdf all along.
func TestDownloadFile_ExtensionlessNameTakesItFromContentType(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		path        string
		want        string
	}{
		{"pdf", "application/pdf", "/companies/agensia/inbox/42/document", "document.pdf"},
		{"pdf from url id", "application/pdf; charset=binary", "/download/42", "42.pdf"},
		{"png", "image/png", "/download/7", "7.png"},
		{"jpeg", "image/jpeg", "/download/7", "7.jpg"},
		{"gif", "image/gif", "/download/7", "7.gif"},
		{"unknown type adds nothing", "application/x-nonsense-nothing", "/download/7", "7"},
		{"no content type adds nothing", "", "/download/7", "7"},
		{"existing extension is kept", "application/pdf", "/download/faktura.xml", "faktura.xml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.contentType == "" {
					// Go sniffs a Content-Type for a written body unless it
					// is told not to.
					w.Header()["Content-Type"] = nil
				} else {
					w.Header().Set("Content-Type", tc.contentType)
				}
				_, _ = w.Write([]byte("%PDF-1.4 body"))
			}))
			defer srv.Close()

			c := New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)

			var buf bytes.Buffer
			got, err := c.DownloadFile(context.Background(), srv.URL+tc.path, &buf)
			if err != nil {
				t.Fatalf("DownloadFile: %v", err)
			}
			if got.Filename != tc.want {
				t.Fatalf("filename = %q, want %q (Content-Type %q)", got.Filename, tc.want, tc.contentType)
			}
		})
	}
}

// A Content-Disposition name is trusted for the stem but still gets the
// extension the content type implies when it has none.
func TestDownloadFile_ContentDispositionNameGetsExtension(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="faktura-123"`)
		_, _ = w.Write([]byte("%PDF-1.4 body"))
	}))
	defer srv.Close()

	c := New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)

	var buf bytes.Buffer
	got, err := c.DownloadFile(context.Background(), srv.URL+"/download/1", &buf)
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if got.Filename != "faktura-123.pdf" {
		t.Fatalf("filename = %q, want %q", got.Filename, "faktura-123.pdf")
	}
}

func TestEnsureFilenameExtension(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		want        string
	}{
		{"42", "application/pdf", "42.pdf"},
		{"42.pdf", "image/png", "42.pdf"},
		{"", "application/pdf", ""},
		{"scan", "application/x-nonsense-nothing", "scan"},
	}
	for _, tc := range cases {
		got := EnsureFilenameExtension(tc.name, tc.contentType)
		if got != tc.want {
			t.Fatalf("EnsureFilenameExtension(%q, %q) = %q, want %q", tc.name, tc.contentType, got, tc.want)
		}
	}

	// Outside the explicit map the system mime table decides, so only the
	// shape is host-independent: some extension, one path element.
	if got := EnsureFilenameExtension("scan", "text/plain; charset=utf-8"); !strings.HasPrefix(got, "scan.") {
		t.Fatalf("EnsureFilenameExtension(\"scan\", \"text/plain\") = %q, want a text extension from the system mime table", got)
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

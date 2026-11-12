// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — tests for the Location sink (issue #26).
package client

import (
	"context"
	"net/http"
	"testing"
)

func TestLocationID(t *testing.T) {
	tests := map[string]string{
		"https://api.fiken.no/api/v2/companies/agensia/purchases/900123":       "900123",
		"https://api.fiken.no/api/v2/companies/agensia/purchases/900123/":      "900123",
		"/companies/agensia/purchases/900123/payments/77":                      "77",
		"https://api.fiken.no/api/v2/companies/agensia/invoices/drafts/ab-cd":  "ab-cd",
		"https://api.fiken.no/api/v2/companies/agensia/purchases/900123?x=y#z": "900123",
		"": "",
	}
	for in, want := range tests {
		if got := LocationID(in); got != want {
			t.Errorf("LocationID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLocationSink(t *testing.T) {
	ctx, sink := NewLocationContext(context.Background())
	if sink.Location() != "" || sink.ID() != "" {
		t.Fatal("a fresh sink must be empty")
	}
	captureLocation(ctx, http.Header{"Content-Type": {"application/json"}})
	if sink.Location() != "" {
		t.Fatal("a response without Location must not fill the sink")
	}
	captureLocation(ctx, http.Header{"Location": {"/companies/agensia/purchases/1"}})
	captureLocation(ctx, http.Header{"Location": {"/companies/agensia/purchases/1/payments/2"}})
	if sink.ID() != "2" {
		t.Fatalf("id = %q, want the last Location's id", sink.ID())
	}

	// The patched doInternal line must be harmless without a sink.
	captureLocation(context.Background(), http.Header{"Location": {"/x/1"}})
	captureLocation(nil, http.Header{"Location": {"/x/1"}}) //nolint:staticcheck // nil ctx is the case under test
	var none *LocationSink
	if none.Location() != "" || none.ID() != "" {
		t.Fatal("a nil sink must read as empty")
	}
}

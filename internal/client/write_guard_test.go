// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Pins the test-company write guard (write_guard.go, PLAN 2.4.1): the full
// decision table, the fail-closed default, and the two enforcement points
// (transport and client). Every case here is offline — a recording
// RoundTripper stands in for the network and is asserted never to be called.
package client

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"fiken-cli/internal/config"
)

// guardState is one row of the "how is the guard configured" axis.
type guardState struct {
	name      string
	configure func()
	// wantCompanyCode is the denial code expected for a mutating request on a
	// company-scoped Fiken path; "" means the request must be allowed.
	wantCompanyCode string
}

func guardStates() []guardState {
	resolver := func(isTest, known bool) func(string) (bool, bool) {
		return func(string) (bool, bool) { return isTest, known }
	}
	return []guardState{
		{"unconfigured", func() { ResetWriteGuardForTest() }, GuardCodeUnconfigured},
		{"test+isTest", func() { ConfigureWriteGuard(ModeTest, resolver(true, true)) }, ""},
		{"test+notTest", func() { ConfigureWriteGuard(ModeTest, resolver(false, true)) }, GuardCodeNotTestCompany},
		{"test+unknown", func() { ConfigureWriteGuard(ModeTest, resolver(false, false)) }, GuardCodeCompanyUnknown},
		{"live+isTest", func() { ConfigureWriteGuard(ModeLive, resolver(true, true)) }, ""},
		{"live+notTest", func() { ConfigureWriteGuard(ModeLive, resolver(false, true)) }, ""},
		{"live+unknown", func() { ConfigureWriteGuard(ModeLive, resolver(false, false)) }, GuardCodeCompanyUnknown},
	}
}

// TestGuardMutation_DecisionTable walks every verb x guard state x path shape.
func TestGuardMutation_DecisionTable(t *testing.T) {
	const (
		companyPath = "https://api.fiken.no/api/v2/companies/testco/contacts"
		tokenPath   = "https://fiken.no/oauth/token"
		noCompany   = "https://api.fiken.no/api/v2/companies"
		otherHost   = "https://example.test/api/v2/companies/testco/contacts"
	)
	verbs := []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

	for _, state := range guardStates() {
		for _, verb := range verbs {
			mutating := verb != "GET"
			cases := []struct {
				path string
				want string
			}{
				// Company-scoped Fiken path: the decision table proper.
				{companyPath, ifMutating(mutating, state.wantCompanyCode)},
				// OAuth token endpoint: allowlisted in every state.
				{tokenPath, ""},
				// Fiken host, mutating, but no company in the path.
				{noCompany, ifMutating(mutating, GuardCodeNoCompany)},
				// Not a Fiken host: never the guard's business.
				{otherHost, ""},
			}
			for _, tc := range cases {
				name := state.name + "/" + verb + "/" + tc.path
				t.Run(name, func(t *testing.T) {
					t.Cleanup(ResetWriteGuardForTest)
					state.configure()

					u, err := url.Parse(tc.path)
					if err != nil {
						t.Fatalf("parse %q: %v", tc.path, err)
					}
					err = GuardMutation(verb, u)
					if tc.want == "" {
						if err != nil {
							t.Fatalf("GuardMutation(%s, %s) = %v, want allow", verb, tc.path, err)
						}
						return
					}
					var guardErr *WriteGuardError
					if !errors.As(err, &guardErr) {
						t.Fatalf("GuardMutation(%s, %s) = %v, want *WriteGuardError code %q", verb, tc.path, err, tc.want)
					}
					if guardErr.Code != tc.want {
						t.Fatalf("code = %q, want %q", guardErr.Code, tc.want)
					}
					if guardErr.Method != verb {
						t.Errorf("Method = %q, want %q", guardErr.Method, verb)
					}
					if guardErr.Path != u.Path {
						t.Errorf("Path = %q, want %q", guardErr.Path, u.Path)
					}
				})
			}
		}
	}
}

// ifMutating returns code for mutating verbs and "" (allow) for read verbs.
func ifMutating(mutating bool, code string) string {
	if !mutating {
		return ""
	}
	return code
}

// TestGuardMutation_DefaultStateDenies is the load-bearing property stated on
// its own: a guard nobody configured refuses a real mutating Fiken request.
func TestGuardMutation_DefaultStateDenies(t *testing.T) {
	t.Cleanup(ResetWriteGuardForTest)
	ResetWriteGuardForTest()

	u, _ := url.Parse("https://api.fiken.no/api/v2/companies/testco/contacts")
	var guardErr *WriteGuardError
	if err := GuardMutation("POST", u); !errors.As(err, &guardErr) {
		t.Fatalf("unconfigured guard allowed a POST: %v", err)
	}
	if guardErr.Code != GuardCodeUnconfigured {
		t.Fatalf("code = %q, want %q", guardErr.Code, GuardCodeUnconfigured)
	}
	if guardErr.Slug != "testco" {
		t.Fatalf("Slug = %q, want %q", guardErr.Slug, "testco")
	}
}

// TestGuardMutation_OnDenySink asserts the audit hook fires on every denial
// and is not consulted on an allow.
func TestGuardMutation_OnDenySink(t *testing.T) {
	t.Cleanup(ResetWriteGuardForTest)
	ResetWriteGuardForTest()

	var seen []*WriteGuardError
	SetWriteGuardOnDeny(func(e *WriteGuardError) { seen = append(seen, e) })

	u, _ := url.Parse("https://api.fiken.no/api/v2/companies/testco/contacts")
	_ = GuardMutation("POST", u)
	_ = GuardMutation("GET", u)
	if len(seen) != 1 {
		t.Fatalf("OnDeny fired %d times, want 1", len(seen))
	}
	if seen[0].Code != GuardCodeUnconfigured {
		t.Fatalf("sink saw code %q, want %q", seen[0].Code, GuardCodeUnconfigured)
	}
}

// TestFikenRoundTripper_GuardDeniesBeforeBase is the durable half: the
// hand-authored transport must refuse before the base transport is touched.
func TestFikenRoundTripper_GuardDeniesBeforeBase(t *testing.T) {
	t.Cleanup(ResetWriteGuardForTest)
	ResetWriteGuardForTest()

	rec := &recordingRoundTripper{}
	rt := &fikenRoundTripper{base: rec}

	req, err := http.NewRequest("POST",
		"https://api.fiken.no/api/v2/companies/nyta/contacts", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := rt.RoundTrip(req)
	if resp != nil {
		t.Errorf("RoundTrip returned a response; want (nil, *WriteGuardError)")
	}
	var guardErr *WriteGuardError
	if !errors.As(err, &guardErr) {
		t.Fatalf("RoundTrip err = %v, want *WriteGuardError", err)
	}
	if guardErr.Slug != "nyta" {
		t.Errorf("Slug = %q, want %q", guardErr.Slug, "nyta")
	}
	if rec.calls != 0 {
		t.Fatalf("base transport called %d times; nothing may reach the wire", rec.calls)
	}
}

// TestFikenRoundTripper_TokenEndpointAllowed pins the allowlist that keeps
// refreshAccessToken working: it posts to the token endpoint directly,
// bypassing doInternal, so only the transport guard sees it.
func TestFikenRoundTripper_TokenEndpointAllowed(t *testing.T) {
	t.Cleanup(ResetWriteGuardForTest)
	ResetWriteGuardForTest()
	t.Setenv("FIKEN_NO_LOCK", "1")

	rec := &recordingRoundTripper{}
	rt := &fikenRoundTripper{base: rec}

	req, err := http.NewRequest("POST", "https://fiken.no/oauth/token",
		bytes.NewReader([]byte("grant_type=refresh_token")))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip on the token endpoint = %v, want it allowed", err)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	if rec.calls != 1 {
		t.Fatalf("base transport called %d times, want 1", rec.calls)
	}
}

// TestClient_GuardDeniesBeforeRequest is the ergonomic half: a *Client built
// the way root.go builds one refuses in doInternal, before a request is built
// or a body encoded.
func TestClient_GuardDeniesBeforeRequest(t *testing.T) {
	t.Cleanup(ResetWriteGuardForTest)
	ResetWriteGuardForTest()

	rec := &recordingRoundTripper{}
	cfg := &config.Config{BaseURL: "https://api.fiken.no/api/v2", AccessToken: "test-token"}
	c := New(cfg, time.Second, 0)
	c.HTTPClient = &http.Client{Transport: rec}
	c.NoCache = true

	_, _, err := c.Post(context.Background(), "/companies/testco/contacts", map[string]any{"name": "x"})
	var guardErr *WriteGuardError
	if !errors.As(err, &guardErr) {
		t.Fatalf("Post err = %v, want *WriteGuardError", err)
	}
	if guardErr.Code != GuardCodeUnconfigured {
		t.Errorf("code = %q, want %q", guardErr.Code, GuardCodeUnconfigured)
	}
	if rec.calls != 0 {
		t.Fatalf("transport called %d times; the guard must fire before any dial", rec.calls)
	}
}

// TestClient_GuardAllowsTestCompany is the positive control: with the guard
// configured for a test company, the same POST reaches the transport.
func TestClient_GuardAllowsTestCompany(t *testing.T) {
	t.Cleanup(ResetWriteGuardForTest)
	ConfigureWriteGuard(ModeTest, func(slug string) (bool, bool) {
		return slug == "testco", slug == "testco"
	})

	rec := &recordingRoundTripper{}
	cfg := &config.Config{BaseURL: "https://api.fiken.no/api/v2", AccessToken: "test-token"}
	c := New(cfg, time.Second, 0)
	c.HTTPClient = &http.Client{Transport: rec}
	c.NoCache = true

	if _, _, err := c.Post(context.Background(), "/companies/testco/contacts", map[string]any{"name": "x"}); err != nil {
		t.Fatalf("Post against a test company = %v, want it allowed", err)
	}
	if rec.calls != 1 {
		t.Fatalf("transport called %d times, want 1", rec.calls)
	}
}

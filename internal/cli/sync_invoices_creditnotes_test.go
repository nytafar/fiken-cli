// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Pins the two dependents added for issue #18. sync covered 11 of the API's
// ~30 list collections and `--resources invoices` failed with "unknown sync
// resource", so a mirror held no issueDate, dueDate, invoiceNumber or
// dispatches[] at all — the sale blob carries none of them — and no credit
// history. The change is a data-table entry, not a new code path, so what has
// to be pinned is the table: the endpoint paths, the id fields the rows are
// keyed on, the company suffix in the storage key, pagination, and the
// lastModifiedGe window both operations declare.
//
// Reuses the seedCompanies / dayBefore / dateParamsOf helpers from
// sync_since_test.go.

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fiken-cli/internal/store"
)

// collectionServer is a fake Fiken that serves a different bare array per
// collection path, so one sync run can walk two resources and each can be
// checked on its own. Every response carries the Fiken-Api-Result-Count the
// completeness check reads, set to the number of rows actually served.
type collectionServer struct {
	rows map[string][]string // last path segment -> rows

	mu     sync.Mutex
	params map[string][]url.Values
}

func (s *collectionServer) collectionOf(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return path
	}
	return path[idx+1:]
}

func (s *collectionServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := s.collectionOf(r.URL.Path)
	s.mu.Lock()
	if s.params == nil {
		s.params = map[string][]url.Values{}
	}
	s.params[name] = append(s.params[name], r.URL.Query())
	s.mu.Unlock()

	rows, ok := s.rows[name]
	if !ok {
		http.Error(w, "no such collection "+name, http.StatusNotFound)
		return
	}
	// Page 0 serves everything; any later page is empty, which is how the
	// walker learns the collection ended.
	items := make([]json.RawMessage, 0, len(rows))
	if page := r.URL.Query().Get("page"); page == "" || page == "0" {
		for _, row := range rows {
			items = append(items, json.RawMessage(row))
		}
	}
	body, _ := json.Marshal(items)
	w.Header().Set("Fiken-Api-Result-Count", fmt.Sprintf("%d", len(rows)))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (s *collectionServer) requestsFor(collection string) []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]url.Values, len(s.params[collection]))
	copy(out, s.params[collection])
	return out
}

func invoiceCreditNoteServer(t *testing.T) *collectionServer {
	t.Helper()
	return &collectionServer{rows: map[string][]string{
		"invoices": {
			`{"invoiceId":4001,"invoiceNumber":"10001","issueDate":"2026-01-05","dueDate":"2026-02-04","lastModifiedDate":"2026-01-06"}`,
			`{"invoiceId":4002,"invoiceNumber":"10002","issueDate":"2026-01-06","dueDate":"2026-02-05","lastModifiedDate":"2026-01-07"}`,
		},
		"creditNotes": {
			`{"creditNoteId":9001,"creditNoteNumber":"5001","issueDate":"2026-02-01","lastModifiedDate":"2026-02-02"}`,
		},
	}}
}

// runSyncCommand drives the real sync command against srv with a fresh mirror
// seeded with one company, and returns the NDJSON event stream.
func runSyncCommand(t *testing.T, srv *collectionServer, args ...string) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)
	t.Setenv("FIKEN_BASE_URL", httpSrv.URL)
	t.Setenv("FIKEN_API_TOKEN", "test-token")

	dbPath := filepath.Join(home, "data.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	seedCompanies(t, db, "testco")
	_ = db.Close()

	prevScope := syncCompanyScope
	t.Cleanup(func() { syncCompanyScope = prevScope })

	var out bytes.Buffer
	cmd := newSyncCmd(&rootFlags{timeout: 10 * time.Second, dataSource: "auto"})
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(append([]string{"--db", dbPath, "--company", "testco"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync %v: %v\n%s", args, err, out.String())
	}
	return out.String(), dbPath
}

// syncSummaryOf returns the single sync_summary event on an NDJSON stream.
func syncSummaryOf(t *testing.T, events string) map[string]any {
	t.Helper()
	var found map[string]any
	for _, line := range strings.Split(events, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev["event"] == "sync_summary" {
			if found != nil {
				t.Fatalf("more than one sync_summary:\n%s", events)
			}
			found = ev
		}
	}
	if found == nil {
		t.Fatalf("no sync_summary event:\n%s", events)
	}
	return found
}

// TestSync_InvoicesAndCreditNotesLandKeyedRows is issue #18's acceptance:
// naming the two new resources is no longer "unknown sync resource", and the
// rows land under invoiceId / creditNoteId with the company in the key.
func TestSync_InvoicesAndCreditNotesLandKeyedRows(t *testing.T) {
	srv := invoiceCreditNoteServer(t)
	events, dbPath := runSyncCommand(t, srv, "--resources", "invoices,credit_notes")

	summary := syncSummaryOf(t, events)
	if got := summary["resources"]; got != float64(2) {
		t.Errorf("sync_summary resources = %v, want 2\n%s", got, events)
	}
	if got := summary["success"]; got != float64(2) {
		t.Errorf("sync_summary success = %v, want 2\n%s", got, events)
	}
	if got := summary["errored"]; got != float64(0) {
		t.Errorf("sync_summary errored = %v, want 0\n%s", got, events)
	}

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if n, err := db.CountCompanyResources("invoices", "testco"); err != nil || n != 2 {
		t.Errorf("invoices rows for testco = (%d, %v), want (2, nil)", n, err)
	}
	if n, err := db.CountCompanyResources("credit_notes", "testco"); err != nil || n != 1 {
		t.Errorf("credit_notes rows for testco = (%d, %v), want (1, nil)", n, err)
	}

	// The storage key, not just the count: id from the override field, and the
	// company suffix every parent-keyed resource needs so two companies'
	// invoiceId ranges cannot collide on the resources primary key.
	nul := string([]byte{0})
	for _, tc := range []struct{ resource, key, field, value string }{
		{"invoices", "4001" + nul + "testco", "invoiceNumber", "10001"},
		{"invoices", "4002" + nul + "testco", "invoiceNumber", "10002"},
		{"credit_notes", "9001" + nul + "testco", "creditNoteNumber", "5001"},
	} {
		data, err := db.Get(tc.resource, tc.key)
		if err != nil {
			t.Fatalf("%s row %q: %v", tc.resource, tc.key, err)
		}
		var obj map[string]any
		if err := json.Unmarshal(data, &obj); err != nil {
			t.Fatalf("%s row %q: %v", tc.resource, tc.key, err)
		}
		if obj[tc.field] != tc.value {
			t.Errorf("%s row %q has %s = %v, want %q", tc.resource, tc.key, tc.field, obj[tc.field], tc.value)
		}
		if obj["parent_id"] != "testco" {
			t.Errorf("%s row %q has parent_id = %v, want testco", tc.resource, tc.key, obj["parent_id"])
		}
	}
}

// TestSync_InvoicesAndCreditNotesSendLastModifiedGe: both operations declare
// the shared lastModifiedGe parameter (spec.yaml:1381 and :1815), so --since
// must reach the wire as a real window rather than degrade to a full pull with
// a resource_not_incremental warning.
func TestSync_InvoicesAndCreditNotesSendLastModifiedGe(t *testing.T) {
	ts, err := parseSinceDuration("7d")
	if err != nil {
		t.Fatalf("parseSinceDuration: %v", err)
	}
	want := dayBefore(t, ts)

	srv := invoiceCreditNoteServer(t)
	events, _ := runSyncCommand(t, srv, "--resources", "invoices,credit_notes", "--since", "7d")

	for _, collection := range []string{"invoices", "creditNotes"} {
		reqs := srv.requestsFor(collection)
		if len(reqs) == 0 {
			t.Fatalf("no request reached /%s", collection)
		}
		for i, q := range reqs {
			if got := q.Get("lastModifiedGe"); got != want {
				t.Errorf("/%s request %d lastModifiedGe = %q, want %q", collection, i, got, want)
			}
			if dates := dateParamsOf(q); len(dates) != 1 || dates[0] != "lastModifiedGe" {
				t.Errorf("/%s request %d carried date params %v, want only lastModifiedGe", collection, i, dates)
			}
		}
	}
	if warns := warningsWithReason(events, "resource_not_incremental"); len(warns) != 0 {
		t.Errorf("a mapped resource warned resource_not_incremental: %s", events)
	}
}

// TestDependentResourceDefs_InvoicesAndCreditNotes pins the data table itself:
// the endpoint paths, that both are known names sync accepts, and that both
// are paginated. A reprint that drops either entry fails here rather than
// silently shrinking the mirror back to 11 collections.
func TestDependentResourceDefs_InvoicesAndCreditNotes(t *testing.T) {
	want := map[string]string{
		"invoices":     "/companies/{companySlug}/invoices",
		"credit_notes": "/companies/{companySlug}/creditNotes",
	}
	found := map[string]bool{}
	for _, dep := range dependentResourceDefs() {
		path, ok := want[dep.Name]
		if !ok {
			continue
		}
		found[dep.Name] = true
		if dep.PathTemplate != path {
			t.Errorf("%s PathTemplate = %q, want %q", dep.Name, dep.PathTemplate, path)
		}
		if dep.ParentTable != "companies" || dep.ParentIDParam != "companySlug" || dep.KeyField != "slug" {
			t.Errorf("%s is not keyed on the company slug: %+v", dep.Name, dep)
		}
	}
	for name := range want {
		if !found[name] {
			t.Errorf("dependentResourceDefs has no %q entry", name)
		}
		if !resourceSupportsPagination(name) {
			t.Errorf("resourceSupportsPagination(%q) = false; both list operations declare page/pageSize", name)
		}
		if !isDependentSyncResource(name) {
			t.Errorf("isDependentSyncResource(%q) = false; the flat pool would run it and fail", name)
		}
	}

	known := map[string]bool{}
	for _, name := range knownSyncResourceNames() {
		known[name] = true
	}
	for name := range want {
		if !known[name] {
			t.Errorf("knownSyncResourceNames() omits %q, so --resources %s is rejected", name, name)
		}
	}
}

// TestSyncSinceParam_InvoicesAndCreditNotes pins the since mapping for the two
// new names next to the five the sync-since-last-modified patch mapped.
func TestSyncSinceParam_InvoicesAndCreditNotes(t *testing.T) {
	for _, resource := range []string{"invoices", "credit_notes"} {
		if got := syncResourceSinceParam(resource); got != "lastModifiedGe" {
			t.Errorf("syncResourceSinceParam(%q) = %q, want lastModifiedGe", resource, got)
		}
		if got := syncResourceSinceParamFormat(resource); got != "date" {
			t.Errorf("syncResourceSinceParamFormat(%q) = %q, want date", resource, got)
		}
	}
}

// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Pins deletion detection (issue #17) at the walker: a complete, unwindowed,
// untruncated pull removes what the API stopped serving and says so, and every
// pull that cannot prove it saw the whole collection removes nothing. Reuses
// the rowServer / deniedParentServer harnesses from pagination_headers_test.go
// and the contacts fixtures from sync_since_test.go.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fiken-cli/internal/client"
	"fiken-cli/internal/config"
	"fiken-cli/internal/store"
)

// deletedEvents returns the sync_deleted events on an NDJSON stream.
func deletedEvents(t *testing.T, events string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(events, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev["event"] == "sync_deleted" {
			out = append(out, ev)
		}
	}
	return out
}

// contactIDsInMirror returns the contactId of every contacts row the mirror
// holds for a company, so a test can name the row that should have gone.
func contactIDsInMirror(t *testing.T, db *store.Store, slug string) []int64 {
	t.Helper()
	rows, err := db.Query(
		`SELECT json_extract(data, '$.contactId') FROM resources
		  WHERE resource_type = 'contacts' AND json_extract(data, '$.parent_id') = ?
		  ORDER BY 1`, slug)
	if err != nil {
		t.Fatalf("query mirror: %v", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// TestSyncDependentResource_CompletePullDeletesRowsTheAPIStoppedServing is
// issue #17 itself: the mirror had no way to lose a record deleted in Fiken.
// Three contacts land, Fiken then serves two, and the third is gone from the
// mirror with one sync_deleted event saying so.
func TestSyncDependentResource_CompletePullDeletesRowsTheAPIStoppedServing(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "testco")

	_, first := newRowServer(t, contactRows(3), 100, 3)
	var firstEvents bytes.Buffer
	if res := syncDependentResource(context.Background(), first, db, contactsDep(), "", false, 0, false, nil, &firstEvents); res.Err != nil {
		t.Fatalf("first sync: %v", res.Err)
	}
	if got := contactIDsInMirror(t, db, "testco"); len(got) != 3 {
		t.Fatalf("mirror holds %v after the first sync, want 3 rows", got)
	}
	if evs := deletedEvents(t, firstEvents.String()); len(evs) != 0 {
		t.Fatalf("first sync emitted %d sync_deleted events, want 0\n%s", len(evs), firstEvents.String())
	}

	// Contact 1002 is deleted in Fiken: the collection is now two rows and the
	// header says so.
	_, second := newRowServer(t, contactRows(2), 100, 2)
	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), second, db, contactsDep(), "", false, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("second sync: %v", res.Err)
	}

	got := contactIDsInMirror(t, db, "testco")
	want := []int64{1000, 1001}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("mirror holds %v, want %v: the row the API stopped serving must go", got, want)
	}
	evs := deletedEvents(t, events.String())
	if len(evs) != 1 {
		t.Fatalf("got %d sync_deleted events, want 1\n%s", len(evs), events.String())
	}
	if evs[0]["resource"] != "contacts" || evs[0]["company"] != "testco" || evs[0]["count"] != float64(1) {
		t.Fatalf("sync_deleted = %v, want resource contacts, company testco, count 1", evs[0])
	}
	if anomalies := countAnomalies(t, events.String(), "result_count_mismatch"); len(anomalies) != 0 {
		t.Fatalf("a complete pull reported %d mismatches\n%s", len(anomalies), events.String())
	}
}

// TestSyncDependentResource_ShortPullDeletesNothingAndSaysSo: the API claims 3
// rows and serves 2. That is exactly issue #12's shape — a page silently
// skipped — and treating the absence as a deletion would delete a live row. The
// mismatch anomaly fires, the sweep is skipped, and the skip is on the record
// so "clean mirror" and "untrustworthy pull" do not look alike.
func TestSyncDependentResource_ShortPullDeletesNothingAndSaysSo(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "testco")

	_, first := newRowServer(t, contactRows(3), 100, 3)
	if res := syncDependentResource(context.Background(), first, db, contactsDep(), "", false, 0, false, nil, &bytes.Buffer{}); res.Err != nil {
		t.Fatalf("first sync: %v", res.Err)
	}

	// Claims 3, serves 2.
	_, short := newRowServer(t, contactRows(2), 100, 3)
	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), short, db, contactsDep(), "", false, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("second sync: %v", res.Err)
	}

	if got := contactIDsInMirror(t, db, "testco"); len(got) != 3 {
		t.Fatalf("mirror holds %v, want all 3 rows: a short pull must not delete", got)
	}
	if evs := deletedEvents(t, events.String()); len(evs) != 0 {
		t.Fatalf("a short pull emitted %d sync_deleted events\n%s", len(evs), events.String())
	}
	if anomalies := countAnomalies(t, events.String(), "result_count_mismatch"); len(anomalies) != 1 {
		t.Fatalf("got %d result_count_mismatch anomalies, want 1\n%s", len(anomalies), events.String())
	}
	skips := warningsWithReason(events.String(), "deletes_skipped_count_mismatch")
	if len(skips) != 1 {
		t.Fatalf("got %d deletes_skipped_count_mismatch warnings, want 1\n%s", len(skips), events.String())
	}
	if skips[0]["company"] != "testco" || skips[0]["result_count"] != float64(3) || skips[0]["rows"] != float64(2) {
		t.Fatalf("skip warning = %v, want company testco, result_count 3, rows 2", skips[0])
	}
}

// TestSyncDependentResource_WindowedPullDeletesNothing: `--since` asks for the
// rows changed since a date, so every unchanged row is absent by construction.
// One of three rows comes back, with a matching filtered Result-Count, and the
// other two must survive.
func TestSyncDependentResource_WindowedPullDeletesNothing(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "testco")

	_, first := newRowServer(t, contactRows(3), 100, 3)
	if res := syncDependentResource(context.Background(), first, db, contactsDep(), "", false, 0, false, nil, &bytes.Buffer{}); res.Err != nil {
		t.Fatalf("first sync: %v", res.Err)
	}

	// The window matched one row, and the header describes the filtered set, so
	// the count check passes: only AllowsDeletion stands between this pull and
	// two deleted live rows.
	_, windowed := newRowServer(t, contactRows(1), 100, 1)
	since := time.Date(2026, 9, 11, 14, 3, 0, 0, time.UTC).Format(time.RFC3339)
	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), windowed, db, contactsDep(), since, false, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("windowed sync: %v", res.Err)
	}

	if got := contactIDsInMirror(t, db, "testco"); len(got) != 3 {
		t.Fatalf("mirror holds %v, want all 3 rows: a windowed pull may never delete", got)
	}
	if evs := deletedEvents(t, events.String()); len(evs) != 0 {
		t.Fatalf("a windowed pull emitted %d sync_deleted events\n%s", len(evs), events.String())
	}
	if skips := warningsWithReason(events.String(), "deletes_skipped_count_mismatch"); len(skips) != 0 {
		t.Fatalf("a windowed pull is not a mismatch; it is out of scope for the sweep\n%s", events.String())
	}
}

// TestSyncDependentResource_CappedRunDeletesNothing: --max-pages stops the walk
// wherever it happens to be, so the rows it did not reach are unexamined, not
// gone. The counts here agree (2 served, 2 claimed) precisely so that only the
// truncation flag can hold the sweep back.
func TestSyncDependentResource_CappedRunDeletesNothing(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "testco")

	_, first := newRowServer(t, contactRows(3), 100, 3)
	if res := syncDependentResource(context.Background(), first, db, contactsDep(), "", false, 0, false, nil, &bytes.Buffer{}); res.Err != nil {
		t.Fatalf("first sync: %v", res.Err)
	}

	_, capped := newRowServer(t, contactRows(2), 100, 2)
	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), capped, db, contactsDep(), "", false, 1, false, nil, &events); res.Err != nil {
		t.Fatalf("capped sync: %v", res.Err)
	}

	if got := contactIDsInMirror(t, db, "testco"); len(got) != 3 {
		t.Fatalf("mirror holds %v, want all 3 rows: a capped run may never delete", got)
	}
	if evs := deletedEvents(t, events.String()); len(evs) != 0 {
		t.Fatalf("a capped run emitted %d sync_deleted events\n%s", len(evs), events.String())
	}
}

// TestSyncDependentResource_DeniedCompanyKeepsItsRows: a Fiken book whose API
// module is not activated answers 403 on every dependent endpoint. It serves no
// rows and no headers, and reading that as "the collection is empty" would wipe
// the company out of the mirror. The company that did answer is swept normally
// in the same run.
func TestSyncDependentResource_DeniedCompanyKeepsItsRows(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "denied-co", "good-co")

	_, first := newRowServer(t, contactRows(3), 100, 3)
	if res := syncDependentResource(context.Background(), first, db, contactsDep(), "", false, 0, false, nil, &bytes.Buffer{}); res.Err != nil {
		t.Fatalf("first sync: %v", res.Err)
	}
	if got := contactIDsInMirror(t, db, "denied-co"); len(got) != 3 {
		t.Fatalf("denied-co holds %v after the first sync, want 3 rows", got)
	}

	srv := httptest.NewServer(&deniedParentServer{deniedSlug: "denied-co", rows: contactRows(2)})
	t.Cleanup(srv.Close)
	c := client.New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)
	c.NoCache = true

	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), c, db, contactsDep(), "", false, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("second sync: %v", res.Err)
	}

	if got := contactIDsInMirror(t, db, "denied-co"); len(got) != 3 {
		t.Fatalf("denied-co holds %v, want all 3 rows: a 403 is not an empty collection", got)
	}
	if got := contactIDsInMirror(t, db, "good-co"); fmt.Sprint(got) != fmt.Sprint([]int64{1000, 1001}) {
		t.Fatalf("good-co holds %v, want [1000 1001]", got)
	}
	evs := deletedEvents(t, events.String())
	if len(evs) != 1 {
		t.Fatalf("got %d sync_deleted events, want 1 (good-co only)\n%s", len(evs), events.String())
	}
	if evs[0]["company"] != "good-co" || evs[0]["count"] != float64(1) {
		t.Fatalf("sync_deleted = %v, want company good-co, count 1", evs[0])
	}
}

// TestSyncDependentResource_BareLegacyRowsSurviveTheSweep: a pre-canonicalisation
// row carries the company in its blob but a BARE primary key, because two
// companies' ids collided on it. The sweep cannot attribute such a row to this
// company's pull, so it leaves it alone even though the pull did not serve it.
// (`sync --full --company` is the path that clears those, on request.)
func TestSyncDependentResource_BareLegacyRowsSurviveTheSweep(t *testing.T) {
	db := openTestStore(t)
	seedCompanies(t, db, "testco")

	if _, err := db.DB().Exec(
		`INSERT INTO resources (id, resource_type, data, synced_at, updated_at)
		 VALUES (?, 'contacts', ?, datetime('now'), datetime('now'))`,
		"7777", `{"contactId":7777,"name":"Legacy","parent_id":"testco"}`,
	); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	_, c := newRowServer(t, contactRows(2), 100, 2)
	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), c, db, contactsDep(), "", false, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("sync: %v", res.Err)
	}

	got := contactIDsInMirror(t, db, "testco")
	if fmt.Sprint(got) != fmt.Sprint([]int64{1000, 1001, 7777}) {
		t.Fatalf("mirror holds %v, want [1000 1001 7777]: a bare legacy row is not this sweep's to delete", got)
	}
	if evs := deletedEvents(t, events.String()); len(evs) != 0 {
		t.Fatalf("the sweep reported %d deletions on a mirror it must not touch\n%s", len(evs), events.String())
	}
}

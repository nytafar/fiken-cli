// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The dependent walker's empty-page guard (issue #17 follow-up). The deletion
// pass trusts two things: the walk was not truncated, and the API's
// Fiken-Api-Result-Count matches the rows the walk landed. A zero-item body the
// walker could not read satisfied both by accident — no rows landed, the header
// said 0, and nothing had marked the walk truncated — so a 200 carrying `{}`,
// `null` or an unrecognised envelope beside Result-Count: 0 read as "this
// collection is empty" and took every mirror row of that (company, resource).
// The flat walker has always tested isEmptyPageResponse at this point; this
// pins that the dependent one does too, and that a REAL empty array still
// deletes, because "the collection was emptied" is the case the feature exists
// for.
//
// Reuses the contacts fixtures and seedCompanies from sync_since_test.go and
// the deletedEvents helper from sync_deletes_test.go.

package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"fiken-cli/internal/client"
	"fiken-cli/internal/config"
	"fiken-cli/internal/store"
)

// fixedBodyServer answers every request with one body and one result count,
// which is how a list endpoint that has gone wrong actually behaves.
func fixedBodyServer(t *testing.T, body string, resultCount int) *client.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(client.HeaderResultCount, strconv.Itoa(resultCount))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := client.New(&config.Config{BaseURL: srv.URL, AccessToken: "test-token"}, 10*time.Second, 0)
	c.NoCache = true
	return c
}

// seedThreeContacts lands three contacts for testco through a complete pull,
// so the deletion pass has something to take.
func seedThreeContacts(t *testing.T) *store.Store {
	t.Helper()
	db := openTestStore(t)
	seedCompanies(t, db, "testco")
	_, c := newRowServer(t, contactRows(3), 100, 3)
	var events bytes.Buffer
	if res := syncDependentResource(context.Background(), c, db, contactsDep(), "", false, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("seed sync: %v", res.Err)
	}
	if got := contactIDsInMirror(t, db, "testco"); len(got) != 3 {
		t.Fatalf("mirror holds %v after seeding, want 3 rows", got)
	}
	return db
}

// TestSyncDependentResource_UnreadableEmptyBodyDeletesNothing: a body the
// walker cannot read is not evidence the collection is empty, no matter what
// the result-count header says.
func TestSyncDependentResource_UnreadableEmptyBodyDeletesNothing(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"payload":{"stuff":1}}`} {
		t.Run(body, func(t *testing.T) {
			db := seedThreeContacts(t)

			var events bytes.Buffer
			c := fixedBodyServer(t, body, 0)
			if res := syncDependentResource(context.Background(), c, db, contactsDep(), "", false, 0, false, nil, &events); res.Err != nil {
				t.Fatalf("sync: %v", res.Err)
			}

			if got := contactIDsInMirror(t, db, "testco"); len(got) != 3 {
				t.Fatalf("mirror holds %v after a %s body with Result-Count 0, want all 3 rows", got, body)
			}
			if evs := deletedEvents(t, events.String()); len(evs) != 0 {
				t.Fatalf("a %s body emitted %d sync_deleted events, want 0\n%s", body, len(evs), events.String())
			}
		})
	}
}

// TestSyncDependentResource_RealEmptyArrayStillDeletes is the other half: an
// actual empty collection, reported as such by the header, must still empty the
// mirror — otherwise the guard above would have disabled the feature.
func TestSyncDependentResource_RealEmptyArrayStillDeletes(t *testing.T) {
	db := seedThreeContacts(t)

	var events bytes.Buffer
	c := fixedBodyServer(t, `[]`, 0)
	if res := syncDependentResource(context.Background(), c, db, contactsDep(), "", false, 0, false, nil, &events); res.Err != nil {
		t.Fatalf("sync: %v", res.Err)
	}

	if got := contactIDsInMirror(t, db, "testco"); len(got) != 0 {
		t.Fatalf("mirror holds %v after the API served an empty collection, want none", got)
	}
	evs := deletedEvents(t, events.String())
	if len(evs) != 1 {
		t.Fatalf("got %d sync_deleted events, want 1\n%s", len(evs), events.String())
	}
	if evs[0]["resource"] != "contacts" || evs[0]["company"] != "testco" || evs[0]["count"] != float64(3) {
		t.Fatalf("sync_deleted = %v, want resource contacts, company testco, count 3", evs[0])
	}
}

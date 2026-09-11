// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Deletion detection (issue #17). No sync path ever removed a mirror row, and
// Fiken offers no negative-delta channel: sales, purchases and transactions are
// soft-deleted (the payload carries deleted: true, which a re-pull picks up),
// but products, projects, inbox documents, contact persons and the rest are
// hard deletes with no flag and no listing of what went. A record deleted in
// Fiken therefore stayed in the mirror forever and every detector kept
// reporting it.
//
// The only evidence available is an absence, so the rule is a set difference:
// mirror rows for one (company, resource) minus the storage keys this walk
// landed. An absence is only evidence when the walk saw the WHOLE collection,
// which is why syncPruneDeletedRows refuses to act unless all of these hold:
//
//   - the pull was not windowed (syncPullWindow.AllowsDeletion) — a --since or
//     --resource-param pull asked for a subset, so every unchanged row is
//     absent by construction;
//   - the walk was not truncated: no --max-pages cap, no resumed cursor, no
//     stuck cursor, no non-JSON body, no upsert or transport error;
//   - the API said how big the collection is (Fiken-Api-Result-Count present);
//   - and the distinct rows landed EQUAL that number. This is issue #15's
//     completeness check, and it is what makes the sweep safe against issue
//     #12's failure class: a walk that silently skipped a page would otherwise
//     read the whole page as deletions.
//
// When only the last condition fails, the run says so (deletes_skipped_count_-
// mismatch) beside the result_count_mismatch anomaly the count check already
// emits, because "the mirror is stale and nobody cleaned it" and "the mirror is
// clean" must not look alike on the event stream.
//
// Scope: the dependent walker only, i.e. rows that hang off a company. The flat
// walker is deliberately left out. Its one resource is `companies` itself,
// whose rows carry a bare storage key with no company scope in it — exactly the
// legacy shape DeleteCompanyResourcesNotIn refuses — and dropping a company row
// on the strength of one pull would orphan every dependent row in the mirror
// for a far larger blast radius than this issue asks for. A company that
// disappears from Fiken is rare, visible, and better handled by an explicit
// repair.
//
// No tombstone and no deleted_at column (YAGNI, per the plan): the row goes.
// The soft `deleted` flags that Fiken itself puts in sale, purchase and
// transaction payloads are untouched and keep meaning what they meant.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"fiken-cli/internal/client"
	"fiken-cli/internal/store"
)

// syncPruneDeletedRows deletes the mirror rows of one (company, resource) that
// a completed, comparable, unwindowed pull did not serve, and reports what it
// did. company is the parent company slug; landed holds the distinct storage
// keys of that one pull (landedIDs is reset per parent).
//
// Never fails the sync. A deletion sweep is a bookkeeping step on top of a pull
// that already succeeded, so a store error is reported and the run continues
// with a stale-but-complete mirror, which is the safe direction.
func syncPruneDeletedRows(db *store.Store, syncEvents io.Writer, resource, company string, landed *landedIDs, info client.PageInfo, truncated bool, window syncPullWindow) {
	if db == nil || company == "" {
		return
	}
	// A windowed pull saw a subset on purpose; a truncated one saw a prefix by
	// accident. Neither can tell an absent row from a deleted one.
	if !window.AllowsDeletion() || truncated {
		return
	}
	// No Fiken-Api-Result-Count means nothing says the walk was complete. That
	// includes the parent that answered 403 before serving a page: it landed no
	// rows, and without this guard an empty landed set would read as "every row
	// of this company is gone".
	if !info.HasResultCount {
		return
	}
	if landed.Len() != info.ResultCount {
		emitSyncDeletesSkipped(syncEvents, resource, company, info.ResultCount, landed.Len())
		return
	}

	removed, err := db.DeleteCompanyResourcesNotIn(resource, company, landed.Keys())
	if err != nil {
		emitSyncDeleteFailed(syncEvents, resource, company, err)
		return
	}
	if removed > 0 {
		emitSyncDeleted(syncEvents, resource, company, removed)
	}
}

// emitSyncDeleted reports rows the API no longer serves and the mirror no
// longer holds. Only ever called with count > 0: a run that deleted nothing has
// nothing to say, and an event stream full of "count":0 would train a reader to
// ignore the one that matters.
func emitSyncDeleted(syncEvents io.Writer, resource, company string, count int) {
	if humanFriendly {
		fmt.Fprintf(os.Stderr, "\n  %s (%s): deleted %d row(s) the API no longer serves.\n", resource, company, count)
		return
	}
	if syncEvents == nil {
		return
	}
	fmt.Fprintf(syncEvents, `{"event":"sync_deleted","resource":"%s","company":"%s","count":%d}`+"\n", resource, company, count)
}

// emitSyncDeletesSkipped marks the pull whose row count disagreed with
// Fiken-Api-Result-Count as one that was NOT swept. The disagreement itself is
// already reported as a result_count_mismatch anomaly; this says what the
// consequence was, so "no deletions happened because none were due" and "no
// deletions happened because the pull could not be trusted" stay distinguishable.
func emitSyncDeletesSkipped(syncEvents io.Writer, resource, company string, resultCount, rows int) {
	if humanFriendly {
		fmt.Fprintf(os.Stderr, "  %s (%s): skipping deletion detection; the pull is not comparable (API %d rows, landed %d).\n", resource, company, resultCount, rows)
		return
	}
	if syncEvents == nil {
		return
	}
	fmt.Fprintf(syncEvents, `{"event":"sync_warning","resource":"%s","company":"%s","reason":"deletes_skipped_count_mismatch","result_count":%d,"rows":%d,"message":"the pull did not match Fiken-Api-Result-Count, so rows missing from it were not deleted"}`+"\n", resource, company, resultCount, rows)
}

// emitSyncDeleteFailed reports a sweep that could not run. Silence here would
// be indistinguishable from a clean mirror.
func emitSyncDeleteFailed(syncEvents io.Writer, resource, company string, err error) {
	if humanFriendly {
		fmt.Fprintf(os.Stderr, "\nwarning: %s (%s): deletion detection failed: %v\n", resource, company, err)
		return
	}
	if syncEvents == nil {
		return
	}
	// Marshalled rather than formatted: an error string is the one field here
	// that can carry a quote or a newline from the store or the driver.
	payload, merr := json.Marshal(struct {
		Event    string `json:"event"`
		Resource string `json:"resource"`
		Company  string `json:"company"`
		Reason   string `json:"reason"`
		Message  string `json:"message"`
	}{"sync_warning", resource, company, "delete_failed", err.Error()})
	if merr != nil {
		return
	}
	fmt.Fprintln(syncEvents, string(payload))
}

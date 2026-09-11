// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The CLI-side half of the result-count headers (issue #15). internal/client
// captures the four Fiken-Api-* headers; everything the generated files need is
// a one-line call into this file, so the generated diff stays at the size of a
// call site.
//
//   - attachPageInfo puts result_count / page_count into a paginated read's
//     provenance, so a consumer of the envelope can tell whether the page it
//     got is the whole collection.
//   - emitResultCountMismatch is the one anomaly issue #15 exists for: sync
//     landed a different number of rows than the API said the collection holds.
//     Issue #12 (page 1 of every resource silently skipped, 477 of 577
//     accounts) produced exactly this shape and nothing was watching.
//   - syncResultCountComparable decides whether a run's summed result count may
//     be recorded beside sync_state.total_count. total_count is the mirror-wide
//     row count (see syncStateTotalCount), so only a run that walked every
//     company, to the end, with headers present produces a number on the same
//     scale; a --company or --max-pages run must leave the recorded value alone
//     rather than overwrite it with a partial sum.
package cli

import (
	"fmt"
	"io"
	"os"

	"fiken-cli/internal/client"
	"fiken-cli/internal/store"
)

// attachPageInfo copies the captured pagination headers onto a live
// provenance. A cache hit or a header-less endpoint leaves both fields nil and
// the meta keys are omitted entirely — an absent header must never be
// published as a zero count.
func attachPageInfo(prov DataProvenance, sink *client.PageInfoSink) DataProvenance {
	info := sink.PageInfo()
	if !info.Present {
		return prov
	}
	if info.HasResultCount {
		resultCount := info.ResultCount
		prov.ResultCount = &resultCount
	}
	if info.PageCount > 0 {
		pageCount := info.PageCount
		prov.PageCount = &pageCount
	}
	return prov
}

// emitResultCountMismatch reports a completed walk that landed a different
// number of rows than Fiken-Api-Result-Count claimed. company is the parent
// company slug for a dependent resource and empty for a flat one.
func emitResultCountMismatch(syncEvents io.Writer, resource, company string, resultCount, rows int) {
	if humanFriendly {
		scope := resource
		if company != "" {
			scope = fmt.Sprintf("%s (%s)", resource, company)
		}
		fmt.Fprintf(os.Stderr, "\nwarning: %s: the API reports %d rows but the sync landed %d; the mirror is incomplete for this resource.\n", scope, resultCount, rows)
		return
	}
	if syncEvents == nil {
		return
	}
	if company != "" {
		fmt.Fprintf(syncEvents, `{"event":"sync_anomaly","resource":"%s","company":"%s","reason":"result_count_mismatch","result_count":%d,"rows":%d}`+"\n", resource, company, resultCount, rows)
		return
	}
	fmt.Fprintf(syncEvents, `{"event":"sync_anomaly","resource":"%s","reason":"result_count_mismatch","result_count":%d,"rows":%d}`+"\n", resource, resultCount, rows)
}

// syncResultCountComparable reports whether this run's summed result count is
// on the same scale as sync_state.total_count and may therefore be persisted.
// seen is false when no response carried Fiken-Api-Result-Count; truncated is
// true when the page walk stopped early (--max-pages, --latest-only, a stuck
// cursor, a non-JSON body).
func syncResultCountComparable(seen, truncated bool) bool {
	return seen && !truncated && syncCompanyScope == ""
}

// recordSyncResultCount persists the API's own row count for a resource when
// the two numbers are comparable, and does nothing otherwise. Never fails a
// sync: a bookkeeping column cannot be worth an aborted run.
func recordSyncResultCount(db *store.Store, resource string, resultCount int, comparable bool) {
	if db == nil || !comparable {
		return
	}
	_ = db.SaveSyncResultCount(resource, resultCount)
}

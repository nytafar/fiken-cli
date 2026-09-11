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
//   - shouldRecordResultCount decides whether a run's summed result count may
//     be recorded beside sync_state.total_count. total_count is the mirror-wide
//     row count (see syncStateTotalCount), so only a run that walked every
//     company, to the end, with headers present produces a number on the same
//     scale; a --company, --max-pages or incremental (--since / watermark) run
//     must leave the recorded value alone rather than overwrite it with a
//     partial sum.
//   - syncSinceParamFor is the one place that answers "did this pull carry a
//     temporal filter", and syncPullIsWindowed the one place that answers "did
//     this pull ask for a subset of the collection", which is what makes a
//     recorded result count unsafe.
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
	if info.HasPageCount {
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

// shouldRecordResultCount reports whether this run's summed result count is on
// the same scale as sync_state.total_count and may therefore be persisted.
//
//   - seen is false when no response carried Fiken-Api-Result-Count.
//   - truncated is true when the page walk stopped early (--max-pages,
//     --latest-only, a resumed cursor, a stuck cursor, a non-JSON body).
//   - windowed is true when the request carried a temporal filter (--since or
//     the stored watermark). Fiken answers a filtered request with the
//     Result-Count of the FILTERED set, so an incremental run that legitimately
//     pulls 3 changed rows out of 577 would otherwise overwrite 577 with 3 and
//     make doctor report rows 577 / result_count 3 forever. The per-pull
//     mismatch check still runs for a windowed pull — the header and the rows
//     describe the same filtered set — only the persisted value is skipped.
//
// A run that may not record leaves the previously recorded value alone; NULL
// still means "no run ever saw the header".
func shouldRecordResultCount(seen, truncated, windowed bool) bool {
	return seen && !truncated && !windowed && syncCompanyScope == ""
}

// syncSinceParamResolver is the seam for the temporal-filter mapping issue #13
// will fill: Fiken's list endpoints accept lastModifiedGe but the spec does not
// declare it as a parameter, so the generated syncResourceSinceParam switch is
// empty and every incremental run currently degrades to a full pull. Nil in
// production until #13 lands; tests install one to drive the windowed path.
var syncSinceParamResolver func(resource string) string

// syncSinceParamFor returns the query parameter that carries the incremental
// window for a resource, or "" when the endpoint has no temporal filter (in
// which case the walkers drop the window and warn rather than send an unknown
// parameter).
func syncSinceParamFor(resource string) string {
	if syncSinceParamResolver != nil {
		if param := syncSinceParamResolver(resource); param != "" {
			return param
		}
	}
	return syncResourceSinceParam(resource)
}

// syncPullIsWindowed reports whether this request asked the API for a subset of
// the collection, so Fiken-Api-Result-Count describes that subset rather than
// the whole thing and must not be recorded beside the mirror-wide total_count.
//
// Two things narrow a pull. The temporal window (--since or the stored
// watermark) arrives as a non-empty effectiveSince. The other is the user's own
// query parameters: `sync --resource-param accounts:lastModifiedGe=2026-09-01`
// is every bit as much a windowed pull, and the walkers computed windowedness
// before userParams.applyTo injected them, so the filtered Result-Count was
// recorded as the collection total.
//
// Any applicable user parameter counts, not just the ones that look like
// filters: the walker cannot tell a filter from a formatting flag, and the
// conservative answer (leave the previously recorded number alone) is the one
// that cannot publish a wrong total. isDependent mirrors applyTo: the dependent
// path is already scoped by its parent path segment and skips --param.
func syncPullIsWindowed(effectiveSince string, userParams *syncUserParams, resource string, isDependent bool) bool {
	if effectiveSince != "" {
		return true
	}
	if userParams == nil {
		return false
	}
	if !isDependent && len(userParams.flatGlobal) > 0 {
		return true
	}
	return len(userParams.trueGlobal) > 0 || len(userParams.perResource[resource]) > 0
}

// everyParentAccountedFor is the "seen" input of shouldRecordResultCount for a
// dependent walk: the summed result count is only on the collection's scale
// when every parent said what its own collection holds.
//
// A parent either returned a page carrying Fiken-Api-Result-Count, or it denied
// access before serving one AND holds no rows in the mirror. A denied parent
// lands zero rows this run and adds zero to the total, so demanding a header
// from it is demanding one that cannot exist: a single Fiken book without the
// API module activated (403 on every dependent endpoint) kept the whole
// mirror's dependent result counts at NULL, which is the state the first full
// resync landed in. But the recorded number is compared against
// sync_state.total_count, which is db.CountResources — mirror-wide, including
// the rows an EARLIER sync landed for the now-denied company. A company whose
// API module lapses would otherwise quietly rewrite 800 to 500 while its 300
// rows sit in the mirror, and doctor would report rows 800 / result_count 500
// forever. So the zero a denied parent contributes is only honest while the
// mirror also holds zero for it (deniedParentIsAccountedFor); otherwise this
// run may not record at all. A parent that DID serve a page without the header
// is different again — the total is then short by an unknown amount — and also
// blocks recording.
//
// At least one parent must have served the header. Without that, an all-denied
// resource would record a literal 0, which doctor and SyncResultCount report as
// "the API says this collection is empty" rather than "nobody answered".
func everyParentAccountedFor(parentsWithResultCount, parentsDeniedAndEmpty, parents int) bool {
	if parentsWithResultCount == 0 {
		return false
	}
	return parentsWithResultCount+parentsDeniedAndEmpty == parents
}

// deniedParentIsAccountedFor reports whether a parent that denied access before
// serving a page may be counted as "walked, landed zero" for the recorded
// result count: only when the mirror holds no rows of this resource for it, so
// the zero it contributes to the sum matches what total_count counts for it.
// A store error answers false — the conservative side is to keep the previously
// recorded value rather than overwrite it from a run we cannot vouch for.
func deniedParentIsAccountedFor(db *store.Store, resource, parent string) bool {
	if db == nil || parent == "" {
		return false
	}
	rows, err := db.CountCompanyResources(resource, parent)
	if err != nil {
		return false
	}
	return rows == 0
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

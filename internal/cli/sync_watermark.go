// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The default run made incremental (issue #22). Issue #13 taught the walkers to
// send lastModifiedGe, but only from a caller-supplied --since: a plain
// `fiken-cli sync` re-pulled every page of every resource for every company,
// roughly 1500 sequential requests behind Fiken's one-request lock. The missing
// half was a watermark the dependent walker could actually use, and sync_state
// could not be it — keyed by resource_type alone, a `--company A` run would
// move a number that company B's next pull reads, and B's changes in that gap
// would never be fetched again. The store now keys it on both (see
// internal/store/sync_watermark.go); this file is the policy over it:
//
//   - WHEN IT APPLIES. Only the default path — no --full, no --since — and only
//     for a resource whose list endpoint declares a temporal filter
//     (syncSinceParamFor). A resource without one keeps pulling in full and
//     says nothing: that is the default path doing its job, not a user request
//     the CLI had to decline, so it gets no resource_not_incremental warning.
//     --since stays a caller window: it neither reads nor writes the watermark.
//     --full clears the pair's watermark before refetching, so an interrupted
//     --full cannot leave a stale mark standing over a half-cleared mirror.
//
//   - WHAT GOES ON THE WIRE. The stored timestamp through syncSinceWindowValue,
//     the same day-floor-minus-one rule --since gets (see sync_since.go for why
//     the extra day is not optional).
//
//   - WHAT IS WRITTEN, AND WHEN. The START of the run, not its end: rows keep
//     changing while a walk runs, and a watermark stamped at the end would
//     silently exclude everything modified during it. And only after a pull
//     this run can vouch for — every page of that one company's collection
//     landed, no sync_error, no truncation, and no result_count_mismatch when
//     the API gave a number to compare against. A partial pull leaves the old
//     watermark (or none) in place and costs one more full pull; advancing past
//     rows nobody fetched would lose them permanently.
package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"fiken-cli/internal/store"
)

// syncRunStartedAt is the wall-clock start of the sync command currently
// running, set once in newSyncCmd's RunE. The watermark a completed pull writes
// is this instant, not the moment the pull finished — see the package comment.
// Zero outside a `sync` invocation (direct walker calls in tests), where the
// walk's own start time is the honest fallback.
//
// A package-level value rather than a walker parameter, matching
// syncCompanyScope: the generated walker signatures are call sites in three
// patch records, and widening them to thread one timestamp through would buy
// nothing a single run-scoped value does not already give.
var syncRunStartedAt time.Time

// syncWatermarkStamp is the value a completed pull records, formatted for the
// store. fallback is used when the run start was never set.
func syncWatermarkStamp(fallback time.Time) string {
	started := syncRunStartedAt
	if started.IsZero() {
		started = fallback
	}
	return started.UTC().Format(time.RFC3339)
}

// syncWatermarkWindow returns the wire value for the stored watermark of one
// (company, resource), or "" when this pull must fetch the whole collection.
//
// "" covers every reason not to window: an explicit --since (the caller's
// window wins and the watermark is not consulted), --full, an endpoint with no
// temporal filter, and — the first-run case — a pair that has never completed a
// pull. A store that cannot answer also reads as "" : pulling in full is the
// direction that cannot lose a row.
func syncWatermarkWindow(db *store.Store, company, resource, sinceTS string, full bool) string {
	if db == nil || full || sinceTS != "" || company == "" {
		return ""
	}
	if syncSinceParamFor(resource) == "" {
		return ""
	}
	mark := db.SyncWatermark(company, resource)
	if mark == "" {
		return ""
	}
	return syncSinceWindowValue(mark, syncSinceParamFormatFor(resource))
}

// syncWatermarkMayAdvance reports whether a COMPLETED pull of this (company,
// resource) is allowed to move the watermark. The completeness of the pull is
// the caller's half of the decision; this is the half that depends only on how
// the run was invoked.
//
// --since is a caller window and never writes: the caller asked for a slice,
// and stamping the run start over it would claim everything older had been
// pulled too. The user's own --param / --resource-param filters narrow a pull
// the same way (syncPullIsWindowed is the one place that knows how), so they
// block the write as well. A resource with no temporal filter never writes
// either — nothing would ever read the row.
//
// --full does NOT block it: a full pull is the most complete pull there is, and
// the run clears the old watermark before refetching so the new one describes
// only what this run actually fetched.
func syncWatermarkMayAdvance(resource, sinceTS string, userParams *syncUserParams) bool {
	if sinceTS != "" {
		return false
	}
	if syncSinceParamFor(resource) == "" {
		return false
	}
	return !syncPullIsWindowed("", userParams, resource, true)
}

// syncAdvanceWatermark records stamp for one (company, resource). Never fails
// the sync: a watermark that did not get written costs one extra full pull,
// which is exactly the state the mirror was in before this landed.
func syncAdvanceWatermark(db *store.Store, company, resource, stamp string) {
	if db == nil || company == "" || resource == "" || stamp == "" {
		return
	}
	_ = db.SetSyncWatermark(company, resource, stamp)
}

// syncClearWatermark drops the watermark for one (company, resource) ahead of a
// --full refetch of that pair, so an interrupted run leaves "pull me in full"
// behind rather than a mark for rows it never fetched.
func syncClearWatermark(db *store.Store, company, resource string) {
	if db == nil || company == "" || resource == "" {
		return
	}
	_ = db.ClearSyncWatermark(company, resource)
}

// emitSyncWindow announces that one (company, resource) pull is incremental and
// from when, so an unattended caller reading the event stream can tell a cheap
// run from a full one without diffing request counts. One event per windowed
// pair, emitted before the first request of that pair.
//
// Human mode gets a line too: without it, "contacts: 3 synced (done)" beside a
// company with 900 contacts reads as data loss.
func emitSyncWindow(syncEvents io.Writer, resource, company, since string) {
	if humanFriendly {
		fmt.Fprintf(os.Stderr, "\n  %s (%s): incremental, only rows modified since %s\n", resource, company, since)
		return
	}
	if syncEvents == nil {
		return
	}
	fmt.Fprintf(syncEvents, `{"event":"sync_window","company":"%s","resource":"%s","since":"%s"}`+"\n", company, resource, since)
}

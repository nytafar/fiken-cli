// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The CLI-side half of the incremental window (issue #13). The mapping itself
// — which resource carries which temporal filter, in which format — lives in
// the generated syncResourceSinceParam / syncResourceSinceParamFormat switches,
// because that pair is the printed data table for it; this file holds the two
// things the generator cannot know:
//
//   - syncSinceWindowValue turns a timestamp into the value that actually goes
//     on the wire. Fiken's lastModifiedGe is INCLUSIVE and lastModifiedDate is
//     day-granular with no time and no zone, so a watermark of
//     2026-09-11T14:03:00+02:00 is sent as 2026-09-10. The floor is forced (a
//     time-of-day suffix is not a valid value for a format: date parameter);
//     the extra day is the margin the day granularity costs, because the
//     client's local day boundary is not necessarily the one the API stamped
//     the row with, and client and server clocks need not agree. (The third
//     reason it was written for — a watermark stamped when the run ENDED while
//     rows kept changing during it — is gone: issue #22 stamps the run's START.
//     The other two are enough on their own.) The
//     result re-pulls at most two days of rows on every incremental run:
//     missing a row is the failure that matters, re-upserting one is idempotent
//     and free.
//
//     Read "incremental run" as either of the two things that produce a
//     window. The caller's --since is one. The other, since issue #22, is the
//     DEFAULT run: the dependent walker reads a watermark per (company,
//     resource) from the sync_watermark table and windows each parent's pull
//     from its own, so a plain `fiken-cli sync` asks Fiken only for what
//     changed. Both arrive here and get the same treatment — this function is
//     the single formatting site for the wire value. The policy around it (who
//     may read a watermark, who may write one, and what --full and --since do
//     to it) lives in sync_watermark.go; sync_state.last_synced_at is NOT that
//     watermark and never was — it is keyed by resource_type alone, so a
//     --company A run would move a number company B reads, and its only reader
//     remains syncResource, which walks `companies`, the sole flat resource,
//     which declares no date filter.
//
//   - syncPullWindow is the walk's one answer to "was this pull a subset of the
//     collection", kept as a value for the whole walk instead of a bare local
//     flag, because two later decisions have to honour it: the recorded
//     sync_state.result_count (issue #15, via shouldRecordResultCount) and the
//     deletion pass of issue #17, which may never conclude "the mirror holds a
//     row the API no longer serves" from a pull that only asked for a window.
package cli

import (
	"strings"
	"time"
)

// syncSinceDayFormat is the layout lastModifiedGe expects (spec.yaml
// components.parameters.lastModifiedGe: type string, format date, "Dates are
// represented as strings formatted as YYYY-MM-DD").
const syncSinceDayFormat = "2006-01-02"

// syncSinceParamFormatResolver mirrors syncSinceParamResolver for the value
// format. Nil in production — the generated switch answers — and installed by
// tests that drive a resource the generated mapping does not cover.
var syncSinceParamFormatResolver func(resource string) string

// syncSinceParamFormatFor returns the format the resource's temporal filter
// expects ("date" for every Fiken filter today), or "" when it declares none.
func syncSinceParamFormatFor(resource string) string {
	if syncSinceParamFormatResolver != nil {
		if format := syncSinceParamFormatResolver(resource); format != "" {
			return format
		}
	}
	return syncResourceSinceParamFormat(resource)
}

// syncSinceWindowValue formats a since value for the wire and widens it by the
// granularity the API filter actually has.
//
// For a day-granular filter (format: date) the value is floored to its day by
// formatSyncSinceValue and then stepped back one day; see the package comment
// for why the floor alone is not enough. A format this function does not
// understand is passed through unchanged, so a future filter with real
// timestamps keeps its precision instead of being silently widened.
func syncSinceWindowValue(value, paramFormat string) string {
	formatted := formatSyncSinceValue(value, paramFormat)
	if !strings.EqualFold(paramFormat, "date") {
		return formatted
	}
	day, err := time.Parse(syncSinceDayFormat, formatted)
	if err != nil {
		// formatSyncSinceValue could not read the value (it returns its input
		// unchanged then). Sending it as-is is the existing behaviour and the
		// API rejects it loudly; silently widening an unparsed string would be
		// worse.
		return formatted
	}
	return day.AddDate(0, 0, -1).Format(syncSinceDayFormat)
}

// syncPullWindow describes how much of a collection one walk asked for.
//
// Param/Value are what the request carries (both "" for a full pull), and
// Windowed is the decision every consumer must read rather than re-derive:
// syncPullIsWindowed counts the user's own --param / --resource-param filters
// too, so a pull can be a window without carrying a since value at all.
type syncPullWindow struct {
	// Param is the query parameter carrying the temporal window, "" when the
	// endpoint declares none or the pull is not incremental.
	Param string
	// Value is the formatted window bound sent under Param, "" when absent.
	Value string
	// Windowed reports that this pull asked the API for a subset of the
	// collection, so nothing it counted describes the whole thing.
	Windowed bool
}

// newSyncPullWindow builds the window for one walk. param/value are the
// resolved temporal filter (already emptied by the caller when the endpoint
// declares none), and resource/isDependent/userParams are what
// syncPullIsWindowed needs to see the user's own filters.
func newSyncPullWindow(param, value string, userParams *syncUserParams, resource string, isDependent bool) syncPullWindow {
	return syncPullWindow{
		Param:    param,
		Value:    value,
		Windowed: syncPullIsWindowed(value, userParams, resource, isDependent),
	}
}

// AllowsDeletion reports whether a completed walk of this pull saw enough of
// the collection for "the mirror holds it and the walk did not return it" to
// mean the row is gone. A windowed pull never has: it asked for the rows that
// changed since a date, so every unchanged row is absent by construction.
// Issue #17's deletion pass is required to consult this.
func (w syncPullWindow) AllowsDeletion() bool { return !w.Windowed }

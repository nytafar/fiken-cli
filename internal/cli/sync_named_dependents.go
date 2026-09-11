// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The CLI-side half of issue #14. `sync --resources journal_entries` sets both
// `resources` (the flat worker pool's work list) and `parentFilter` (the
// dependent pass's filter), so every dependent named on the command line was
// run twice: once through the flat pool, where syncResourcePath knows only the
// one flat resource (`companies`) and fails, and once through the dependent
// pass, where it succeeds. The flat failure returned before any event was
// emitted, so machine mode saw an unnamed error and the summary read
// "resources: 4, errored: 2" for two resources that both synced.
//
// isDependentSyncResource is the guard the flat pool needs: a name the
// dependent pass owns is not the flat pool's work.
package cli

// isDependentSyncResource reports whether dependentResourceDefs() owns this
// resource name, i.e. whether the dependent (parent-keyed) pass will sync it.
// A name that is neither flat nor dependent is nobody's: it falls through to
// the flat pool, which reports it by name.
func isDependentSyncResource(resource string) bool {
	for _, dep := range dependentResourceDefs() {
		if dep.Name == resource {
			return true
		}
	}
	return false
}

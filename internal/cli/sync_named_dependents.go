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
//
// dependentParentEmptyResult is the follow-up the skip exposed. With the flat
// pool no longer claiming the name, `sync --resources journal_entries --agent`
// against a mirror with no companies row printed `resources: 1, success: 1,
// errored: 0` and exited 0: the dependent pass found an empty parent table,
// printed a line only under --human-friendly and returned a bare success. A
// machine-mode caller could not tell "synced nothing because nothing changed"
// from "synced nothing because the mirror has no companies yet".
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// dependentParentEmptyResult reports a dependent resource that could not run
// because its parent table holds no rows, and returns the warned result that
// makes the run summary say so (warned: 1, not success: 1). The exit policy is
// unchanged: a warned resource is not an error.
func dependentParentEmptyResult(syncEvents io.Writer, resource, parentTable string, started time.Time) syncResult {
	hint := fmt.Sprintf("run fiken-cli sync --resources %s first", parentTable)
	if humanFriendly {
		fmt.Fprintf(os.Stderr, "  %s: skipping (parent table %s is empty, sync it first — %s)\n", resource, parentTable, hint)
	} else if syncEvents != nil {
		payload := struct {
			Event       string `json:"event"`
			Resource    string `json:"resource"`
			Reason      string `json:"reason"`
			ParentTable string `json:"parent_table"`
			Hint        string `json:"hint"`
		}{
			Event:       "sync_warning",
			Resource:    resource,
			Reason:      "parent_table_empty",
			ParentTable: parentTable,
			Hint:        hint,
		}
		out, _ := json.Marshal(payload)
		fmt.Fprintf(syncEvents, "%s\n", out)
	}
	return syncResult{
		Resource: resource,
		Warn:     fmt.Errorf("skipped %s: parent table %s is empty", resource, parentTable),
		Duration: time.Since(started),
	}
}

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

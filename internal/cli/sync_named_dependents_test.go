// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Pins issue #14: naming a dependent in --resources used to run it through the
// flat worker pool as well, where it failed without emitting an event, so a
// machine-mode consumer read "resources: 4, errored: 2" for two resources that
// both synced. Drives the real sync command against a fake server; the mirror
// and the config live in a temp HOME, so no real book is ever contacted.

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiken-cli/internal/store"
)

// syncTestEnv is a sync command wired to a fake Fiken that answers every list
// path with a bare JSON array of `rows` items, plus a seeded mirror holding one
// company.
type syncTestEnv struct {
	flags  *rootFlags
	dbPath string
	out    *bytes.Buffer
}

func newSyncTestEnv(t *testing.T, rows int) *syncTestEnv {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := make([]json.RawMessage, 0, rows)
		if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "0" {
			for i := 0; i < rows; i++ {
				items = append(items, json.RawMessage(fmt.Sprintf(`{"journalEntryId":%d,"description":"entry %d"}`, 9000+i, i)))
			}
		}
		body, _ := json.Marshal(items)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	configPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("base_url = %q\napi_token = \"test-token\"\n", srv.URL)), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("FIKEN_BASE_URL", srv.URL)
	t.Setenv("FIKEN_API_TOKEN", "test-token")

	dbPath := filepath.Join(home, "data.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, _, err := db.UpsertBatch("companies", []json.RawMessage{
		json.RawMessage(`{"slug":"testco","name":"Test Company","testCompany":true}`),
	}); err != nil {
		t.Fatalf("seed companies: %v", err)
	}
	_ = db.Close()

	return &syncTestEnv{
		flags:  &rootFlags{timeout: 10 * time.Second, dataSource: "auto"},
		dbPath: dbPath,
		out:    &bytes.Buffer{},
	}
}

// run executes `sync` with the given extra arguments and returns the command's
// error. Events land in env.out.
func (e *syncTestEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	cmd := newSyncCmd(e.flags)
	cmd.SetOut(e.out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(append([]string{"--db", e.dbPath}, args...))
	return cmd.Execute()
}

// events parses the NDJSON event stream the command wrote to stdout.
func (e *syncTestEnv) events(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(e.out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("non-JSON line on the event stream: %q", line)
		}
		out = append(out, ev)
	}
	return out
}

func eventsOfType(events []map[string]any, kind string) []map[string]any {
	var out []map[string]any
	for _, ev := range events {
		if ev["event"] == kind {
			out = append(out, ev)
		}
	}
	return out
}

// TestSync_NamedDependentRunsOnlyTheDependentPass is issue #14: the summary of
// `sync --resources journal_entries` must read one resource, one success, no
// errors — not four resources with two anonymous failures.
func TestSync_NamedDependentRunsOnlyTheDependentPass(t *testing.T) {
	env := newSyncTestEnv(t, 3)

	if err := env.run(t, "--resources", "journal_entries"); err != nil {
		t.Fatalf("sync --resources journal_entries: %v\n%s", err, env.out.String())
	}

	events := env.events(t)
	summaries := eventsOfType(events, "sync_summary")
	if len(summaries) != 1 {
		t.Fatalf("sync_summary events = %d, want 1\n%s", len(summaries), env.out.String())
	}
	summary := summaries[0]
	for key, want := range map[string]int{"resources": 1, "success": 1, "errored": 0} {
		got, ok := summary[key].(float64)
		if !ok || int(got) != want {
			t.Fatalf("sync_summary %s = %v, want %d\n%s", key, summary[key], want, env.out.String())
		}
	}
	if errs := eventsOfType(events, "sync_error"); len(errs) != 0 {
		t.Fatalf("sync_error events = %v, want none\n%s", errs, env.out.String())
	}
	// The flat pool must not have claimed the dependent at all.
	for _, ev := range eventsOfType(events, "sync_start") {
		if ev["resource"] == "journal_entries" {
			t.Fatalf("journal_entries was started by the flat pool: %v", ev)
		}
	}
	if got, _ := summary["total_records"].(float64); int(got) != 3 {
		t.Fatalf("sync_summary total_records = %v, want 3", summary["total_records"])
	}
}

// TestSync_NamedParentStillCascadesToDependents keeps the fix from silencing
// the flat resource itself: naming the parent runs the parent and its children.
func TestSync_NamedParentStillCascadesToDependents(t *testing.T) {
	env := newSyncTestEnv(t, 3)

	if err := env.run(t, "--resources", "companies"); err != nil {
		t.Fatalf("sync --resources companies: %v\n%s", err, env.out.String())
	}

	events := env.events(t)
	started := eventsOfType(events, "sync_start")
	if len(started) != 1 || started[0]["resource"] != "companies" {
		t.Fatalf("sync_start events = %v, want exactly companies\n%s", started, env.out.String())
	}
	summary := eventsOfType(events, "sync_summary")[0]
	if got, _ := summary["errored"].(float64); int(got) != 0 {
		t.Fatalf("sync_summary errored = %v, want 0\n%s", summary["errored"], env.out.String())
	}
	// companies plus the ten dependents that hang off it.
	if got, _ := summary["resources"].(float64); int(got) != 1+len(dependentResourceDefs()) {
		t.Fatalf("sync_summary resources = %v, want %d\n%s", summary["resources"], 1+len(dependentResourceDefs()), env.out.String())
	}
}

// TestSync_UnknownResourceEmitsNamedSyncError: a name that is neither flat nor
// dependent is still a failure, but a named one.
func TestSync_UnknownResourceEmitsNamedSyncError(t *testing.T) {
	env := newSyncTestEnv(t, 3)

	err := env.run(t, "--resources", "not_a_resource")
	if err == nil {
		t.Fatalf("sync --resources not_a_resource returned nil, want a failure\n%s", env.out.String())
	}

	events := env.events(t)
	errs := eventsOfType(events, "sync_error")
	if len(errs) != 1 {
		t.Fatalf("sync_error events = %d, want exactly 1\n%s", len(errs), env.out.String())
	}
	if errs[0]["resource"] != "not_a_resource" {
		t.Fatalf("sync_error resource = %v, want not_a_resource", errs[0]["resource"])
	}
	summary := eventsOfType(events, "sync_summary")[0]
	if got, _ := summary["errored"].(float64); int(got) != 1 {
		t.Fatalf("sync_summary errored = %v, want 1\n%s", summary["errored"], env.out.String())
	}
	if got, _ := summary["resources"].(float64); int(got) != 1 {
		t.Fatalf("sync_summary resources = %v, want 1\n%s", summary["resources"], env.out.String())
	}
}

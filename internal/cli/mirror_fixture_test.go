// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — a fixture mirror for the detector tests: seed a
// throwaway SQLite mirror with rows for one company and run a command of the
// real tree against it. Every amount and name in these fixtures is invented.
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"fiken-cli/internal/store"
)

// fixtureCompany is the slug every fixture row belongs to.
const fixtureCompany = "agensia"

// seedMirror writes the rows into a new mirror and returns its path. rows maps
// a resource type to a JSON array; parent_id is stamped on each row the way
// the dependent sync does, so loadCompanyResources sees them.
func seedMirror(t *testing.T, rows map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("open fixture mirror: %v", err)
	}
	defer db.Close()
	if err := db.Upsert("companies", fixtureCompany,
		json.RawMessage(`{"slug":"`+fixtureCompany+`","name":"Agensia Test AS","testCompany":true,"vatType":"bi-monthly"}`)); err != nil {
		t.Fatalf("seed company: %v", err)
	}
	for resource, raw := range rows {
		var items []map[string]any
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			t.Fatalf("fixture %s does not parse: %v", resource, err)
		}
		for i, item := range items {
			if _, has := item["parent_id"]; !has {
				item["parent_id"] = fixtureCompany
			}
			data, _ := json.Marshal(item)
			if err := db.Upsert(resource, fmt.Sprintf("%s-%d", resource, i), data); err != nil {
				t.Fatalf("seed %s[%d]: %v", resource, i, err)
			}
		}
	}
	return path
}

// runOnMirror runs a mirror-reading command with --json against the fixture
// and decodes its payload into out. The provenance envelope the detectors
// wrap their report in is unwrapped, so out receives the report itself.
func runOnMirror(t *testing.T, dbPath string, out any, args ...string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("FIKEN_CONFIG", filepath.Join(home, "config.toml"))

	var stdout, stderr bytes.Buffer
	root := RootCmd()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append(args, "--db", dbPath, "--company", fixtureCompany, "--json"))
	if err := root.Execute(); err != nil {
		t.Fatalf("%v: %v\nstderr: %s", args, err, stderr.String())
	}
	raw := stdout.Bytes()
	var envelope struct {
		Results json.RawMessage `json:"results"`
	}
	if json.Unmarshal(raw, &envelope) == nil && len(envelope.Results) > 0 {
		raw = envelope.Results
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%v: output does not decode: %v\n%s", args, err, stdout.String())
	}
}

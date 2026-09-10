// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Wiring for the test-company write guard (internal/client/write_guard.go,
// PLAN 2.4.1). This file owns the three things internal/client deliberately
// cannot own: the write mode (read from an untracked env file, never a flag),
// the slug -> testCompany resolver (reads the SQLite mirror), and the audit
// sink that appends a refusal to the append-only agent_events table.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fiken-cli/internal/client"
	"fiken-cli/internal/fikencore"
)

// writeGuardExitCode is the process exit code for a write-guard denial.
// Distinct from usage(2)/not-found(3)/auth(4)/api(5)/partial(6)/rate-limit(7)
// and config(10) so a script can tell "refused locally, nothing was sent" from
// "the API rejected it".
const writeGuardExitCode = 8

// configureWriteGuard installs mode, resolver and audit sink for the CLI.
// Idempotent and cheap: the resolver loads the mirror lazily, at most once.
func configureWriteGuard(flags *rootFlags) {
	agent := flags != nil && flags.agent
	client.ConfigureWriteGuard(resolveWriteGuardMode(agent), writeGuardResolver)
	client.SetWriteGuardOnDeny(auditWriteGuardDenial)
}

// ConfigureWriteGuardForMCP is the same wiring for the MCP server, which has
// no rootFlags. MCP is an agent surface, so it never reaches live mode.
func ConfigureWriteGuardForMCP() {
	client.ConfigureWriteGuard(client.ModeTest, writeGuardResolver)
	client.SetWriteGuardOnDeny(auditWriteGuardDenial)
}

// ---------------------------------------------------------------- mode source

// writeGuardEnvFiles lists the untracked files consulted for FIKEN_MODE, in
// precedence order. All are gitignored (.env / *.env cover the first two).
func writeGuardEnvFiles() []string {
	paths := []string{".env.local", ".env"}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		paths = append(paths, filepath.Join(dir, "fiken-cli", "env"))
	} else if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "fiken-cli", "env"))
	}
	return paths
}

// resolveWriteGuardMode decides the process write mode.
//
//	--agent                    -> test, unconditionally
//	PRINTING_PRESS_VERIFY set  -> test, unconditionally
//	FIKEN_MODE in the process env -> that value
//	first env file that defines FIKEN_MODE -> that value
//	otherwise                  -> test
func resolveWriteGuardMode(agent bool) client.Mode {
	if agent {
		return client.ModeTest
	}
	if os.Getenv("PRINTING_PRESS_VERIFY") != "" {
		return client.ModeTest
	}
	if v, ok := os.LookupEnv("FIKEN_MODE"); ok {
		return modeFromValue(v)
	}
	for _, path := range writeGuardEnvFiles() {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		mode, found := parseModeFromEnvFile(f)
		_ = f.Close()
		if found {
			return mode
		}
	}
	return client.ModeTest
}

// modeFromValue maps an env value to a mode. Only "live" (any case) opts in;
// anything else, including a typo, stays in test mode.
func modeFromValue(v string) client.Mode {
	if strings.EqualFold(strings.TrimSpace(v), "live") {
		return client.ModeLive
	}
	return client.ModeTest
}

// parseModeFromEnvFile reads a dotenv-shaped stream and returns the mode named
// by the first FIKEN_MODE assignment, plus whether one was found. Tolerates a
// leading "export ", surrounding whitespace, single or double quotes, and
// "#" comment lines.
func parseModeFromEnvFile(r io.Reader) (client.Mode, bool) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "FIKEN_MODE" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		return modeFromValue(value), true
	}
	return client.ModeTest, false
}

// ------------------------------------------------------------------ resolver

var (
	writeGuardCacheMu sync.Mutex
	writeGuardCache   map[string]bool
)

// writeGuardResolver answers slug -> (testCompany, known) from the mirror.
// Fails closed: any error yields (false, false), i.e. company_unknown. The
// mirror is opened at most once per process; the whole companies set is a
// handful of rows, so one read fills the cache for every slug.
func writeGuardResolver(slug string) (bool, bool) {
	writeGuardCacheMu.Lock()
	defer writeGuardCacheMu.Unlock()
	if writeGuardCache == nil {
		writeGuardCache = loadCompanyTestFlags()
	}
	isTest, known := writeGuardCache[slug]
	return isTest, known
}

// loadCompanyTestFlags reads the companies rows out of the mirror. Companies
// live in the generic `resources` table (there is no typed companies table),
// so ListFieldSets' json_extract route is the one that sees them — the same
// route resolveCompanySlug uses. Never returns nil, so a failed load is
// cached as "no companies known" rather than retried on every request.
func loadCompanyTestFlags() map[string]bool {
	out := map[string]bool{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := openMirror(ctx, "")
	if err != nil {
		return out
	}
	defer db.Close()

	rows, err := db.ListFieldSets("companies", []string{"slug", "testCompany"})
	if err != nil {
		return out
	}
	for _, row := range rows {
		slug := strings.TrimSpace(row["slug"])
		if slug == "" {
			continue
		}
		flag := strings.TrimSpace(row["testCompany"])
		out[slug] = flag == "true" || flag == "1"
	}
	return out
}

// --------------------------------------------------------------- denial sink

// auditWriteGuardDenial appends the refusal to the append-only agent_events
// table so a blocked write is visible later. Entirely best-effort: the denial
// stands whether or not the audit write succeeds.
func auditWriteGuardDenial(e *client.WriteGuardError) {
	if e == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	core, err := fikencore.Open(ctx, "")
	if err != nil {
		return
	}
	defer core.Close()

	inputs, _ := json.Marshal(map[string]string{
		"code": e.Code, "method": e.Method, "path": e.Path, "company": e.Slug,
	})
	_, _ = core.LogEvent(ctx, fikencore.Event{
		Surface:     "cli",
		Operation:   "guard.denied",
		CompanySlug: e.Slug,
		Result:      "failure",
		Rationale:   e.Error(),
		InputsJSON:  string(inputs),
	})
}

// ------------------------------------------------------------ error surfacing

// writeGuardCLIError converts a *client.WriteGuardError anywhere in err's
// chain into a cliError carrying writeGuardExitCode, emitting the machine
// envelope first under --json (which --agent implies). Returns nil when err is
// not a guard denial, so the caller keeps its own error untouched.
func writeGuardCLIError(flags *rootFlags, err error) error {
	if err == nil {
		return nil
	}
	var guardErr *client.WriteGuardError
	if !As(err, &guardErr) {
		return nil
	}
	if flags != nil && flags.asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"error":   guardErr.Error(),
			"code":    guardErr.Code,
			"company": guardErr.Slug,
		})
	}
	return &cliError{code: writeGuardExitCode, err: err}
}

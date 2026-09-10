// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Pins the mode source for the test-company write guard: the dotenv parser and
// the precedence rules in resolveWriteGuardMode. Live mode must be reachable
// only through an untracked env file, and never when --agent is set.
package cli

import (
	"strings"
	"testing"

	"fiken-cli/internal/client"
)

func TestParseModeFromEnvFile(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantMode  client.Mode
		wantFound bool
	}{
		{"plain", "FIKEN_MODE=live\n", client.ModeLive, true},
		{"export", "export FIKEN_MODE=live\n", client.ModeLive, true},
		{"double quoted", "FIKEN_MODE=\"live\"\n", client.ModeLive, true},
		{"single quoted", "FIKEN_MODE='live'\n", client.ModeLive, true},
		{"whitespace", "  FIKEN_MODE =  live  \n", client.ModeLive, true},
		{"uppercase value", "FIKEN_MODE=LIVE\n", client.ModeLive, true},
		{"commented out", "# FIKEN_MODE=live\n", client.ModeTest, false},
		{"absent", "FOO=bar\nBAZ=qux\n", client.ModeTest, false},
		{"empty file", "", client.ModeTest, false},
		{"other value", "FIKEN_MODE=test\n", client.ModeTest, true},
		{"typo value stays test", "FIKEN_MODE=liv\n", client.ModeTest, true},
		{"first assignment wins", "FIKEN_MODE=test\nFIKEN_MODE=live\n", client.ModeTest, true},
		{"after comments and blanks", "# header\n\nOTHER=1\nexport FIKEN_MODE=\"live\"\n", client.ModeLive, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, found := parseModeFromEnvFile(strings.NewReader(tc.body))
			if found != tc.wantFound {
				t.Fatalf("found = %v, want %v", found, tc.wantFound)
			}
			if mode != tc.wantMode {
				t.Fatalf("mode = %v, want %v", mode, tc.wantMode)
			}
		})
	}
}

// TestResolveWriteGuardMode_AgentForcesTest is the rule that keeps an agent in
// test mode no matter what the environment says.
func TestResolveWriteGuardMode_AgentForcesTest(t *testing.T) {
	t.Setenv("FIKEN_MODE", "live")
	if got := resolveWriteGuardMode(true); got != client.ModeTest {
		t.Fatalf("--agent mode = %v, want test", got)
	}
	if got := resolveWriteGuardMode(false); got != client.ModeLive {
		t.Fatalf("non-agent mode with the env set = %v, want live", got)
	}
}

// TestResolveWriteGuardMode_VerifyForcesTest keeps the Printing Press verifier
// off the live path even if a stray env var is exported.
func TestResolveWriteGuardMode_VerifyForcesTest(t *testing.T) {
	t.Setenv("PRINTING_PRESS_VERIFY", "1")
	t.Setenv("FIKEN_MODE", "live")
	if got := resolveWriteGuardMode(false); got != client.ModeTest {
		t.Fatalf("verify-env mode = %v, want test", got)
	}
}

// TestResolveWriteGuardMode_DefaultIsTest pins the fail-safe default: no
// process env, no env file, no flags.
func TestResolveWriteGuardMode_DefaultIsTest(t *testing.T) {
	t.Setenv("PRINTING_PRESS_VERIFY", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := resolveWriteGuardMode(false); got != client.ModeTest {
		t.Fatalf("default mode = %v, want test", got)
	}
}

// TestWriteGuardEnvFiles pins the file list and its order: two untracked
// dotenv spellings in the working directory, then the user config file.
func TestWriteGuardEnvFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	got := writeGuardEnvFiles()
	want := []string{".env.local", ".env", "/xdg/fiken-cli/env"}
	if len(got) != len(want) {
		t.Fatalf("env files = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("env files = %v, want %v", got, want)
		}
	}
}

// TestWriteGuardCLIError maps a guard denial to its own exit code and leaves
// unrelated errors alone.
func TestWriteGuardCLIError(t *testing.T) {
	guardErr := &client.WriteGuardError{
		Code: client.GuardCodeNotTestCompany, Slug: "testco",
		Method: "POST", Path: "/api/v2/companies/testco/contacts",
	}
	mapped := writeGuardCLIError(&rootFlags{}, guardErr)
	if mapped == nil {
		t.Fatal("writeGuardCLIError returned nil for a guard denial")
	}
	if got := ExitCode(mapped); got != writeGuardExitCode {
		t.Fatalf("ExitCode = %d, want %d", got, writeGuardExitCode)
	}
	// The 80 generated mutating commands funnel their error through
	// classifyAPIError, which wraps it in an apiErr (exit 5). Execute must
	// still see the guard denial underneath and win with its own code.
	wrapped := classifyAPIError(guardErr, &rootFlags{})
	if got := ExitCode(wrapped); got != 5 {
		t.Fatalf("classifyAPIError exit = %d, want 5 before remapping", got)
	}
	remapped := writeGuardCLIError(&rootFlags{}, wrapped)
	if remapped == nil {
		t.Fatal("writeGuardCLIError missed a guard denial wrapped by classifyAPIError")
	}
	if got := ExitCode(remapped); got != writeGuardExitCode {
		t.Fatalf("remapped exit = %d, want %d", got, writeGuardExitCode)
	}
	if writeGuardCLIError(&rootFlags{}, nil) != nil {
		t.Error("writeGuardCLIError(nil) should be nil")
	}
	if writeGuardCLIError(&rootFlags{}, errPlain) != nil {
		t.Error("writeGuardCLIError should ignore unrelated errors")
	}
}

type plainError struct{}

func (plainError) Error() string { return "unrelated" }

var errPlain error = plainError{}

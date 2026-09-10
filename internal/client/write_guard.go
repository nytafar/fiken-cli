// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Test-company write guard (PLAN 2.4.1). No code path, present or future, may
// put a mutating request on the wire against a Fiken company whose synced
// record is not testCompany:true unless live mode was deliberately enabled via
// an untracked env file.
//
// The load-bearing property is the DEFAULT STATE: an unconfigured guard denies
// every mutating request to a Fiken host. A binary (or a future code path) that
// forgets to call ConfigureWriteGuard cannot write. Enforcement sits in two
// places — (*fikenRoundTripper).RoundTrip in fiken_transport.go (durable,
// hand-authored, below every client) and (*Client).doInternal in client.go
// (ergonomic, produces a structured CLI error before a request is built).
//
// internal/client keeps no dependency on internal/store: the slug -> testCompany
// resolver is injected from internal/cli (write_guard_setup.go).
package client

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// Mode is the write mode of the process. The zero value is ModeTest, so a
// forgotten Configure call is the strict mode, never the permissive one.
type Mode int

const (
	// ModeTest permits mutations only against companies the mirror records as
	// testCompany:true. It is the zero value and the default.
	ModeTest Mode = iota
	// ModeLive permits mutations against any company the mirror knows about.
	// Reachable only through an untracked env file naming FIKEN_MODE.
	ModeLive
)

func (m Mode) String() string {
	if m == ModeLive {
		return "live"
	}
	return "test"
}

// Write-guard denial codes. Stable strings: they are the "code" field of the
// JSON error envelope and are asserted on by tests and agents.
const (
	// GuardCodeUnconfigured — a mutating Fiken request was attempted before
	// ConfigureWriteGuard ran. The default, fail-closed state.
	GuardCodeUnconfigured = "guard_unconfigured"
	// GuardCodeNoCompany — a mutating Fiken request whose path carries no
	// company slug and is not on the explicit allowlist.
	GuardCodeNoCompany = "guard_no_company"
	// GuardCodeCompanyUnknown — the slug is absent from the local mirror, so
	// its testCompany flag cannot be established. Denied in both modes.
	GuardCodeCompanyUnknown = "company_unknown"
	// GuardCodeNotTestCompany — the slug is known and is not a test company,
	// and the process is in test mode.
	GuardCodeNotTestCompany = "not_test_company"
)

// guardLiveModeHint tells the operator the one supported way to reach live
// mode. Deliberately not a CLI flag, so an agent cannot pass it per invocation.
const guardLiveModeHint = "to write to real books, set FIKEN_MODE to \"live\" in an untracked env file (./.env.local, ./.env, or ~/.config/fiken-cli/env); there is deliberately no CLI flag"

// WriteGuardError is the typed denial. Callers in internal/cli map it to a
// distinct exit code and a JSON error envelope.
type WriteGuardError struct {
	Code   string
	Slug   string
	Method string
	Path   string
}

func (e *WriteGuardError) Error() string {
	who := "no company slug in the request path"
	if e.Slug != "" {
		who = "company " + e.Slug
	}
	var why string
	switch e.Code {
	case GuardCodeUnconfigured:
		why = "the test-company write guard was never configured, so every write is refused"
	case GuardCodeNoCompany:
		why = "a mutating request to a Fiken host must name a company"
	case GuardCodeCompanyUnknown:
		why = "it is not in the local mirror, so its testCompany flag is unknown — run 'fiken-cli sync' first"
	case GuardCodeNotTestCompany:
		why = "it is not a test company and this process is in test mode"
	default:
		why = "the test-company write guard refused it"
	}
	return fmt.Sprintf("write guard: refused %s %s against %s: %s (%s)",
		e.Method, e.Path, who, why, guardLiveModeHint)
}

// fikenGuardHosts are the hosts the guard polices. Anything else (update
// checks, unrelated HTTP, httptest servers in unit tests) passes straight
// through. Deliberately the same set as fikenLockHosts.
var fikenGuardHosts = map[string]bool{
	"api.fiken.no": true,
	"fiken.no":     true,
}

var (
	writeGuardMu      sync.RWMutex
	writeGuardMode    Mode
	writeGuardResolve func(slug string) (isTest bool, known bool)
	writeGuardOnDeny  func(*WriteGuardError)
)

// ConfigureWriteGuard installs the process write mode and the slug ->
// (testCompany, known) resolver. Called once at startup from the two places
// a *Client is constructed (internal/cli/root.go, internal/mcp/tools.go).
// A nil resolver leaves the guard unconfigured, i.e. denying.
func ConfigureWriteGuard(mode Mode, resolve func(slug string) (bool, bool)) {
	writeGuardMu.Lock()
	defer writeGuardMu.Unlock()
	writeGuardMode = mode
	writeGuardResolve = resolve
}

// SetWriteGuardOnDeny installs a denial sink. internal/cli uses it to append
// the refusal to the fikencore audit table without internal/client having to
// import internal/fikencore. Errors in the sink never affect the denial.
func SetWriteGuardOnDeny(fn func(*WriteGuardError)) {
	writeGuardMu.Lock()
	defer writeGuardMu.Unlock()
	writeGuardOnDeny = fn
}

// ResetWriteGuardForTest restores the default (unconfigured, ModeTest) state.
func ResetWriteGuardForTest() {
	writeGuardMu.Lock()
	defer writeGuardMu.Unlock()
	writeGuardMode = ModeTest
	writeGuardResolve = nil
	writeGuardOnDeny = nil
}

// companySlugFromPath extracts {slug} from any path shaped
// /api/v2/companies/{slug}/... (or /companies/{slug}/...). Returns "" when the
// path names no company, including the /companies collection endpoint itself.
func companySlugFromPath(p string) string {
	segments := strings.Split(p, "/")
	for i, seg := range segments {
		if seg == "companies" && i+1 < len(segments) {
			return segments[i+1]
		}
	}
	return ""
}

// isGuardAllowlistedPath reports whether a mutating request without a company
// slug is nonetheless legitimate. Today the only member is the OAuth token
// endpoint on fiken.no, which mints credentials and writes no books.
// (*Client).refreshAccessToken bypasses doInternal and posts there directly,
// so the transport-level guard is what this allowlist exists for.
func isGuardAllowlistedPath(host, p string) bool {
	return host == "fiken.no" && strings.TrimSuffix(p, "/") == "/oauth/token"
}

// GuardMutation is the decision point. It returns nil to allow, or a
// *WriteGuardError to deny. Non-Fiken hosts and non-mutating verbs always pass.
func GuardMutation(method string, u *url.URL) error {
	if u == nil || !fikenGuardHosts[u.Hostname()] {
		return nil
	}
	if !isMutatingVerb(method) {
		return nil
	}
	if isGuardAllowlistedPath(u.Hostname(), u.Path) {
		return nil
	}

	slug := companySlugFromPath(u.Path)
	if slug == "" {
		return guardDeny(&WriteGuardError{Code: GuardCodeNoCompany, Method: method, Path: u.Path})
	}

	writeGuardMu.RLock()
	mode, resolve := writeGuardMode, writeGuardResolve
	writeGuardMu.RUnlock()

	if resolve == nil {
		return guardDeny(&WriteGuardError{Code: GuardCodeUnconfigured, Slug: slug, Method: method, Path: u.Path})
	}
	isTest, known := resolve(slug)
	if !known {
		return guardDeny(&WriteGuardError{Code: GuardCodeCompanyUnknown, Slug: slug, Method: method, Path: u.Path})
	}
	if !isTest && mode == ModeTest {
		return guardDeny(&WriteGuardError{Code: GuardCodeNotTestCompany, Slug: slug, Method: method, Path: u.Path})
	}
	return nil
}

// guardDeny notifies the denial sink (best effort) and returns the error.
func guardDeny(e *WriteGuardError) error {
	writeGuardMu.RLock()
	sink := writeGuardOnDeny
	writeGuardMu.RUnlock()
	if sink != nil {
		sink(e)
	}
	return e
}

// guardMutationURL is the string-URL convenience the generated doInternal call
// site uses, so that edit to client.go stays at three lines. A URL that will
// not parse cannot be dispatched either, so an unparseable target is allowed
// through to fail in the request builder with its own message.
func guardMutationURL(method, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	return GuardMutation(method, u)
}

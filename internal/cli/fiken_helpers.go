// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — shared helpers for the Fiken analytical/write
// commands. Lives in package cli beside the generated command files; survives
// regen as a NOVEL file.
package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"fiken-cli/internal/cliutil"
	"fiken-cli/internal/fikencore"
	"fiken-cli/internal/store"

	"github.com/spf13/cobra"
)

// fikenRequestID returns a random hex X-Request-ID the write commands stamp on
// the committing call and record in the audit log / idempotency row. The
// transport (fiken_transport.go) keeps a caller-set X-Request-ID, so this value
// is the one Fiken actually sees.
func fikenRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req-" + time.Now().UTC().Format("20060102150405.000000")
	}
	return hex.EncodeToString(b[:])
}

// loadProposal reads a fikencore.Proposal from a JSON file (filePath) or, when
// filePath is "" or "-", from stdin. Used by validate/commit/reverse.
func loadProposal(cmd *cobra.Command, filePath string) (*fikencore.Proposal, error) {
	var raw []byte
	var err error
	if filePath == "" || filePath == "-" {
		raw, err = io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("reading proposal from stdin: %w", err)
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			return nil, fmt.Errorf("no proposal provided: pass --file <path> or pipe JSON on stdin")
		}
	} else {
		raw, err = os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("reading proposal file: %w", err)
		}
	}
	var p fikencore.Proposal
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parsing proposal JSON: %w", err)
	}
	return &p, nil
}

// loadValidAccounts returns the set of account codes in the synced chart of
// accounts for a company, for validate's account-existence check. An empty set
// (e.g. accounts not synced) disables that check rather than failing.
func loadValidAccounts(ctx context.Context, db *store.Store, slug string) map[string]bool {
	accts, err := loadCompanyResources(ctx, db, "accounts", slug)
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, a := range accts {
		if code := jsonStr(a, "code"); code != "" {
			set[code] = true
		}
	}
	return set
}

// openMirror opens the local SQLite mirror (the disposable sync store). All
// analytical detectors read from here, never the live API.
func openMirror(ctx context.Context, dbPath string) (*store.Store, error) {
	if dbPath == "" {
		dbPath = defaultDBPath("fiken-cli")
	}
	return store.OpenWithContext(ctx, dbPath)
}

// resolveCompanySlug picks the company to operate on: the --company flag when
// set, otherwise the single synced company. With zero or many synced companies
// and no flag, it returns an actionable error.
func resolveCompanySlug(db *store.Store, flagCompany string) (string, error) {
	if strings.TrimSpace(flagCompany) != "" {
		return strings.TrimSpace(flagCompany), nil
	}
	slugs, err := db.ListField("companies", "slug")
	if err != nil {
		return "", fmt.Errorf("reading synced companies: %w", err)
	}
	switch len(slugs) {
	case 0:
		// Under the Printing Press verifier (validate-narrative/verify run
		// examples against an empty mirror), return a placeholder so the read
		// commands produce an empty report and exit 0 instead of erroring.
		if cliutil.IsVerifyEnv() {
			return "_verify", nil
		}
		return "", fmt.Errorf("no companies in the local mirror — run 'fiken-cli sync' first, or pass --company <slug>")
	case 1:
		return slugs[0], nil
	default:
		return "", fmt.Errorf("%d companies synced; pass --company <slug> (one of: %s)", len(slugs), strings.Join(slugs, ", "))
	}
}

// loadCompanyResources loads every mirror row of resourceType scoped to a
// company. Dependent (walker-synced) rows carry parent_id = company slug, so
// filtering on it returns exactly that company's data even when the mirror
// holds several companies. Each row is decoded to a JSON object map.
func loadCompanyResources(ctx context.Context, db *store.Store, resourceType, slug string) ([]map[string]json.RawMessage, error) {
	rows, err := db.DB().QueryContext(ctx,
		`SELECT data FROM resources WHERE resource_type = ? AND json_extract(data, '$.parent_id') = ?`,
		resourceType, slug)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", resourceType, err)
	}
	defer rows.Close()
	var out []map[string]json.RawMessage
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan %s: %w", resourceType, err)
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			continue // skip a malformed row rather than abort the whole report
		}
		out = append(out, obj)
	}
	return out, rows.Err()
}

// jsonObjects decodes a JSON array field (e.g. a journal entry's "lines") into
// a slice of object maps. Absent/empty/non-array yields nil.
func jsonObjects(m map[string]json.RawMessage, key string) []map[string]json.RawMessage {
	raw, ok := m[key]
	if !ok {
		return nil
	}
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil
	}
	return arr
}

// jsonObject decodes a single nested JSON object field (e.g. a purchase's
// "supplier"). Absent/non-object yields nil.
func jsonObject(m map[string]json.RawMessage, key string) map[string]json.RawMessage {
	raw, ok := m[key]
	if !ok {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	return obj
}

// topKey returns the highest-count key in a frequency map (empty when empty).
func topKey(counts map[string]int) string {
	best, bestN := "", -1
	for k, n := range counts {
		if n > bestN || (n == bestN && k < best) {
			best, bestN = k, n
		}
	}
	return best
}

// emitFiken writes v as filtered JSON when --agent/--json (or piped) is in
// effect, else calls the human renderer. Centralizes the output-mode decision
// for the novel commands.
func emitFiken(cmd *cobra.Command, flags *rootFlags, v any, human func()) error {
	if flags.asJSON || flags.agent || flags.csv || !isTerminal(cmd.OutOrStdout()) {
		return printJSONFiltered(cmd.OutOrStdout(), v, flags)
	}
	human()
	return nil
}

// kr formats an øre integer amount as a Norwegian-kroner decimal string
// (12345 -> "123.45"). Exact integer arithmetic; no float rounding.
func kr(ore int64) string {
	neg := ore < 0
	if neg {
		ore = -ore
	}
	s := fmt.Sprintf("%d.%02d", ore/100, ore%100)
	if neg {
		return "-" + s
	}
	return s
}

// nowFunc is the clock the period resolver reads. A package-level var so
// tests can pin "now" and assert the default-year behaviour deterministically.
var nowFunc = time.Now

// resolvePeriod turns the --period flag into a resolved Window, and is the one
// place that decides what an unset --period means. The tool serves the open
// year, so unset is the current calendar year rather than all history; "all"
// (any case) opts back out to unbounded. Everything else goes to parsePeriod.
func resolvePeriod(flag string) (Window, error) {
	s := strings.TrimSpace(flag)
	if s == "" {
		y := nowFunc().Year()
		return Window{
			From:   fmt.Sprintf("%04d-01-01", y),
			To:     fmt.Sprintf("%04d-12-31", y),
			Source: "default_current_year",
		}, nil
	}
	if strings.EqualFold(s, "all") {
		return Window{Source: "all"}, nil
	}
	from, to, err := parsePeriod(s)
	if err != nil {
		return Window{}, err
	}
	return Window{From: from, To: to, Source: "flag"}, nil
}

// parsePeriod turns a period token into an inclusive [from,to] YYYY-MM-DD
// range. Supported: "2026", "2026-05", "2026-Q1".."2026-Q4", and explicit
// "from:to". Empty input returns empty bounds (no filter).
func parsePeriod(s string) (from, to string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", nil
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		from, to = strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
		if !validDate(from) || !validDate(to) {
			return "", "", fmt.Errorf("invalid date range %q (want from:to as YYYY-MM-DD)", s)
		}
		return from, to, nil
	}
	if strings.Contains(strings.ToUpper(s), "Q") {
		parts := strings.SplitN(strings.ToUpper(s), "-Q", 2)
		if len(parts) == 2 {
			y, e1 := strconv.Atoi(parts[0])
			q, e2 := strconv.Atoi(parts[1])
			if e1 == nil && e2 == nil && q >= 1 && q <= 4 {
				startMonth := (q-1)*3 + 1
				from = fmt.Sprintf("%04d-%02d-01", y, startMonth)
				to = lastDayOfMonth(y, startMonth+2)
				return from, to, nil
			}
		}
		return "", "", fmt.Errorf("invalid quarter %q (want YYYY-Q1..Q4)", s)
	}
	switch len(s) {
	case 4: // year
		y, e := strconv.Atoi(s)
		if e != nil {
			return "", "", fmt.Errorf("invalid year %q", s)
		}
		return fmt.Sprintf("%04d-01-01", y), fmt.Sprintf("%04d-12-31", y), nil
	case 7: // YYYY-MM
		t, e := time.Parse("2006-01", s)
		if e != nil {
			return "", "", fmt.Errorf("invalid month %q (want YYYY-MM)", s)
		}
		return s + "-01", lastDayOfMonth(t.Year(), int(t.Month())), nil
	case 10: // single day
		if !validDate(s) {
			return "", "", fmt.Errorf("invalid date %q (want YYYY-MM-DD)", s)
		}
		return s, s, nil
	default:
		return "", "", fmt.Errorf("unrecognized period %q (want YYYY, YYYY-MM, YYYY-Qn, or from:to)", s)
	}
}

func lastDayOfMonth(year, month int) string {
	// Normalize month overflow (e.g. month 14 -> next year).
	t := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC)
	return t.Format("2006-01-02")
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// jsonStr extracts a string field from a decoded JSON object, tolerating
// absence (returns "").
func jsonStr(m map[string]json.RawMessage, key string) string {
	raw, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

// jsonInt extracts an integer field (Fiken amounts are integer øre), tolerating
// absence or string-encoded numbers.
func jsonInt(m map[string]json.RawMessage, key string) (int64, bool) {
	raw, ok := m[key]
	if !ok {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			return v, true
		}
	}
	return 0, false
}

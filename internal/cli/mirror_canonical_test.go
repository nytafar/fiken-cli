// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"fiken-cli/internal/store"
)

func TestMirrorCompanySlugFromPath(t *testing.T) {
	cases := []struct{ path, want string }{
		{"/companies/agensia/journalEntries", "agensia"},
		{"/companies/agensia/bankAccounts", "agensia"},
		{"/companies/agensia/purchases/1234/attachments", "agensia"},
		{"companies/agensia/sales", "agensia"},
		// the company record itself has no parent
		{"/companies/agensia", ""},
		{"/companies", ""},
		// an unsubstituted template must never become a slug
		{"/companies/{companySlug}/sales", ""},
		{"", ""},
		{"/user", ""},
	}
	for _, tc := range cases {
		if got := mirrorCompanySlugFromPath(tc.path); got != tc.want {
			t.Errorf("mirrorCompanySlugFromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestMirrorWithParentID(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"bankAccountId": 1}`),
		json.RawMessage(`{"bankAccountId": 2, "parent_id": "already"}`),
		json.RawMessage(`[1,2,3]`), // not an object: passed through
	}
	got := mirrorWithParentID(items, "agensia")
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}

	var first map[string]any
	if err := json.Unmarshal(got[0], &first); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if first["parent_id"] != "agensia" {
		t.Errorf("parent_id = %v, want agensia", first["parent_id"])
	}

	var second map[string]any
	if err := json.Unmarshal(got[1], &second); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if second["parent_id"] != "already" {
		t.Errorf("existing parent_id overwritten: %v", second["parent_id"])
	}

	if string(got[2]) != `[1,2,3]` {
		t.Errorf("non-object item rewritten: %s", got[2])
	}

	if out := mirrorWithParentID(items, ""); len(out) != 3 || string(out[0]) != string(items[0]) {
		t.Errorf("empty slug must pass items through untouched")
	}
}

func TestSyncStateTotalCount_UsesActualRowCount(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	items := []json.RawMessage{
		json.RawMessage(`{"journalEntryId": 1, "parent_id": "agensia"}`),
		json.RawMessage(`{"journalEntryId": 2, "parent_id": "agensia"}`),
	}
	if _, _, err := s.UpsertBatch("journal_entries", items); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
	// A resumed run's counter says 999; the mirror holds 2.
	if got := syncStateTotalCount(s, "journal_entries", 999); got != 2 {
		t.Fatalf("syncStateTotalCount = %d, want 2", got)
	}
	// An unresolvable name must not fail a sync — fall back to the counter.
	if got := syncStateTotalCount(s, "no-such-thing", 7); got != 7 {
		t.Fatalf("fallback = %d, want 7", got)
	}
}

func TestScopeParentRowsToCompany(t *testing.T) {
	rows := []map[string]string{
		{"id": "agensia", "slug": "agensia"},
		{"id": "other-co", "slug": "other-co"},
		{"id": "third-co", "slug": "third-co"},
	}
	t.Cleanup(func() { syncCompanyScope = "" })

	syncCompanyScope = ""
	if got := scopeParentRowsToCompany("companies", rows); len(got) != 3 {
		t.Fatalf("unscoped run = %d rows, want 3", len(got))
	}

	syncCompanyScope = "agensia"
	got := scopeParentRowsToCompany("companies", rows)
	if len(got) != 1 || got[0]["slug"] != "agensia" {
		t.Fatalf("scoped run = %v, want only agensia", got)
	}
	if other := scopeParentRowsToCompany("something_else", rows); len(other) != 3 {
		t.Fatalf("non-company parent table was filtered")
	}
}

// The closed set in internal/store/resource_name.go has to cover every resource
// literal the generated commands hand to the mirror; an uncovered one now fails
// the read or drops the mutation cache instead of silently minting a third
// spelling in `resources`. Scanning the package keeps the set honest across a
// reprint, which is the only time new literals appear.
func TestCanonicalResource_AcceptsEveryGeneratedCallSiteName(t *testing.T) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`resolveRead(?:WithStrategy)?\(cmd\.Context\(\), c, flags, (?:"[a-z]+", )?"([A-Za-z0-9_-]+)"`),
		regexp.MustCompile(`resolvePaginatedRead(?:WithStrategy)?\(cmd\.Context\(\), c, flags, (?:"[a-z]+", )?"([A-Za-z0-9_-]+)"`),
		regexp.MustCompile(`writeMutationResponseToStore\(cmd\.Context\(\), "([A-Za-z0-9_-]+)"`),
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	found := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, re := range patterns {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				found[m[1]] = true
			}
		}
	}
	if len(found) < 10 {
		t.Fatalf("scanned only %d resource literals; the scan is broken", len(found))
	}

	names := make([]string, 0, len(found))
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		canonical, err := store.CanonicalResource(n)
		if err != nil {
			t.Errorf("generated call site passes %q, which CanonicalResource rejects: %v", n, err)
			continue
		}
		if !store.IsKnownResource(canonical) {
			t.Errorf("generated call site passes %q (canonical %q), which is not in the closed set", n, canonical)
		}
	}
}

// A cached mutation response has to be keyed and filtered exactly like a synced
// row: parent_id stamped from the request path, storage key id\0slug. Before
// PATCH(mutation-cache-parent-id) the mutation cache wrote a BARE id, so the row
// was invisible to loadCompanyResources (which filters on $.parent_id) and two
// companies' purchases with the same purchaseId overwrote each other.
func TestWriteMutationResponseToStore_StampsParentIDFromRequestPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()

	writeMutationResponseToStore(ctx,
		"purchases",
		json.RawMessage(`{"purchaseId": 4242, "kind": "supplier", "date": "2026-01-15"}`),
		"",
		"/companies/agensia/purchases")
	// same API id, a different book: must not collide on the primary key
	writeMutationResponseToStore(ctx,
		"purchases",
		json.RawMessage(`{"purchaseId": 4242, "kind": "supplier", "date": "2026-02-20"}`),
		"",
		"/companies/other-co/purchases")

	db, err := store.OpenWithContext(ctx, defaultDBPath("fiken-cli"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	ids := resourceStorageIDs(t, db, "purchases")
	want := []string{"4242" + string([]byte{0}) + "agensia", "4242" + string([]byte{0}) + "other-co"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("storage keys = %q, want %q", ids, want)
	}

	rows, err := loadCompanyResources(ctx, db, "purchases", "agensia")
	if err != nil {
		t.Fatalf("loadCompanyResources: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("loadCompanyResources(agensia) = %d rows, want 1", len(rows))
	}
	if got := string(rows[0]["date"]); got != `"2026-01-15"` {
		t.Errorf("agensia row date = %s, want the agensia mutation", got)
	}
}

// A mutation whose path carries no company slug (the company record itself, an
// account-level endpoint) keeps the bare id: those resources are not
// parent-keyed, so a bare key is the correct one and stamping a parent would
// invent an owner.
func TestWriteMutationResponseToStore_NoSlugKeepsBareKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()

	writeMutationResponseToStore(ctx,
		"companies",
		json.RawMessage(`{"slug": "agensia", "name": "Agensia AS"}`),
		"",
		"/companies")

	db, err := store.OpenWithContext(ctx, defaultDBPath("fiken-cli"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	ids := resourceStorageIDs(t, db, "companies")
	if !reflect.DeepEqual(ids, []string{"agensia"}) {
		t.Errorf("storage keys = %q, want [agensia] (bare, no parent)", ids)
	}
}

func resourceStorageIDs(t *testing.T, db *store.Store, resourceType string) []string {
	t.Helper()
	rows, err := db.DB().Query(`SELECT id FROM resources WHERE resource_type = ? ORDER BY id`, resourceType)
	if err != nil {
		t.Fatalf("query %s: %v", resourceType, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// Regen guard: every generated mutation command must hand the request path to
// writeMutationResponseToStore. A reprint that drops the argument would not
// compile, but one that reintroduces the four-argument call with a literal ""
// would compile and silently go back to bare keys.
func TestGeneratedMutationCallSitesPassRequestPath(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	call := regexp.MustCompile(`writeMutationResponseToStore\(cmd\.Context\(\), "[A-Za-z0-9_-]+", data, "[A-Za-z0-9_-]*", ([A-Za-z]+)\)`)
	any := regexp.MustCompile(`writeMutationResponseToStore\(cmd\.Context\(\)`)
	sites := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		body := string(b)
		total := len(any.FindAllString(body, -1))
		threaded := call.FindAllStringSubmatch(body, -1)
		if total != len(threaded) {
			t.Errorf("%s: %d mutation cache call(s), %d pass the request path", name, total, len(threaded))
		}
		for _, m := range threaded {
			if m[1] != "path" {
				t.Errorf("%s: mutation cache call passes %q, want the request path variable", name, m[1])
			}
		}
		sites += len(threaded)
	}
	if sites < 50 {
		t.Fatalf("scanned only %d mutation call sites; the scan is broken", sites)
	}
}

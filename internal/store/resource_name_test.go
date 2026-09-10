// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
package store

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestCanonicalResource_KebabToSnake(t *testing.T) {
	cases := []struct{ in, want string }{
		// the five names the printed CLI spells two ways (codebase-map C-5)
		{"bank-accounts", "bank_accounts"},
		{"journal-entries", "journal_entries"},
		{"contact-person", "contact_person"},
		{"write-off", "write_off"},
		{"create-invoice-draft", "create_invoice_draft"},
		// identity for names already canonical
		{"bank_accounts", "bank_accounts"},
		{"journal_entries", "journal_entries"},
		{"contact_person", "contact_person"},
		{"write_off", "write_off"},
		{"create_invoice_draft", "create_invoice_draft"},
		// names that agree on both sides stay put
		{"accounts", "accounts"},
		{"companies", "companies"},
		{"contacts", "contacts"},
		{"inbox", "inbox"},
		{"products", "products"},
		{"projects", "projects"},
		{"purchases", "purchases"},
		{"sales", "sales"},
		{"settled", "settled"},
		{"transactions", "transactions"},
		{"transactions_delete", "transactions_delete"},
		// generic resources with no typed table
		{"attachments", "attachments"},
		{"credit-notes", "credit_notes"},
		{"order-confirmations", "order_confirmations"},
		{"time-entries", "time_entries"},
		{"account-balances", "account_balances"},
		{"general-journal-entries", "general_journal_entries"},
		// leading/trailing whitespace is not a distinct resource
		{"  sales  ", "sales"},
	}
	for _, tc := range cases {
		got, err := CanonicalResource(tc.in)
		if err != nil {
			t.Errorf("CanonicalResource(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("CanonicalResource(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCanonicalResource_RejectsUnknownAndMalformed(t *testing.T) {
	bad := []string{
		"",
		"   ",
		// an unknown HYPHENATED name is the bug class of issue #6: a kebab
		// spelling that resolves to nothing must never be guessed at.
		"no-such-thing",
		"journal-entry",
		"bank-account",
		"sales-lines",
		// malformed
		"sales lines",
		"sales.lines",
		"sales/lines",
		`sales"; DROP TABLE resources; --`,
		"_leading",
		"-leading",
	}
	for _, in := range bad {
		if got, err := CanonicalResource(in); err == nil {
			t.Errorf("CanonicalResource(%q) = %q, want an error", in, got)
		}
	}
}

// An unknown separator-free name is accepted as an opaque generic resource:
// `resources` is a generic mirror and the printed store's own regression suite
// writes synthetic types through these entry points. Such a name must never
// claim a typed table.
func TestCanonicalResource_UnknownSnakeNameIsGeneric(t *testing.T) {
	for _, in := range []string{"rt_0", "numeric_ids", "present_type", "post_panic", "overrideTest"} {
		got, err := CanonicalResource(in)
		if err != nil {
			t.Fatalf("CanonicalResource(%q): %v", in, err)
		}
		if got != strings.TrimSpace(in) {
			t.Errorf("CanonicalResource(%q) = %q, want it unchanged", in, got)
		}
		if HasTypedTable(got) {
			t.Errorf("HasTypedTable(%q) = true, want false", got)
		}
		if IsKnownResource(got) {
			t.Errorf("IsKnownResource(%q) = true, want false", got)
		}
	}
}

func TestCanonicalResource_IsIdempotent(t *testing.T) {
	for _, name := range append(TypedTableResources(), "credit-notes", "attachments", "write-off") {
		once, err := CanonicalResource(name)
		if err != nil {
			t.Fatalf("CanonicalResource(%q): %v", name, err)
		}
		twice, err := CanonicalResource(once)
		if err != nil {
			t.Fatalf("CanonicalResource(%q): %v", once, err)
		}
		if once != twice {
			t.Errorf("CanonicalResource not idempotent for %q: %q then %q", name, once, twice)
		}
	}
}

// The closed set is derived from the DDL. If a table is added or renamed in the
// generated migrations without updating resource_name.go, the loud default arm
// in UpsertBatch would start rejecting a legitimate resource (or, worse, let one
// through generically). Parse the DDL and compare.
func TestTypedTableResources_MatchTheDDL(t *testing.T) {
	ddl := regexp.MustCompile(`CREATE TABLE IF NOT EXISTS "([a-z_]+)"`)
	fromDDL := map[string]bool{}
	for _, m := range ddl.FindAllStringSubmatch(migrationsSourceForTest(t), -1) {
		fromDDL[m[1]] = true
	}
	if len(fromDDL) == 0 {
		t.Fatal("found no quoted CREATE TABLE statements; the DDL scan is broken")
	}
	for name := range fromDDL {
		if !typedTableResources[name] {
			t.Errorf("DDL declares table %q but typedTableResources does not list it", name)
		}
	}
	for name := range typedTableResources {
		if !fromDDL[name] {
			t.Errorf("typedTableResources lists %q but the DDL declares no such table", name)
		}
	}
}

// Every canonical name that owns a typed table must also be resolvable through
// the parent-key map or explicitly not parent-keyed; a hyphen key there is what
// produced the bare storage keys of issue #6.
func TestResourceParentKeyColumns_AreCanonical(t *testing.T) {
	for name := range resourceParentKeyColumns {
		got, err := CanonicalResource(name)
		if err != nil {
			t.Errorf("resourceParentKeyColumns key %q is not a resolvable resource: %v", name, err)
			continue
		}
		if got != name {
			t.Errorf("resourceParentKeyColumns key %q is not canonical (want %q)", name, got)
		}
	}
}

func TestResourceIDFieldOverrides_AreCanonical(t *testing.T) {
	for name := range resourceIDFieldOverrides {
		got, err := CanonicalResource(name)
		if err != nil {
			t.Errorf("resourceIDFieldOverrides key %q is not a resolvable resource: %v", name, err)
			continue
		}
		if got != name {
			t.Errorf("resourceIDFieldOverrides key %q is not canonical (want %q)", name, got)
		}
	}
}

// migrationsSourceForTest returns the generated store source so the DDL parity
// test can read the CREATE TABLE statements. The migrations slice is a local
// inside Migrate, so the source text is the only handle on it.
func migrationsSourceForTest(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatalf("read store.go: %v", err)
	}
	return string(b)
}

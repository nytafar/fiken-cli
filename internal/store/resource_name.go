// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// One canonical spelling for a mirror resource name (PLAN 2.2 item 1, issue #6).
//
// The printed CLI spells the same resource two ways: the sync registry, the DDL
// and the id-override map use snake_case (`journal_entries`), while the generated
// read/write commands, the typed-table dispatch switch and the parent-key map use
// kebab-case (`journal-entries`). Nothing normalised between them, so
//
//   - the typed tables for bank_accounts and journal_entries were never written by
//     sync (the dispatch arm was spelled with a hyphen), and
//   - resourceParentKeyColumns missed those two, so their rows landed in `resources`
//     under a BARE id instead of `id\0<companySlug>` — two companies' journal entries
//     with the same journalEntryId collided on the primary key and one overwrote the
//     other. That is data loss in the only table the detectors read.
//
// Every storage boundary now funnels the name through CanonicalResource first.
package store

import (
	"fmt"
	"regexp"
	"strings"
)

// resourceNameRE is the shape a resource name may have at all. Anything else
// (spaces, dots, slashes, quotes) is a caller bug, not a naming variant.
var resourceNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// typedTableResources is the closed set of canonical names that own a
// domain-specific table in the DDL (store.go migrations, the `CREATE TABLE
// IF NOT EXISTS "<name>"` statements). Keep in step with that slice: a name
// here MUST have a dispatch arm in UpsertBatch, and the loud default arm
// turns a miss into an error instead of a silent generic-only write.
var typedTableResources = map[string]bool{
	"accounts":                    true,
	"bank_accounts":               true,
	"contacts":                    true,
	"contacts_attachments":        true,
	"contact_person":              true,
	"inbox":                       true,
	"invoices_attachments":        true,
	"journal_entries":             true,
	"journal_entries_attachments": true,
	"create_invoice_draft":        true,
	"products":                    true,
	"projects":                    true,
	"purchases":                   true,
	"purchases_attachments":       true,
	"purchases_delete":            true,
	"purchases_payments":          true,
	"sales":                       true,
	"sales_attachments":           true,
	"sales_delete":                true,
	"sales_payments":              true,
	"settled":                     true,
	"write_off":                   true,
	"transactions":                true,
	"transactions_delete":         true,
}

// genericResources are canonical names the store legitimately holds with no
// typed table of their own: the sync registry's root resource (`companies`),
// the attachment sub-collections (whose objects carry `identifier`, not `id`,
// so they never extract a primary key anyway) and every read/mutation command
// whose resource has no DDL table. Derived from the resource literals passed to
// resolveRead*/writeMutationResponseToStore in internal/cli; the
// TestCanonicalResource_AcceptsEveryGeneratedCallSiteName guard in
// internal/cli keeps this list honest when the CLI is reprinted.
var genericResources = map[string]bool{
	"account_balances":        true,
	"activities":              true,
	"attachments":             true,
	"bank_balances":           true,
	"companies":               true,
	"credit_notes":            true,
	"delete":                  true,
	"general_journal_entries": true,
	"groups":                  true,
	"invoices":                true,
	"offers":                  true,
	"order_confirmations":     true,
	"payments":                true,
	"time_entries":            true,
	"time_users":              true,
	"user":                    true,
}

// CanonicalResource maps a resource name to the one spelling the store uses:
// kebab-case becomes snake_case, and the result is checked against the closed
// set of names the store knows.
//
// Rejection rule, deliberately asymmetric:
//
//   - A malformed name (empty, or carrying anything outside [A-Za-z0-9_-]) is
//     always an error.
//   - A HYPHENATED name that does not resolve to a known resource is always an
//     error. The hyphen spelling is exactly the bug class of issue #6: it means
//     a caller used the CLI/kebab name for a resource the store has never heard
//     of, and guessing would recreate the silent miss this function exists to
//     stop. `CanonicalResource("no-such-thing")` therefore fails.
//   - An unknown name that is already separator-free is returned unchanged as a
//     generic resource. `resources` is a generic mirror: it must be able to hold
//     a resource with no typed table, and the printed store's own regression
//     suite writes synthetic types (rt_0, numeric_ids, present_type, …) through
//     these entry points. Such a name can only ever take the generic path, and
//     the loud default arms make sure it cannot shadow a typed table.
//
// The returned name is what every downstream lookup keys on: the typed-table
// dispatch, resourceParentKeyColumns, resourceIDFieldOverrides and the
// resource_type column in `resources`.
func CanonicalResource(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("resource name is empty")
	}
	if !resourceNameRE.MatchString(trimmed) {
		return "", fmt.Errorf("malformed resource name %q: expected [A-Za-z][A-Za-z0-9_-]*", name)
	}
	canonical := strings.ReplaceAll(trimmed, "-", "_")
	if typedTableResources[canonical] || genericResources[canonical] {
		return canonical, nil
	}
	if strings.Contains(trimmed, "-") {
		return "", fmt.Errorf(
			"unknown resource %q (canonicalises to %q, which is not a resource this store knows); "+
				"add it to internal/store/resource_name.go if it is real",
			name, canonical)
	}
	return canonical, nil
}

// HasTypedTable reports whether a canonical resource name owns a
// domain-specific table. Used by the default arms of the two upsert dispatches:
// a name that answers true here and still reaches the default arm is a
// dispatch bug and must be surfaced, not written generically.
func HasTypedTable(canonicalResource string) bool {
	return typedTableResources[canonicalResource]
}

// IsKnownResource reports whether the canonical name is in the closed set at
// all (typed or generic). A false here means the name only survives
// CanonicalResource as an opaque generic type.
func IsKnownResource(canonicalResource string) bool {
	return typedTableResources[canonicalResource] || genericResources[canonicalResource]
}

// TypedTableResources returns the canonical names that own a typed table, in
// no particular order. `doctor` uses it to cross-check row counts.
func TypedTableResources() []string {
	out := make([]string, 0, len(typedTableResources))
	for name := range typedTableResources {
		out = append(out, name)
	}
	return out
}

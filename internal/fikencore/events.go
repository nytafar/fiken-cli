// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated. Shared operation vocabulary for the
// append-only audit log (build-spec §7). Operation names follow a `noun.verb`
// grammar; the grammar is the rule, the seed list below is NOT a closed
// taxonomy. New operations are added as constants by following the pattern —
// no migration. log-event WARNS on an unknown-but-grammar-valid operation and
// never rejects it, so a genuinely new event (e.g. payout.reconciled) is still
// recorded; only a grammar violation (e.g. "cmmit") is refused.
//
// Every writer — the CLI write commands AND the log-event sink the browser
// "Superføring" harness calls — references these constants so one event never
// gets two spellings.
package fikencore

import "regexp"

// Operation grammar: lowercase noun.verb(.verb...), dot-separated.
// Anchored so a stray prefix/suffix or uppercase fails.
var operationGrammar = regexp.MustCompile(`^[a-z]+(\.[a-z]+)+$`)

// Seed vocabulary (open set — extend by adding constants). Grouped by family;
// query a family with `WHERE operation LIKE 'posting.%'`.
const (
	// Posting lifecycle (commit / reverse).
	OpPostingCommitted = "posting.committed"
	OpPostingReversed  = "posting.reversed"
	OpDraftCreated     = "draft.created"

	// Reconciliation / matching.
	OpMatchAccepted     = "match.accepted"
	OpMatchConfirmed    = "match.confirmed"
	OpPaymentRegistered = "payment.registered"
	OpSaleSettled       = "sale.settled"

	// Inbox / documents.
	OpInboxAdded = "inbox.added"

	// Browser "Superføring" surface (Round-2 UI test: bank-line lifecycle is
	// create -> refresh -> confirm, recorded as voucher.created then
	// match.confirmed).
	OpVoucherCreated = "voucher.created"
	OpLineClassified = "line.classified"
	OpFilterCreated  = "filter.created"
)

// knownOperations is the seed set log-event recognizes without a warning.
// Membership is advisory only — unknown grammar-valid operations are still
// accepted (with a warning), never rejected.
var knownOperations = map[string]struct{}{
	OpPostingCommitted:  {},
	OpPostingReversed:   {},
	OpDraftCreated:      {},
	OpMatchAccepted:     {},
	OpMatchConfirmed:    {},
	OpPaymentRegistered: {},
	OpSaleSettled:       {},
	OpInboxAdded:        {},
	OpVoucherCreated:    {},
	OpLineClassified:    {},
	OpFilterCreated:     {},
}

// OperationGrammarValid reports whether op satisfies the noun.verb grammar.
// A false result is a hard error for log-event (typo / malformed); a true
// result is always accepted.
func OperationGrammarValid(op string) bool {
	return operationGrammar.MatchString(op)
}

// OperationKnown reports whether op is in the seed vocabulary. A false result
// is a soft signal (log-event warns "vocabulary growth") — never a rejection.
func OperationKnown(op string) bool {
	_, ok := knownOperations[op]
	return ok
}

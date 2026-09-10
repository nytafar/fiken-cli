// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — an index over the `transactions` resource, which the
// sync registry has always mirrored but no detector read. A transaction is the
// journal side of a document: its `entries[]` are journal entries and each
// entry's `lines[]` carry a signed `amount` (a credit is negative), an
// `account` (possibly a `2400:20079` sub-account) and, when Fiken derived it, a
// numeric `vatCode` as a string. Two detectors need the same lookup — drift's
// settled residual on the supplier account and mva-summary's reverse-charge
// cross-check against the 2702/2712 pair — so the index is built once here.
//
// Every method is nil-safe: when no transactions are mirrored the index is nil
// and the callers see an empty journal rather than a crash.
package cli

// pp:data-source local

import (
	"context"
	"encoding/json"
	"strings"

	"fiken-cli/internal/store"
)

// supplierAccount is the Norwegian standard leverandørgjeld account. It is a
// real hierarchical code: each supplier gets a `2400:NNNNN` sub-account, so
// matching it needs a prefix — the one legitimate prefix match in detector
// code, kept behind isSupplierAccount so there is a single place to read.
const supplierAccount = "2400"

// The reverse-charge (snudd avregning) VAT pair Fiken posts itself for a
// purchase of services from abroad: 2702 "Utgående mva, kjøp tjenester fra
// utlandet" as a credit (negative amount) and 2712 "Inngående mva, kjøp
// tjenester fra utlandet" as the matching debit. The order line carries no VAT
// at all for those types, so the journal is the only place the amount exists.
const (
	reverseChargeOutputAccount = "2702"
	reverseChargeInputAccount  = "2712"
)

// accountIs reports whether a journal line's account code is the given ledger
// account, tolerating Fiken's `<account>:<sub>` sub-account form.
func accountIs(code, account string) bool {
	return code == account || strings.HasPrefix(code, account+":")
}

// isSupplierAccount reports whether a journal line sits on leverandørgjeld
// (2400, or any of its per-supplier sub-accounts).
func isSupplierAccount(code string) bool {
	return accountIs(code, supplierAccount)
}

// jsonBoolField extracts a boolean document field (Fiken's `settled`/`paid`),
// tolerating absence (false) and a string-encoded "true".
func jsonBoolField(m map[string]json.RawMessage, key string) bool {
	raw, ok := m[key]
	if !ok {
		return false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b
	}
	return strings.Trim(string(raw), `"`) == "true"
}

// txLine is one journal line of a transaction: the signed amount in øre, the
// account it sits on and the numeric MVA code Fiken stamped on it (a string in
// the API, empty when there is none). Journal lines carry no vatType — the
// string taxonomy lives on order lines only.
type txLine struct {
	Account   string
	AmountOre int64
	VATCode   string
}

// txIndex maps a transactionId to its journal lines, flattened across the
// transaction's entries. A document points at its transaction by
// `transactionId`, so this is the join both detectors need.
type txIndex struct {
	byTx map[int64][]txLine
}

// buildTxIndex flattens mirrored transaction rows into the index. Rows without
// a transactionId are skipped; a row with no lines still registers, so a
// caller can tell "no such transaction" from "transaction with an empty
// journal".
func buildTxIndex(txs []map[string]json.RawMessage) *txIndex {
	ix := &txIndex{byTx: make(map[int64][]txLine, len(txs))}
	for _, tx := range txs {
		id, ok := jsonInt(tx, "transactionId")
		if !ok || id == 0 {
			continue
		}
		lines := ix.byTx[id]
		if lines == nil {
			lines = []txLine{}
		}
		for _, entry := range jsonObjects(tx, "entries") {
			for _, ln := range jsonObjects(entry, "lines") {
				amount, _ := jsonInt(ln, "amount")
				lines = append(lines, txLine{
					Account:   jsonStr(ln, "account"),
					AmountOre: amount,
					VATCode:   jsonStr(ln, "vatCode"),
				})
			}
		}
		ix.byTx[id] = lines
	}
	return ix
}

// loadTxIndex reads the company's mirrored transactions and indexes them. A
// company that has never synced the resource yields an empty (but non-nil)
// index, which every method handles.
func loadTxIndex(ctx context.Context, db *store.Store, slug string) (*txIndex, error) {
	txs, err := loadCompanyResources(ctx, db, "transactions", slug)
	if err != nil {
		return nil, err
	}
	return buildTxIndex(txs), nil
}

// count is the number of transactions indexed, reported in a detector's params
// so an empty journal is visible rather than mistaken for a clean book.
func (ix *txIndex) count() int {
	if ix == nil {
		return 0
	}
	return len(ix.byTx)
}

// has reports whether the transaction is in the index at all. A document
// pointing at a transaction that was never synced must be skipped and counted,
// not read as a zero balance.
func (ix *txIndex) has(txID int64) bool {
	if ix == nil {
		return false
	}
	_, ok := ix.byTx[txID]
	return ok
}

// lines returns the transaction's journal lines (nil when unknown).
func (ix *txIndex) lines(txID int64) []txLine {
	if ix == nil {
		return nil
	}
	return ix.byTx[txID]
}

// sumAccount sums the signed amounts of the transaction's lines whose account
// the matcher accepts. Signs are Fiken's: a credit is negative, so a fully
// settled supplier balance sums to 0.
func (ix *txIndex) sumAccount(txID int64, match func(account string) bool) int64 {
	var sum int64
	for _, ln := range ix.lines(txID) {
		if match(ln.Account) {
			sum += ln.AmountOre
		}
	}
	return sum
}

// reverseChargeOutputVAT is the output (utgående) VAT the journal booked for a
// reverse-charge purchase: the 2702 lines are credits, so the amount is their
// negated sum. 2712 is the mirror deduction and is deliberately NOT added —
// the pair nets to zero, and the quantity being cross-checked is the output
// half alone. When a transaction carries only the deduction side (no 2702 at
// all), the 2712 debit is used instead, so the check compares like with like
// rather than reporting the whole bucket as missing.
func (ix *txIndex) reverseChargeOutputVAT(txID int64) int64 {
	var out, in int64
	for _, ln := range ix.lines(txID) {
		switch {
		case accountIs(ln.Account, reverseChargeOutputAccount):
			out -= ln.AmountOre
		case accountIs(ln.Account, reverseChargeInputAccount):
			in += ln.AmountOre
		}
	}
	if out == 0 {
		return in
	}
	return out
}

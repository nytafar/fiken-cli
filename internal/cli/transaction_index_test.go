// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the transaction index.
// Fixtures are synthetic JSON in the shape the mirror stores.
package cli

import (
	"encoding/json"
	"testing"
)

// txRows decodes a JSON array of transactions into the row shape
// loadCompanyResources hands buildTxIndex.
func txRows(t *testing.T, raw string) []map[string]json.RawMessage {
	t.Helper()
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	return rows
}

const txFixture = `[
  {"transactionId": 1001, "type": "Kjøp", "entries": [
    {"journalEntryId": 1, "lines": [
      {"amount": 100000, "account": "6553"},
      {"amount": -100000, "account": "2400:20001"}
    ]},
    {"journalEntryId": 2, "lines": [
      {"amount": 100000, "account": "2400:20001"},
      {"amount": -100000, "account": "1920:10001"}
    ]}
  ]},
  {"transactionId": 1002, "type": "Kjøp", "entries": [
    {"journalEntryId": 3, "lines": [
      {"amount": 200000, "account": "6553"},
      {"amount": -200000, "account": "2400"},
      {"amount": -50000, "account": "2702"},
      {"amount": 50000, "account": "2712", "vatCode": "86"}
    ]}
  ]},
  {"type": "Kjøp", "entries": [{"lines": [{"amount": 1, "account": "2400"}]}]}
]`

func TestBuildTxIndex(t *testing.T) {
	ix := buildTxIndex(txRows(t, txFixture))
	if ix.count() != 2 {
		t.Fatalf("indexed %d transactions, want 2 (the row without a transactionId is skipped)", ix.count())
	}
	if !ix.has(1001) || ix.has(9999) {
		t.Errorf("has() = (%v, %v), want (true, false)", ix.has(1001), ix.has(9999))
	}
	if got := len(ix.lines(1001)); got != 4 {
		t.Errorf("lines(1001) = %d lines, want 4 flattened across both entries", got)
	}
	if got := ix.lines(9999); got != nil {
		t.Errorf("lines of an unknown transaction = %+v, want nil", got)
	}
	if got := ix.lines(1002)[3].VATCode; got != "86" {
		t.Errorf("journal line vatCode = %q, want %q", got, "86")
	}
}

func TestTxIndexSumAccount(t *testing.T) {
	ix := buildTxIndex(txRows(t, txFixture))
	tests := []struct {
		name string
		txID int64
		want int64
	}{
		// -100000 (the purchase) +100000 (the payment) = settled.
		{"settled purchase nets to zero on the supplier account", 1001, 0},
		// Booked but never paid: the credit still stands.
		{"unpaid purchase leaves the credit", 1002, -200000},
		{"unknown transaction sums to zero", 4242, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ix.sumAccount(tt.txID, isSupplierAccount); got != tt.want {
				t.Errorf("sumAccount = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestIsSupplierAccount(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{"2400", true},
		{"2400:20001", true},
		{"2400:20079", true},
		{"24001", false}, // a different account, not a sub-account
		{"1920:10001", false},
		{"6553", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			if got := isSupplierAccount(tt.code); got != tt.want {
				t.Errorf("isSupplierAccount(%q) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}

// TestTxIndexReverseChargeOutputVAT pins the sign rule: 2702 is a credit, so
// the output VAT is its negated sum; 2712 is the mirror deduction and must not
// be added to it.
func TestTxIndexReverseChargeOutputVAT(t *testing.T) {
	ix := buildTxIndex(txRows(t, txFixture))
	if got := ix.reverseChargeOutputVAT(1002); got != 50000 {
		t.Errorf("reverseChargeOutputVAT(1002) = %d, want 50000", got)
	}
	if got := ix.reverseChargeOutputVAT(1001); got != 0 {
		t.Errorf("a transaction with no reverse-charge pair = %d, want 0", got)
	}
}

// TestTxIndexNilSafe: an unsynced `transactions` resource must disable the
// journal checks, never panic.
func TestTxIndexNilSafe(t *testing.T) {
	var ix *txIndex
	if ix.count() != 0 || ix.has(1) || ix.lines(1) != nil ||
		ix.sumAccount(1, isSupplierAccount) != 0 || ix.reverseChargeOutputVAT(1) != 0 {
		t.Error("a nil index must behave as an empty one")
	}
	empty := buildTxIndex(nil)
	if empty == nil || empty.count() != 0 {
		t.Error("buildTxIndex(nil) must return an empty, usable index")
	}
}

func TestJSONBoolField(t *testing.T) {
	row := txRows(t, `[{"settled": true, "paid": false, "stringy": "true", "n": 1}]`)[0]
	tests := []struct {
		key  string
		want bool
	}{
		{"settled", true},
		{"paid", false},
		{"stringy", true},
		{"n", false},
		{"absent", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := jsonBoolField(row, tt.key); got != tt.want {
				t.Errorf("jsonBoolField(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

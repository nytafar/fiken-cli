// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the missing-bilag attachment
// predicate (the load-bearing pure logic of the command).
package cli

import (
	"encoding/json"
	"fmt"
	"testing"
)

func mustObj(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("bad fixture %q: %v", s, err)
	}
	return m
}

func TestHasAttachments(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		doc          string
		want         bool
	}{
		{"purchase without attachment field", "purchases", `{"purchaseId":1,"date":"2026-01-01"}`, false},
		{"purchase empty array", "purchases", `{"purchaseId":1,"purchaseAttachments":[]}`, false},
		{"purchase null array", "purchases", `{"purchaseId":1,"purchaseAttachments":null}`, false},
		{"purchase with one attachment", "purchases", `{"purchaseId":1,"purchaseAttachments":[{"identifier":"a"}]}`, true},
		{"purchase with several attachments", "purchases", `{"purchaseAttachments":[{"identifier":"a"},{"identifier":"b"}]}`, true},
		// The bug this table guards: purchases carry purchaseAttachments, so a
		// bare "attachments" key on a purchase must not count as a bilag, and a
		// documented purchase must not be reported as missing one.
		{"purchase bare attachments key does not count", "purchases", `{"purchaseId":1,"attachments":[{"identifier":"a"}]}`, false},
		{"sale with saleAttachments", "sales", `{"saleId":1,"saleAttachments":[{"uuid":"u"}]}`, true},
		{"sale empty saleAttachments", "sales", `{"saleId":1,"saleAttachments":[]}`, false},
		{"sale bare attachments key does not count", "sales", `{"saleId":1,"attachments":[{"uuid":"u"}]}`, false},
		{"journal entry with attachments", "journal_entries", `{"journalEntryId":1,"attachments":[{"uuid":"u"}]}`, true},
		{"journal entry empty attachments", "journal_entries", `{"journalEntryId":1,"attachments":[]}`, false},
		{"journal entry absent attachments", "journal_entries", `{"journalEntryId":1}`, false},
		{"unknown type falls back to attachments", "invoices", `{"attachments":[{"uuid":"u"}]}`, true},
		{"unknown type without attachments", "invoices", `{"saleAttachments":[{"uuid":"u"}]}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasAttachments(tt.resourceType, mustObj(t, tt.doc)); got != tt.want {
				t.Errorf("hasAttachments(%q, %s) = %v, want %v", tt.resourceType, tt.doc, got, tt.want)
			}
		})
	}
}

// ---- Classification (issue #28) ----

// bilagFixture is a period where every purchase and sale is documented and
// the journal holds what a real term holds: the documents' own postings,
// payments on them, a provider settlement, the VAT settlement, a salary run
// and its tax transfer, a bank fee and an internal transfer. None of those
// journal entries has an attachment.
var bilagFixture = map[string]string{
	"purchases": `[
	  {"purchaseId": 1, "date": "2026-07-03", "kind": "supplier", "transactionId": 501, "identifier": "F-100",
	   "supplier": {"contactId": 7, "name": "Hosting AS"},
	   "lines": [{"netPrice": 100000, "vat": 25000, "vatType": "HIGH", "account": "6540"}], "purchaseAttachments": [{"identifier": "1"}]},
	  {"purchaseId": 2, "date": "2026-07-09", "kind": "cash_purchase", "transactionId": 502,
	   "lines": [{"netPrice": 20000, "vat": 5000, "vatType": "HIGH", "account": "6800"}], "purchaseAttachments": [{"identifier": "2"}]}
	]`,
	"sales": `[
	  {"saleId": 11, "date": "2026-07-05", "kind": "invoice", "transactionId": 601, "netAmount": 400000, "vatAmount": 100000,
	   "customer": {"contactId": 9, "name": "Kunde AS"}, "saleAttachments": [{"uuid": "a"}]}
	]`,
	"credit_notes": `[
	  {"creditNoteId": 21, "issueDate": "2026-07-20", "sale": {"saleId": 12, "date": "2026-07-20", "transactionId": 602}}
	]`,
	"journal_entries": `[
	  {"journalEntryId": 1, "date": "2026-07-03", "transactionType": "Kjøp", "transactionId": 501, "lines": [{"amount": 125000, "account": "6540"}]},
	  {"journalEntryId": 2, "date": "2026-07-10", "transactionType": "Betalingsoppgjør", "transactionId": 501, "lines": [{"amount": 125000, "account": "2400:1"}]},
	  {"journalEntryId": 3, "date": "2026-07-09", "transactionType": "Kjøp", "transactionId": 502, "lines": [{"amount": 25000, "account": "6800"}]},
	  {"journalEntryId": 4, "date": "2026-07-05", "transactionType": "Salg", "transactionId": 601, "lines": [{"amount": 500000, "account": "1500:1"}]},
	  {"journalEntryId": 5, "date": "2026-07-19", "transactionType": "Betalingsoppgjør", "transactionId": 601, "lines": [{"amount": 500000, "account": "1920:1"}]},
	  {"journalEntryId": 6, "date": "2026-07-20", "transactionType": "Kreditert salg", "transactionId": 602, "lines": [{"amount": 10000, "account": "3000"}]},
	  {"journalEntryId": 7, "date": "2026-07-21", "transactionType": "PayPal PoS", "transactionId": 701, "lines": [{"amount": 90000, "account": "1920:1"}]},
	  {"journalEntryId": 8, "date": "2026-08-31", "transactionType": "Mva-oppgjør", "transactionId": 702, "lines": [{"amount": 70000, "account": "2701"}]},
	  {"journalEntryId": 9, "date": "2026-07-25", "transactionType": "Lønn", "transactionId": 703, "lines": [{"amount": 3000000, "account": "5000"}]},
	  {"journalEntryId": 10, "date": "2026-08-15", "transactionType": "Betaling av forskuddstrekk", "transactionId": 704, "lines": [{"amount": 900000, "account": "2600"}]},
	  {"journalEntryId": 11, "date": "2026-07-31", "transactionType": "Bankomkostning", "transactionId": 705, "lines": [{"amount": 4500, "account": "7770"}]},
	  {"journalEntryId": 12, "date": "2026-08-01", "transactionType": "Intern overføring", "transactionId": 706, "lines": [{"amount": 1000000, "account": "1920:2"}]},
	  {"journalEntryId": 13, "date": "2026-08-02", "transactionType": "Betalingsoppgjør", "transactionId": 799, "lines": [{"amount": 1200, "account": "1920:1"}]}
	]`,
}

func TestMissingBilag_DocumentedPeriodReportsNothingAndCountsWhatItSkipped(t *testing.T) {
	var rep Report
	runOnMirror(t, seedMirror(t, bilagFixture), &rep, "missing-bilag", "--period", "2026-07-01:2026-08-31")
	if rep.Total != 0 || len(rep.Findings) != 0 {
		t.Fatalf("findings = %+v, want none on a period where every purchase and sale is documented", rep.Findings)
	}
	want := map[string]int{
		"document_posting":      4, // the two purchases, the sale and the credit note
		"payment":               3, // two on documents, one on a transaction the mirror does not hold
		"provider_settlement":   1,
		"vat_settlement":        1,
		"salary_and_tax":        2,
		"bank_fee_and_interest": 1,
		"internal_transfer":     1,
	}
	if len(rep.Excluded) != len(want) {
		t.Fatalf("excluded = %v, want %v", rep.Excluded, want)
	}
	for class, n := range want {
		if rep.Excluded[class] != n {
			t.Errorf("excluded[%s] = %d, want %d (all: %v)", class, rep.Excluded[class], n, rep.Excluded)
		}
	}
}

func TestMissingBilag_ReportsWhatNeedsItsOwnBilag(t *testing.T) {
	fixture := map[string]string{
		"purchases": `[
		  {"purchaseId": 1, "date": "2026-07-03", "kind": "supplier", "transactionId": 501, "identifier": "F-100",
		   "supplier": {"contactId": 7, "name": "Hosting AS"}, "lines": [{"netPrice": 100000, "vat": 25000, "vatType": "HIGH", "account": "6540"}]},
		  {"purchaseId": 2, "date": "2026-07-09", "kind": "cash_purchase", "transactionId": 502,
		   "lines": [{"netPrice": 20000, "vat": 5000, "vatType": "HIGH", "account": "6800"}], "purchaseAttachments": []},
		  {"purchaseId": 3, "date": "2026-07-10", "kind": "supplier", "deleted": true, "lines": []}
		]`,
		"sales": `[
		  {"saleId": 11, "date": "2026-07-05", "kind": "cash_sale", "transactionId": 601, "netAmount": 400000, "vatAmount": 100000}
		]`,
		"journal_entries": `[
		  {"journalEntryId": 1, "date": "2026-07-03", "transactionType": "Kjøp", "transactionId": 501, "lines": [{"amount": 125000, "account": "6540"}]},
		  {"journalEntryId": 2, "date": "2026-07-12", "transactionType": "Fri", "transactionId": 801, "description": "Korrigering", "lines": [{"amount": 30000, "account": "6300"}, {"amount": -30000, "account": "1920:1"}]},
		  {"journalEntryId": 3, "date": "2026-07-13", "transactionType": "Fri", "transactionId": 802, "lines": [{"amount": 100, "account": "6300"}], "attachments": [{"uuid": "x"}]},
		  {"journalEntryId": 4, "date": "2026-07-14", "transactionType": "Tolldeklarasjon", "transactionId": 803, "lines": [{"amount": 50000, "account": "2714"}]},
		  {"journalEntryId": 5, "date": "2026-07-15", "transactionType": "Noe nytt fra Fiken", "transactionId": 804, "lines": [{"amount": 700, "account": "7790"}]},
		  {"journalEntryId": 6, "date": "2026-07-16", "transactionType": "Bankomkostning", "transactionId": 805, "lines": [{"amount": 4500, "account": "7770"}]},
		  {"journalEntryId": 2, "date": "2026-07-12", "lines": [{"amount": 30000, "account": "6300"}]}
		]`,
	}
	db := seedMirror(t, fixture)

	var rep Report
	runOnMirror(t, db, &rep, "missing-bilag", "--period", "2026-07")
	got := map[string]string{}
	for _, f := range rep.Findings {
		class, _ := f.Detail["class"].(string)
		got[fmt.Sprintf("%s#%d", f.DocType, f.DocID)] = class
	}
	want := map[string]string{
		"purchase#1":      "",
		"purchase#2":      "",
		"sale#11":         "",
		"journal_entry#2": "free_posting",
		"journal_entry#4": "customs_declaration",
		"journal_entry#5": "unknown_type",
	}
	if len(got) != len(want) {
		t.Fatalf("findings = %v, want %v", got, want)
	}
	for k, class := range want {
		if c, ok := got[k]; !ok || c != class {
			t.Errorf("finding %s class = %q (present %v), want %q", k, c, ok, class)
		}
	}
	if rep.Excluded["bank_fee_and_interest"] != 1 || rep.Excluded["document_posting"] != 1 {
		t.Errorf("excluded = %v, want the bank fee and the purchase's own posting", rep.Excluded)
	}

	// --include reports an excluded class and stops counting it as excluded.
	var withFees Report
	runOnMirror(t, db, &withFees, "missing-bilag", "--period", "2026-07", "--include", "bank_fee_and_interest")
	if withFees.Total != rep.Total+1 || withFees.Excluded["bank_fee_and_interest"] != 0 {
		t.Fatalf("--include: total %d excluded %v, want one more finding and no bank fees excluded", withFees.Total, withFees.Excluded)
	}
	var all Report
	runOnMirror(t, db, &all, "missing-bilag", "--period", "2026-07", "--include", "all")
	if len(all.Excluded) != 0 || all.Total != rep.Total+2 {
		t.Fatalf("--include all: total %d excluded %v, want everything reported", all.Total, all.Excluded)
	}
}

func TestParseBilagInclude(t *testing.T) {
	if _, err := parseBilagInclude("payment, nonsense"); err == nil {
		t.Fatal("an unknown class must be a usage error, not a silent no-op")
	}
	got, err := parseBilagInclude(" payment ,vat_settlement,")
	if err != nil || len(got) != 2 || !got["payment"] || !got["vat_settlement"] {
		t.Fatalf("got %v, %v", got, err)
	}
	if none, _ := parseBilagInclude(""); len(none) != 0 {
		t.Fatalf("empty flag = %v, want nothing included", none)
	}
}

func TestJournalEntryClass_LinkToADocumentWinsOverTheType(t *testing.T) {
	docTx := map[int64]bool{501: true}
	tests := []struct{ entry, want string }{
		{`{"transactionType":"Fri","transactionId":501}`, "document_posting"},
		{`{"transactionType":"Betalingsoppgjør","transactionId":501}`, "payment"},
		{`{"transactionType":"","transactionId":501}`, "document_posting"},
		{`{"transactionType":"Fri","transactionId":900}`, "free_posting"},
		{`{"transactionType":"Gjeld"}`, "free_posting"},
		{`{"transactionId":900}`, "unknown_type"},
	}
	for _, tt := range tests {
		if got := journalEntryClass(mustObj(t, tt.entry), docTx); got != tt.want {
			t.Errorf("journalEntryClass(%s) = %q, want %q", tt.entry, got, tt.want)
		}
	}
}

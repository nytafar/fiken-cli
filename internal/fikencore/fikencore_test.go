// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL). Tests for the core write-layer logic: operation
// grammar, VAT reference, proposal validation, request construction (incl. §9
// date-alignment), and the core DB (idempotency guard + append-only audit).
package fikencore

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOperationGrammar(t *testing.T) {
	cases := map[string]bool{
		"posting.committed": true, "match.confirmed": true, "payout.reconciled": true,
		"a.b.c": true, "cmmit": false, "Posting.Committed": false, "posting.": false,
		".committed": false, "posting committed": false, "": false,
	}
	for op, want := range cases {
		if got := OperationGrammarValid(op); got != want {
			t.Errorf("OperationGrammarValid(%q) = %v, want %v", op, got, want)
		}
	}
	if !OperationKnown(OpVoucherCreated) || !OperationKnown(OpMatchConfirmed) {
		t.Error("seed operations voucher.created/match.confirmed should be known")
	}
	if OperationKnown("payout.reconciled") {
		t.Error("a novel grammar-valid op should be unknown (warn, not reject)")
	}
}

func TestVATReference(t *testing.T) {
	if !VATTypeValidFor("HIGH", SideSales) || !VATTypeValidFor("HIGH", SidePurchases) {
		t.Error("HIGH should be valid both sides")
	}
	if VATTypeValidFor("HIGH_DIRECT", SideSales) {
		t.Error("HIGH_DIRECT is purchases-only")
	}
	if !VATTypeValidFor("EXEMPT", SideSales) || VATTypeValidFor("EXEMPT", SidePurchases) {
		t.Error("EXEMPT is sales-only")
	}
	if info, ok := VATCode(3); !ok || info.Type != "HIGH" {
		t.Errorf("VATCode(3) = %+v,%v want HIGH", info, ok)
	}
	if _, ok := VATCode(999); ok {
		t.Error("VATCode(999) should be unknown")
	}
}

func validPurchaseProposal() Proposal {
	return Proposal{
		CompanySlug: "demo", Kind: ProposalKindPurchase,
		SourceSystem: "inbox", SourceID: "42", BankLineDate: "2026-05-20",
		Purchase: &PurchaseProposal{
			Kind: "supplier", Paid: false, SupplierID: 100, Currency: "NOK",
			Lines: []ProposalLine{{Description: "Rent", NetPrice: 100000, Vat: 25000, Account: "6300", VatType: "HIGH"}},
		},
	}
}

func TestValidateProposal(t *testing.T) {
	accts := map[string]bool{"6300:10001": true}

	ok := ValidateProposal(validPurchaseProposal(), accts)
	if !ok.OK {
		t.Fatalf("valid purchase should pass, got errors: %+v", ok.Errors)
	}

	// Wrong-side vatType is a hard error.
	bad := validPurchaseProposal()
	bad.Purchase.Lines[0].VatType = "EXEMPT" // sales-only
	if res := ValidateProposal(bad, accts); res.OK {
		t.Error("sales-only vatType on a purchase line should be an error")
	}

	// Missing idempotency key.
	noKey := validPurchaseProposal()
	noKey.SourceID = ""
	if res := ValidateProposal(noKey, accts); res.OK {
		t.Error("missing source_id should be an error")
	}

	// Unbalanced journal line.
	je := Proposal{CompanySlug: "demo", Kind: ProposalKindJournalEntry, SourceSystem: "manual", SourceID: "x", Date: "2026-05-20",
		JournalEntry: &JournalEntryProposal{Lines: []JournalLine{{Amount: 1000, DebitAccount: "6300"}}}}
	if res := ValidateProposal(je, nil); res.OK {
		t.Error("journal line missing credit_account should be an error")
	}

	// VAT rounding warning (not an error).
	rnd := validPurchaseProposal()
	rnd.Purchase.Lines[0].Vat = 24000 // off from 25000
	res := ValidateProposal(rnd, accts)
	if !res.OK {
		t.Error("a VAT rounding discrepancy should be a warning, not an error")
	}
	if !hasFinding(res.Warnings, "vat_rounding") {
		t.Errorf("expected a vat_rounding warning, got %+v", res.Warnings)
	}
}

func hasFinding(fs []ValidationFinding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}

// One line per regime through the write path: the regimes whose line vat is
// structural must not warn when they are right, and must be hard errors when
// they are wrong.
func TestValidateLineRegimes(t *testing.T) {
	cases := []struct {
		name     string
		vatType  string
		net, vat int64
		wantOK   bool
		wantCode string // in Errors when !wantOK, in Warnings when wantOK and non-empty
	}{
		{name: "ordinary domestic line at rate", vatType: "HIGH", net: 100000, vat: 25000, wantOK: true},
		{name: "1 øre of rounding is tolerated", vatType: "MEDIUM", net: 3333, vat: 501, wantOK: true},
		{name: "rate drift warns, never blocks", vatType: "HIGH", net: 100000, vat: 20000, wantOK: true, wantCode: "vat_rounding"},
		{name: "reverse-charge basis line carries no vat", vatType: "HIGH_FOREIGN_SERVICE_DEDUCTIBLE", net: 200000, vat: 0, wantOK: true},
		{name: "import basis line carries no vat", vatType: "MEDIUM_BASIS", net: 200000, vat: 0, wantOK: true},
		{name: "vat on a basis line is an error", vatType: "HIGH_BASIS", net: 200000, vat: 50000, wantOK: false, wantCode: KindBasisHasVAT},
		{name: "direct line has the vat and no basis", vatType: "HIGH_DIRECT", net: 0, vat: 50000, wantOK: true},
		{name: "a basis on a direct line is an error", vatType: "MEDIUM_DIRECT", net: 200000, vat: 30000, wantOK: false, wantCode: KindDirectHasNet},
		{name: "nondeductible line: inclusive net, negative embedded vat", vatType: "HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE", net: 125000, vat: -25000, wantOK: true},
		{name: "nondeductible with a positive vat is an error", vatType: "HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE", net: 125000, vat: 25000, wantOK: false, wantCode: KindNondeductibleEmbeddedVAT},
		{name: "zero-rated line carries no vat", vatType: "NONE", net: 200000, vat: 0, wantOK: true},
		{name: "vat on a zero-rated line is an error", vatType: "NONE_IMPORT_BASIS", net: 200000, vat: 50000, wantOK: false, wantCode: KindZeroRatedHasVAT},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := validPurchaseProposal()
			p.Purchase.Lines[0].VatType = c.vatType
			p.Purchase.Lines[0].NetPrice = c.net
			p.Purchase.Lines[0].Vat = c.vat
			res := ValidateProposal(p, nil)
			if res.OK != c.wantOK {
				t.Fatalf("OK = %v, want %v (errors %+v, warnings %+v)", res.OK, c.wantOK, res.Errors, res.Warnings)
			}
			if c.wantCode == "" {
				if len(res.Warnings) != 0 {
					t.Errorf("a correctly coded %s line must not warn, got %+v", c.vatType, res.Warnings)
				}
				return
			}
			if c.wantOK {
				if !hasFinding(res.Warnings, c.wantCode) {
					t.Errorf("want warning %q, got %+v", c.wantCode, res.Warnings)
				}
			} else if !hasFinding(res.Errors, c.wantCode) {
				t.Errorf("want error %q, got %+v", c.wantCode, res.Errors)
			}
		})
	}
}

func TestBuildRequestDateAlignment(t *testing.T) {
	p := validPurchaseProposal()
	p.Date = "2026-05-31" // invoice issue date — must be overridden by bank line date
	path, body, entityType, err := BuildRequest(p)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if path != "/companies/demo/purchases" {
		t.Errorf("path = %q", path)
	}
	if entityType != "purchase" {
		t.Errorf("entityType = %q", entityType)
	}
	if body["date"] != "2026-05-20" {
		t.Errorf("date = %v, want bank-line-aligned 2026-05-20 (§9), not the issue date", body["date"])
	}
	if body["kind"] != "supplier" || body["currency"] != "NOK" {
		t.Errorf("purchase body fields wrong: %+v", body)
	}

	// Journal entry path.
	je := Proposal{CompanySlug: "demo", Kind: ProposalKindJournalEntry, Date: "2026-05-20",
		JournalEntry: &JournalEntryProposal{Lines: []JournalLine{{Amount: 1000, DebitAccount: "6300", CreditAccount: "1920"}}}}
	jpath, _, jet, err := BuildRequest(je)
	if err != nil || jpath != "/companies/demo/generalJournalEntries" || jet != "journal_entry" {
		t.Errorf("journal entry BuildRequest = %q,%q,%v", jpath, jet, err)
	}
}

func TestCoreIdempotencyAndAudit(t *testing.T) {
	ctx := context.Background()
	c, err := Open(ctx, filepath.Join(t.TempDir(), "fiken-core.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	rec := IdempotencyRecord{CompanySlug: "demo", SourceSystem: "inbox", SourceID: "42", Status: StatusProposed}
	created, _, err := c.Reserve(ctx, rec)
	if err != nil || !created {
		t.Fatalf("first Reserve should create: created=%v err=%v", created, err)
	}
	created2, existing, err := c.Reserve(ctx, rec)
	if err != nil {
		t.Fatalf("second Reserve err: %v", err)
	}
	if created2 {
		t.Error("second Reserve for same key must NOT create (the double-post guard)")
	}
	if existing.SourceID != "42" {
		t.Errorf("conflict should return existing record, got %+v", existing)
	}

	if err := c.Advance(ctx, "demo", "inbox", "42", StatusCommitted, AdvanceOpts{FikenEntityType: "purchase", FikenEntityID: "999"}); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	got, found, _ := c.Lookup(ctx, "demo", "inbox", "42")
	if !found || got.Status != StatusCommitted || got.FikenEntityID != "999" {
		t.Errorf("after Advance: %+v", got)
	}

	id, err := c.LogEvent(ctx, Event{Surface: "api", Operation: OpPostingCommitted, CompanySlug: "demo", TargetEntityID: "999", Result: "success", InputsJSON: `{"a":1}`})
	if err != nil || id == "" {
		t.Fatalf("LogEvent: id=%q err=%v", id, err)
	}
	events, err := c.RecentEvents(ctx, "demo", 10)
	if err != nil || len(events) != 1 || events[0].Operation != OpPostingCommitted {
		t.Fatalf("RecentEvents: %+v err=%v", events, err)
	}

	// Append-only triggers: UPDATE and DELETE must be rejected.
	if _, err := c.DB().ExecContext(ctx, `UPDATE agent_events SET result='failure' WHERE id=?`, id); err == nil {
		t.Error("UPDATE on agent_events should be blocked by the append-only trigger")
	}
	if _, err := c.DB().ExecContext(ctx, `DELETE FROM agent_events WHERE id=?`, id); err == nil {
		t.Error("DELETE on agent_events should be blocked by the append-only trigger")
	}

	// Bad operation grammar is rejected by LogEvent.
	if _, err := c.LogEvent(ctx, Event{Surface: "api", Operation: "cmmit", Result: "success"}); err == nil {
		t.Error("LogEvent should reject a grammar-invalid operation")
	}
}

func TestVersionedJSON(t *testing.T) {
	if got := ensureVersioned(`{"a":1}`); got != `{"a":1,"v":1}` && got != `{"v":1,"a":1}` {
		t.Errorf("ensureVersioned object = %q, want a v:1 injected", got)
	}
	if got := ensureVersioned(""); got != "" {
		t.Errorf("ensureVersioned empty = %q, want empty", got)
	}
}

func TestULIDSortable(t *testing.T) {
	a := newULID()
	b := newULID()
	if len(a) != 26 || len(b) != 26 {
		t.Fatalf("ULID length: %d, %d (want 26)", len(a), len(b))
	}
	if a == b {
		t.Error("two ULIDs should differ")
	}
}

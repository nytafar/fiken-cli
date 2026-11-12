// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — tests for the duplicate guard on a purchase create
// (issue #25). The command-level tests drive the real tree against the fake
// Fiken in create_result_test.go.
package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// The fake's purchases list: the supplier of purchaseBody already holds
// INV-1001 (paid), another supplier holds the same number, and a deleted
// purchase carries it too.
const existingPurchases = `[
  {"purchaseId":700,"date":"2026-02-27","paid":true,"identifier":"INV-1001","supplier":{"contactId":501,"name":"Hosting AS"}},
  {"purchaseId":701,"date":"2026-02-28","paid":false,"identifier":"INV-1001","supplier":{"contactId":999,"name":"Other AS"}},
  {"purchaseId":702,"date":"2026-03-01","paid":false,"identifier":"INV-1001","deleted":true,"supplier":{"contactId":501,"name":"Hosting AS"}},
  {"purchaseId":703,"date":"2026-03-01","paid":false,"identifier":"INV-2000","supplier":{"contactId":501,"name":"Hosting AS"}}
]`

func TestPurchasesCreate_RefusesExistingInvoiceNumber(t *testing.T) {
	srv := &createServer{existing: existingPurchases}
	out, _, err := runCreateCLI(t, srv, purchaseBody, "purchases", "create", "agensia", "--stdin", "--agent")
	if err == nil {
		t.Fatalf("a purchase the supplier already has must be refused; out=%s", out)
	}
	if ExitCode(err) != duplicateExitCode {
		t.Fatalf("exit code = %d, want %d", ExitCode(err), duplicateExitCode)
	}
	if srv.postCount() != 0 {
		t.Fatalf("the refused create was posted %d time(s)", srv.postCount())
	}
	// The message names the existing purchase, its date and its paid state,
	// and only the one that belongs to this supplier.
	msg := err.Error()
	for _, want := range []string{"INV-1001", "purchase 700", "2026-02-27", "paid", "--idempotent", "--allow-duplicate"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not mention %q", msg, want)
		}
	}
	for _, not := range []string{"701", "702", "703"} {
		if strings.Contains(msg, "purchase "+not) {
			t.Errorf("refusal names purchase %s, which is another supplier's, deleted, or another number: %s", not, msg)
		}
	}
	env := decodeEnvelope(t, out)
	if env["success"] != false || env["reason"] != "duplicate_purchase" {
		t.Fatalf("envelope = %v, want success false and reason duplicate_purchase", env)
	}
	existing, _ := env["existing"].([]any)
	if len(existing) != 1 {
		t.Fatalf("existing = %v, want exactly purchase 700", env["existing"])
	}
	first, _ := existing[0].(map[string]any)
	if first["purchaseId"] != json.Number("700") || first["paid"] != true || first["date"] != "2026-02-27" {
		t.Fatalf("existing[0] = %v", first)
	}
	// The lookup asked for a window around the new purchase's date.
	if len(srv.gets) == 0 || !strings.Contains(srv.gets[0], "dateGe=2025-11-30") || !strings.Contains(srv.gets[0], "dateLe=2026-06-02") {
		t.Fatalf("lookup query = %v, want a ±92 day window around 2026-03-02", srv.gets)
	}
}

func TestPurchasesCreate_IdempotentIsANoOpThatNamesTheExistingID(t *testing.T) {
	srv := &createServer{existing: existingPurchases}
	out, _, err := runCreateCLI(t, srv, purchaseBody, "purchases", "create", "agensia", "--stdin", "--agent", "--idempotent")
	if err != nil {
		t.Fatalf("--idempotent must turn the refusal into success: %v", err)
	}
	if srv.postCount() != 0 {
		t.Fatalf("the no-op posted %d time(s)", srv.postCount())
	}
	env := decodeEnvelope(t, out)
	if env["success"] != true || env["noop"] != true || env["id"] != json.Number("700") {
		t.Fatalf("envelope = %v, want a successful no-op carrying id 700", env)
	}
}

func TestPurchasesCreate_AllowDuplicateCreatesAnyway(t *testing.T) {
	srv := &createServer{existing: existingPurchases}
	out, _, err := runCreateCLI(t, srv, purchaseBody, "purchases", "create", "agensia", "--stdin", "--agent", "--allow-duplicate")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if srv.postCount() != 1 || len(srv.gets) != 0 {
		t.Fatalf("posts=%d lookups=%d, want one post and no lookup", srv.postCount(), len(srv.gets))
	}
	if decodeEnvelope(t, out)["id"] != json.Number("900123") {
		t.Fatalf("the override must create and report the new id: %s", out)
	}
}

func TestPurchasesCreate_NewInvoiceNumberIsCreated(t *testing.T) {
	srv := &createServer{existing: existingPurchases}
	body := strings.Replace(purchaseBody, "INV-1001", "INV-1002", 1)
	if _, _, err := runCreateCLI(t, srv, body, "purchases", "create", "agensia", "--stdin", "--agent"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if srv.postCount() != 1 {
		t.Fatalf("posts = %d, want 1", srv.postCount())
	}
}

func TestPurchasesCreate_DryRunReportsTheRefusal(t *testing.T) {
	srv := &createServer{existing: existingPurchases}
	_, _, err := runCreateCLI(t, srv, purchaseBody, "purchases", "create", "agensia", "--stdin", "--agent", "--dry-run")
	if ExitCode(err) != duplicateExitCode || srv.postCount() != 0 {
		t.Fatalf("a dry run of a duplicate must say it would be refused; err=%v posts=%d", err, srv.postCount())
	}
}

func TestPurchasesCreate_FailedLookupRefuses(t *testing.T) {
	// The fake has no purchases list for this slug, so the lookup 404s. The
	// create must not go ahead blind.
	srv := &createServer{}
	_, _, err := runCreateCLI(t, srv, purchaseBody, "purchases", "create", "annet", "--stdin", "--agent")
	if ExitCode(err) != duplicateExitCode || !strings.Contains(err.Error(), "could not check") {
		t.Fatalf("err = %v, want the guard's own refusal", err)
	}
	if srv.postCount() != 0 {
		t.Fatalf("posted %d time(s) after a failed lookup", srv.postCount())
	}
}

func TestMatchExistingPurchases(t *testing.T) {
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(existingPurchases+""), &rows); err != nil {
		t.Fatal(err)
	}
	cash := map[string]json.RawMessage{}
	_ = json.Unmarshal([]byte(`{"purchaseId":800,"date":"2026-03-02","paid":true,"identifier":"K-77","kind":"cash_purchase"}`), &cash)
	rows = append(rows, cash)

	tests := []struct {
		name       string
		supplierID int64
		identifier string
		want       []int64
	}{
		{"same supplier and number", 501, "INV-1001", []int64{700}},
		{"case and surrounding space do not make a new number", 501, "  inv-1001 ", []int64{700}},
		{"another supplier's number", 999, "INV-1001", []int64{701}},
		{"a supplier that has not used the number", 502, "INV-1001", nil},
		{"cash purchase matches cash purchase", 0, "K-77", []int64{800}},
		{"a supplier purchase never matches a cash purchase", 501, "K-77", nil},
		{"no invoice number, nothing to match", 501, "", nil},
		{"leading zeros are significant", 501, "INV-01001", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []int64
			for _, e := range matchExistingPurchases(rows, tt.supplierID, tt.identifier, "live") {
				got = append(got, e.PurchaseID)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestAnyInt64(t *testing.T) {
	for _, v := range []any{float64(501), int64(501), 501, json.Number("501"), "501", " 501 "} {
		if anyInt64(v) != 501 {
			t.Errorf("anyInt64(%#v) = %d, want 501", v, anyInt64(v))
		}
	}
	if anyInt64(nil) != 0 || anyInt64("x") != 0 {
		t.Error("a missing or unreadable id must read as 0")
	}
}

const commitProposal = `{"company_slug":"agensia","kind":"purchase","source_system":"inbox","source_id":"42","bank_line_date":"2026-03-02",
 "purchase":{"kind":"supplier","paid":false,"currency":"NOK","supplier_id":501,"identifier":"INV-1001",
  "lines":[{"description":"Hosting","net_price":10000,"vat":2500,"account":"6540","vat_type":"HIGH"}]}}`

func TestCommit_RefusesAPurchaseTheSupplierAlreadyHas(t *testing.T) {
	srv := &createServer{existing: existingPurchases}
	out, _, err := runCreateCLI(t, srv, commitProposal, "commit", "--yes", "--agent")
	if ExitCode(err) != duplicateExitCode {
		t.Fatalf("err = %v (exit %d), want the duplicate refusal", err, ExitCode(err))
	}
	if srv.postCount() != 0 {
		t.Fatalf("posted %d time(s)", srv.postCount())
	}
	if !strings.Contains(out, `"duplicate_purchase"`) || !strings.Contains(err.Error(), "purchase 700") {
		t.Fatalf("the refusal must name the existing purchase; out=%s err=%v", out, err)
	}
}

func TestCommit_DryRunShowsItWouldNotCommit(t *testing.T) {
	srv := &createServer{existing: existingPurchases}
	out, _, err := runCreateCLI(t, srv, commitProposal, "commit", "--dry-run", "--agent")
	if err != nil {
		t.Fatalf("a preview must not fail: %v", err)
	}
	env := decodeEnvelope(t, out)
	if env["would_commit"] != false || env["reason"] != "duplicate_purchase" {
		t.Fatalf("preview = %v, want would_commit false with the reason", env)
	}
}

func TestCommit_NewPurchaseRecordsTheIDFromLocation(t *testing.T) {
	srv := &createServer{existing: existingPurchases}
	proposal := strings.Replace(commitProposal, "INV-1001", "INV-1002", 1)
	out, _, err := runCreateCLI(t, srv, proposal, "commit", "--yes", "--agent")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	env := decodeEnvelope(t, out)
	// Fiken's 201 has no body; before #26 this id was recorded as "".
	if env["committed"] != true || env["fiken_entity_id"] != "900123" {
		t.Fatalf("commit result = %v, want committed with fiken_entity_id 900123", env)
	}
}

func TestInboxAlreadyBooked(t *testing.T) {
	rows := func(s string) []map[string]json.RawMessage {
		var out []map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	doc := func(s string) map[string]json.RawMessage { return rows("[" + s + "]")[0] }
	purchases := rows(`[
	  {"purchaseId":1,"date":"2026-02-01","paid":true,"identifier":"INV-55012","supplier":{"contactId":5,"name":"Hosting AS"},
	   "purchaseAttachments":[{"filename":"Receipt-2291-0042.pdf"}]},
	  {"purchaseId":2,"date":"2026-02-03","paid":false,"identifier":"88","supplier":{"contactId":6,"name":"Kiosk AS"},
	   "purchaseAttachments":[{"filename":"invoice.pdf"}]},
	  {"purchaseId":3,"date":"2026-02-04","paid":true,"identifier":"A1","purchaseAttachments":[{"filename":"invoice.pdf"}]},
	  {"purchaseId":4,"date":"2026-02-05","paid":true,"identifier":"A2","purchaseAttachments":[{"filename":"invoice.pdf"}]},
	  {"purchaseId":5,"date":"2026-02-06","paid":true,"identifier":"A3","purchaseAttachments":[{"filename":"invoice.pdf"}]},
	  {"purchaseId":6,"date":"2026-02-07","paid":true,"identifier":"INV-55012","deleted":true,"purchaseAttachments":[{"filename":"receipt-2291-0042.pdf"}]}
	]`)

	t.Run("the same file is attached to a purchase", func(t *testing.T) {
		got := inboxAlreadyBooked(doc(`{"documentId":9,"filename":"receipt-2291-0042.pdf","name":"Receipt"}`), purchases)
		if len(got) != 1 || got[0].PurchaseID != 1 || got[0].MatchedOn != "attachment_filename" || !got[0].Paid {
			t.Fatalf("got %+v, want purchase 1 on its attachment filename", got)
		}
	})
	t.Run("the invoice number is in the document name", func(t *testing.T) {
		got := inboxAlreadyBooked(doc(`{"documentId":9,"filename":"scan.pdf","name":"Faktura INV-55012 Hosting"}`), purchases)
		if len(got) != 1 || got[0].PurchaseID != 1 || got[0].MatchedOn != "invoice_number" {
			t.Fatalf("got %+v, want purchase 1 on its invoice number", got)
		}
	})
	t.Run("a filename every purchase carries identifies none", func(t *testing.T) {
		if got := inboxAlreadyBooked(doc(`{"documentId":9,"filename":"invoice.pdf"}`), purchases); len(got) != 0 {
			t.Fatalf("got %+v, want nothing for a generic filename", got)
		}
	})
	t.Run("a short invoice number is not searched for", func(t *testing.T) {
		if got := inboxAlreadyBooked(doc(`{"documentId":9,"filename":"x.pdf","name":"kvittering 88"}`), purchases); len(got) != 0 {
			t.Fatalf("got %+v, want nothing", got)
		}
	})
	t.Run("no document, an empty list and never nil", func(t *testing.T) {
		if got := inboxAlreadyBooked(nil, purchases); got == nil || len(got) != 0 {
			t.Fatalf("got %#v, want an empty slice", got)
		}
	})
}

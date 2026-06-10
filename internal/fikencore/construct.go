// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL). Turns a Proposal into the Fiken request body + path.
// The single place the §9 date-alignment rule is applied: the posting's date
// (Fakturadato for a purchase) is set to the bank-line date so the API-posted
// entry rendezvous with the waiting bank line on refresh. This builds the
// posting; it does NOT replicate Fiken's matcher logic (the matching is
// Fiken's, done UI-side).
package fikencore

import "fmt"

// BuildRequest constructs the company-scoped path and JSON body for a proposal.
// entityType is the idempotency_map fiken_entity_type ("purchase"|"journal_entry").
func BuildRequest(p Proposal) (path string, body map[string]any, entityType string, err error) {
	if p.CompanySlug == "" {
		return "", nil, "", fmt.Errorf("proposal missing company_slug")
	}
	date := p.EffectiveDate()
	if date == "" {
		return "", nil, "", fmt.Errorf("proposal missing date (and no bank_line_date to align to)")
	}

	switch p.Kind {
	case ProposalKindPurchase:
		if p.Purchase == nil {
			return "", nil, "", fmt.Errorf("purchase proposal missing purchase body")
		}
		body, err = buildPurchaseBody(date, *p.Purchase)
		if err != nil {
			return "", nil, "", err
		}
		return "/companies/" + p.CompanySlug + "/purchases", body, "purchase", nil

	case ProposalKindJournalEntry:
		if p.JournalEntry == nil {
			return "", nil, "", fmt.Errorf("journal_entry proposal missing journal_entry body")
		}
		body = buildJournalEntryBody(date, p.Description, *p.JournalEntry)
		return "/companies/" + p.CompanySlug + "/generalJournalEntries", body, "journal_entry", nil

	default:
		return "", nil, "", fmt.Errorf("unknown proposal kind %q (want purchase|journal_entry)", p.Kind)
	}
}

func buildPurchaseBody(date string, pp PurchaseProposal) (map[string]any, error) {
	if pp.Kind == "" {
		return nil, fmt.Errorf("purchase missing kind (cash_purchase|supplier)")
	}
	if len(pp.Lines) == 0 {
		return nil, fmt.Errorf("purchase has no lines")
	}
	currency := pp.Currency
	if currency == "" {
		currency = "NOK"
	}
	lines := make([]map[string]any, 0, len(pp.Lines))
	for _, l := range pp.Lines {
		line := map[string]any{
			"description": l.Description,
			"netPrice":    l.NetPrice,
			"vat":         l.Vat,
			"vatType":     l.VatType,
		}
		if l.Account != "" {
			line["account"] = l.Account
		}
		if l.ProjectID != 0 {
			line["projectId"] = l.ProjectID
		}
		lines = append(lines, line)
	}
	body := map[string]any{
		"date":     date, // Fakturadato — aligned to bank-line date (§9)
		"kind":     pp.Kind,
		"paid":     pp.Paid,
		"currency": currency,
		"lines":    lines,
	}
	if pp.Identifier != "" {
		body["identifier"] = pp.Identifier
	}
	if pp.SupplierID != 0 {
		body["supplierId"] = pp.SupplierID
	}
	if pp.PaymentAccount != "" {
		body["paymentAccount"] = pp.PaymentAccount
	}
	if pp.PaymentDate != "" {
		body["paymentDate"] = pp.PaymentDate
	}
	return body, nil
}

func buildJournalEntryBody(date, description string, je JournalEntryProposal) map[string]any {
	lines := make([]map[string]any, 0, len(je.Lines))
	for _, l := range je.Lines {
		line := map[string]any{"amount": l.Amount}
		if l.DebitAccount != "" {
			line["debitAccount"] = l.DebitAccount
		}
		if l.DebitVatCode != nil {
			line["debitVatCode"] = *l.DebitVatCode
		}
		if l.CreditAccount != "" {
			line["creditAccount"] = l.CreditAccount
		}
		if l.CreditVatCode != nil {
			line["creditVatCode"] = *l.CreditVatCode
		}
		lines = append(lines, line)
	}
	entry := map[string]any{
		"description": description,
		"date":        date,
		"lines":       lines,
	}
	return map[string]any{
		"description":    description,
		"open":           je.Open,
		"journalEntries": []map[string]any{entry},
	}
}

// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL). The client-side dry-run Fiken lacks (build-spec §8.2).
// Validation is STRUCTURAL, not authoritative (§9): it catches format/balance/
// plausibility problems before a write touches the books, but never asserts the
// MVA judgment is correct. validAccounts is the set of account codes from the
// synced chart of accounts; an empty set disables the account-existence check.
package fikencore

import (
	"fmt"
	"strings"
	"time"
)

// Severity levels for findings.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// vatLineTolerance is the øre of rounding slack the write path allows on a
// line's VAT before it warns.
const vatLineTolerance = 2

// ValidationFinding is one validation issue.
type ValidationFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// ValidationResult is the outcome of validating a proposal.
type ValidationResult struct {
	OK       bool                `json:"ok"`
	Errors   []ValidationFinding `json:"errors,omitempty"`
	Warnings []ValidationFinding `json:"warnings,omitempty"`
}

func (r *ValidationResult) addError(code, msg string) {
	r.Errors = append(r.Errors, ValidationFinding{SeverityError, code, msg})
}
func (r *ValidationResult) addWarning(code, msg string) {
	r.Warnings = append(r.Warnings, ValidationFinding{SeverityWarning, code, msg})
}

// ValidateProposal runs all structural checks. Errors block a commit (unless
// the operator overrides with --force); warnings are advisory.
func ValidateProposal(p Proposal, validAccounts map[string]bool) ValidationResult {
	var r ValidationResult
	if p.CompanySlug == "" {
		r.addError("missing_company", "proposal has no company_slug")
	}
	if p.SourceSystem == "" || p.SourceID == "" {
		r.addError("missing_idempotency_key", "proposal needs source_system and source_id for the idempotency guard")
	}
	date := p.EffectiveDate()
	if date == "" {
		r.addError("missing_date", "proposal has no date (and no bank_line_date to align to)")
	} else if !validDateRFC(date) {
		r.addError("bad_date", fmt.Sprintf("date %q is not YYYY-MM-DD", date))
	} else {
		checkDateSanity(date, &r)
	}

	switch p.Kind {
	case ProposalKindPurchase:
		validatePurchase(p.Purchase, validAccounts, &r)
	case ProposalKindJournalEntry:
		validateJournalEntry(p.JournalEntry, validAccounts, &r)
	case "":
		r.addError("missing_kind", "proposal has no kind (purchase|journal_entry)")
	default:
		r.addError("bad_kind", fmt.Sprintf("unknown kind %q (want purchase|journal_entry)", p.Kind))
	}

	r.OK = len(r.Errors) == 0
	return r
}

func validatePurchase(pp *PurchaseProposal, validAccounts map[string]bool, r *ValidationResult) {
	if pp == nil {
		r.addError("missing_purchase", "kind=purchase but no purchase body")
		return
	}
	if pp.Kind != "cash_purchase" && pp.Kind != "supplier" {
		r.addError("bad_purchase_kind", fmt.Sprintf("purchase kind %q must be cash_purchase or supplier", pp.Kind))
	}
	if pp.Kind == "supplier" && pp.SupplierID == 0 {
		r.addError("missing_supplier", "kind=supplier requires supplier_id")
	}
	if pp.Currency != "" && len(pp.Currency) != 3 {
		r.addWarning("bad_currency", fmt.Sprintf("currency %q is not a 3-letter ISO code", pp.Currency))
	}
	if len(pp.Lines) == 0 {
		r.addError("no_lines", "purchase has no lines")
		return
	}
	for i, l := range pp.Lines {
		validateLine(i, l, SidePurchases, validAccounts, r)
	}
}

func validateLine(i int, l ProposalLine, side string, validAccounts map[string]bool, r *ValidationResult) {
	where := fmt.Sprintf("line %d", i+1)
	if l.VatType == "" {
		r.addError("missing_vat_type", where+": vatType is required on an order line")
	} else if !VATTypeValidFor(l.VatType, side) {
		if KnownVATType(l.VatType) {
			r.addError("vat_type_wrong_side", fmt.Sprintf("%s: vatType %q is not valid for %s", where, l.VatType, side))
		} else {
			r.addWarning("unknown_vat_type", fmt.Sprintf("%s: vatType %q is not a recognized Fiken type", where, l.VatType))
		}
	}
	if l.NetPrice < 0 {
		r.addError("negative_net", where+": netPrice is negative")
	}
	// The line's vat means something different in each regime, so the only
	// check is the invariant that regime carries (vatLineTolerance øre of
	// rounding slack on the arithmetic ones). A drift on an ordinary domestic
	// line stays a warning; a regime violation — VAT on a basis line, a basis
	// on a direct line — is a coding error and blocks the commit.
	if info, ok := Lookup(l.VatType); ok {
		if inv, kind, diff := LineInvariant(info, l.NetPrice, l.Vat, vatLineTolerance); !inv {
			switch kind {
			case KindVATRate:
				expected, _ := ExpectedLineVAT(info, l.NetPrice)
				r.addWarning("vat_rounding", fmt.Sprintf("%s: vat %d øre deviates from expected %d øre for %s of net %d", where, l.Vat, expected, l.VatType, l.NetPrice))
			case KindBasisHasVAT:
				r.addError(kind, fmt.Sprintf("%s: %s is a basis (grunnlag) type — the line vat must be 0, got %d øre", where, l.VatType, l.Vat))
			case KindZeroRatedHasVAT:
				r.addError(kind, fmt.Sprintf("%s: %s carries no VAT — the line vat must be 0, got %d øre", where, l.VatType, l.Vat))
			case KindDirectHasNet:
				r.addError(kind, fmt.Sprintf("%s: %s posts VAT with no basis — netPrice must be 0, got %d øre", where, l.VatType, l.NetPrice))
			case KindNondeductibleEmbeddedVAT:
				r.addError(kind, fmt.Sprintf("%s: %s expects the embedded VAT %d øre (netPrice is VAT-inclusive), got %d øre — off by %d", where, l.VatType, l.Vat-diff, l.Vat, diff))
			}
		}
	}
	if len(validAccounts) > 0 && l.Account != "" && !accountKnown(l.Account, validAccounts) {
		r.addWarning("unknown_account", fmt.Sprintf("%s: account %q not found in the synced chart of accounts", where, l.Account))
	}
}

func validateJournalEntry(je *JournalEntryProposal, validAccounts map[string]bool, r *ValidationResult) {
	if je == nil {
		r.addError("missing_journal_entry", "kind=journal_entry but no journal_entry body")
		return
	}
	if len(je.Lines) == 0 {
		r.addError("no_lines", "journal entry has no lines")
		return
	}
	for i, l := range je.Lines {
		where := fmt.Sprintf("line %d", i+1)
		if l.DebitAccount == "" || l.CreditAccount == "" {
			r.addError("unbalanced_line", where+": a journal line needs both debit_account and credit_account")
		}
		if l.Amount <= 0 {
			r.addError("nonpositive_amount", where+": amount must be positive")
		}
		if l.DebitVatCode != nil {
			if _, ok := VATCode(*l.DebitVatCode); !ok {
				r.addWarning("unknown_vat_code", fmt.Sprintf("%s: debit_vat_code %d is not a recognized Fiken code", where, *l.DebitVatCode))
			}
		}
		if l.CreditVatCode != nil {
			if _, ok := VATCode(*l.CreditVatCode); !ok {
				r.addWarning("unknown_vat_code", fmt.Sprintf("%s: credit_vat_code %d is not a recognized Fiken code", where, *l.CreditVatCode))
			}
		}
		for _, acct := range []string{l.DebitAccount, l.CreditAccount} {
			if len(validAccounts) > 0 && acct != "" && !accountKnown(acct, validAccounts) {
				r.addWarning("unknown_account", fmt.Sprintf("%s: account %q not found in the synced chart of accounts", where, acct))
			}
		}
	}
}

// accountKnown accepts an exact match or a main-account prefix match (Fiken
// allows posting to a main account "3000" or a sub-account "3000:10001").
func accountKnown(account string, valid map[string]bool) bool {
	if valid[account] {
		return true
	}
	main := account
	if i := strings.IndexByte(account, ':'); i >= 0 {
		main = account[:i]
	}
	for code := range valid {
		if code == main || strings.HasPrefix(code, main+":") {
			return true
		}
	}
	return false
}

func validDateRFC(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func checkDateSanity(date string, r *ValidationResult) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return
	}
	if t.Year() < 2000 {
		r.addWarning("date_far_past", fmt.Sprintf("date %s is before 2000 — likely a mistake", date))
	}
	// >400 days in the future relative to the proposal is suspicious; we cannot
	// know "today" deterministically here, so flag obviously-wrong years.
	if t.Year() > 2100 {
		r.addWarning("date_far_future", fmt.Sprintf("date %s is far in the future — likely a mistake", date))
	}
}

// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL). The Proposal is the shared currency of the write layer
// (prepare -> validate -> commit). It describes a posting to be made — either a
// purchase (kjøp; the Superføring bank-line rendezvous, build-spec §9) or a
// general journal entry (fri postering; corrections/reversals) — independent of
// the Fiken wire format. construct.go turns it into a request body; validate.go
// checks it; commit.go guards + posts it.
package fikencore

// Proposal is a proposed posting plus its provenance (for idempotency + audit).
type Proposal struct {
	CompanySlug string `json:"company_slug"`
	Kind        string `json:"kind"` // ProposalKindPurchase | ProposalKindJournalEntry
	Description string `json:"description,omitempty"`
	Date        string `json:"date,omitempty"` // YYYY-MM-DD; for a purchase this is Fakturadato

	// Provenance / idempotency (build-spec §6.1/§9).
	SourceSystem string `json:"source_system"`
	SourceID     string `json:"source_id"`
	SourceHash   string `json:"source_hash,omitempty"`

	// BankLineDate, when set, is the bank-statement line date the posting must
	// rendezvous with. commit aligns Fakturadato to it (target offset 0, §9).
	BankLineDate string `json:"bank_line_date,omitempty"`

	// Agent context for the audit log.
	Confidence string `json:"confidence,omitempty"` // high|medium|low
	Rationale  string `json:"rationale,omitempty"`

	Purchase     *PurchaseProposal     `json:"purchase,omitempty"`
	JournalEntry *JournalEntryProposal `json:"journal_entry,omitempty"`
}

// Proposal kinds.
const (
	ProposalKindPurchase     = "purchase"
	ProposalKindJournalEntry = "journal_entry"
)

// PurchaseProposal mirrors the fields of a Fiken purchase create request.
type PurchaseProposal struct {
	Kind           string         `json:"kind"` // cash_purchase | supplier
	Paid           bool           `json:"paid"`
	Currency       string         `json:"currency,omitempty"` // default NOK
	SupplierID     int64          `json:"supplier_id,omitempty"`
	PaymentAccount string         `json:"payment_account,omitempty"` // e.g. 1920:10001
	PaymentDate    string         `json:"payment_date,omitempty"`
	Identifier     string         `json:"identifier,omitempty"`
	Lines          []ProposalLine `json:"lines"`
}

// ProposalLine is a purchase/sale order line (amounts in øre).
type ProposalLine struct {
	Description string `json:"description"`
	NetPrice    int64  `json:"net_price"`
	Vat         int64  `json:"vat"`
	Account     string `json:"account"`  // expense account, e.g. 6000 or 6000:10001
	VatType     string `json:"vat_type"` // HIGH, NONE, ...
	ProjectID   int64  `json:"project_id,omitempty"`
}

// JournalEntryProposal is a free posting (fri postering). Each line is a
// self-balancing debit/credit pair, matching Fiken's journalEntry line model.
type JournalEntryProposal struct {
	Open  bool          `json:"open"`
	Lines []JournalLine `json:"lines"`
}

// JournalLine is one debit/credit transfer (amount in øre: net for the debit
// side / gross for the credit side, per Fiken's VAT handling).
type JournalLine struct {
	Amount        int64  `json:"amount"`
	DebitAccount  string `json:"debit_account,omitempty"`
	DebitVatCode  *int   `json:"debit_vat_code,omitempty"`
	CreditAccount string `json:"credit_account,omitempty"`
	CreditVatCode *int   `json:"credit_vat_code,omitempty"`
}

// EffectiveDate returns the date the posting should carry: the bank-line date
// when present (§9 date-alignment, target offset 0), else the proposal's Date.
func (p Proposal) EffectiveDate() string {
	if p.BankLineDate != "" {
		return p.BankLineDate
	}
	return p.Date
}

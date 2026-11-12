// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator stub. A read-compound that
// assembles everything needed to draft a posting from an inbox document
// (build-spec §8.2): the bilag metadata + documentUrl (the agent reads the
// bytes), candidate expense accounts, valid purchase MVA types, and recent
// suppliers with their usual account/VAT. Output is a proposal TEMPLATE the
// agent fills in, then pipes to validate -> commit. Read-only.
package cli

// pp:data-source local

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"fiken-cli/internal/fikencore"
)

type prepareSupplier struct {
	ContactID    int64  `json:"contact_id"`
	Name         string `json:"name"`
	ModalAccount string `json:"modal_account,omitempty"`
	ModalVatType string `json:"modal_vat_type,omitempty"`
	PurchaseN    int    `json:"purchase_count"`
}

type prepareAccount struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// prepareBookedHit is a purchase that already appears to carry the inbox
// document (issue #25): the same file is attached to it, or its invoice
// number is in the document's name. An inbox document stays `unused` when a
// copy of it was attached to a purchase by another route, so the inbox status
// alone does not say whether the receipt is booked.
type prepareBookedHit struct {
	PurchaseID         int64  `json:"purchase_id"`
	Date               string `json:"date"`
	Paid               bool   `json:"paid"`
	Supplier           string `json:"supplier,omitempty"`
	Identifier         string `json:"identifier,omitempty"`
	MatchedOn          string `json:"matched_on"` // attachment_filename | invoice_number
	AttachmentFilename string `json:"attachment_filename,omitempty"`
}

// maxPurchasesPerFilename is where a filename stops identifying a receipt: a
// name carried by more purchases than this ("invoice.pdf", "kvittering.pdf")
// is what a supplier calls every file, and matching on it would report a
// document as booked against purchases it has nothing to do with.
const maxPurchasesPerFilename = 3

// minInvoiceNumberLen keeps a short invoice number ("12") from matching
// inside any document name that happens to contain those characters.
const minInvoiceNumberLen = 5

// inboxAlreadyBooked reports the purchases that already seem to hold the
// inbox document. Two signals, both read from the mirror: a purchase
// attachment with the document's filename, and a purchase whose invoice number
// stands as a whole token in the document's name, filename or description.
// A hint, not proof: the caller opens the purchase before deciding. Pure.
func inboxAlreadyBooked(doc map[string]json.RawMessage, purchases []map[string]json.RawMessage) []prepareBookedHit {
	hits := []prepareBookedHit{}
	if doc == nil {
		return hits
	}
	filename := strings.ToLower(strings.TrimSpace(jsonStr(doc, "filename")))
	tokens := map[string]bool{}
	for _, field := range []string{"name", "filename", "description"} {
		for _, tok := range strings.FieldsFunc(strings.ToLower(jsonStr(doc, field)), func(r rune) bool {
			return r == ' ' || r == '_' || r == '.' || r == ',' || r == ';' || r == '(' || r == ')' || r == '/' || r == '\t'
		}) {
			tokens[tok] = true
		}
	}

	hit := func(p map[string]json.RawMessage, on, attachment string) prepareBookedHit {
		id, _ := jsonInt(p, "purchaseId")
		h := prepareBookedHit{
			PurchaseID: id, Date: jsonStr(p, "date"), Paid: jsonBoolField(p, "paid"),
			Identifier: jsonStr(p, "identifier"), MatchedOn: on, AttachmentFilename: attachment,
		}
		if sup := jsonObject(p, "supplier"); sup != nil {
			h.Supplier = jsonStr(sup, "name")
		}
		return h
	}

	seen := map[int64]bool{}
	var byFilename []prepareBookedHit
	for _, p := range purchases {
		if jsonBoolField(p, "deleted") {
			continue
		}
		id, _ := jsonInt(p, "purchaseId")
		if ident := normIdentifier(jsonStr(p, "identifier")); len(ident) >= minInvoiceNumberLen && tokens[ident] {
			seen[id] = true
			hits = append(hits, hit(p, "invoice_number", ""))
			continue
		}
		if filename == "" {
			continue
		}
		for _, a := range jsonObjects(p, "purchaseAttachments") {
			if strings.ToLower(strings.TrimSpace(jsonStr(a, "filename"))) == filename {
				byFilename = append(byFilename, hit(p, "attachment_filename", jsonStr(a, "filename")))
				break
			}
		}
	}
	if len(byFilename) <= maxPurchasesPerFilename {
		for _, h := range byFilename {
			if !seen[h.PurchaseID] {
				hits = append(hits, h)
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Date != hits[j].Date {
			return hits[i].Date > hits[j].Date
		}
		return hits[i].PurchaseID < hits[j].PurchaseID
	})
	return hits
}

type prepareReport struct {
	Company          string                     `json:"company"`
	InboxDocID       string                     `json:"inbox_document_id,omitempty"`
	InboxDoc         map[string]json.RawMessage `json:"inbox_document,omitempty"`
	DocumentURL      string                     `json:"document_url,omitempty"`
	AlreadyBooked    []prepareBookedHit         `json:"already_booked"`
	CandidateAccount []prepareAccount           `json:"candidate_expense_accounts"`
	PurchaseVatTypes []string                   `json:"purchase_vat_types"`
	RecentSuppliers  []prepareSupplier          `json:"recent_suppliers"`
	ProposalTemplate map[string]any             `json:"proposal_template"`
}

func newNovelPrepareCmd(flags *rootFlags) *cobra.Command {
	var flagCompany, dbPath string

	cmd := &cobra.Command{
		Use:   "prepare [inbox-doc-id]",
		Short: "Assemble the context to draft a posting from an inbox document",
		Long: "Gathers the inbox document (with documentUrl for the agent to read), candidate expense accounts,\n" +
			"valid purchase MVA types, and recent suppliers with their usual account/VAT — then emits a\n" +
			"proposal template to fill in and pipe to validate/commit. `already_booked` lists purchases that\n" +
			"already carry the document (same attachment filename, or their invoice number in its name): an\n" +
			"inbox document can read as unused while a copy of it sits on a purchase. Read-only.",
		Example: strings.Trim(`
  fiken-cli prepare 734083065 --company fiken-demo --agent
  fiken-cli prepare --company fiken-demo --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		Args:        cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunOK(flags) {
				return nil
			}
			db, err := openMirror(cmd.Context(), dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			slug, err := resolveCompanySlug(db, flagCompany)
			if err != nil {
				return err
			}

			rep := prepareReport{Company: slug, PurchaseVatTypes: fikencore.TypesFor(fikencore.SidePurchases)}

			var inboxID string
			if len(args) > 0 {
				inboxID = args[0]
			}
			inbox, err := loadCompanyResources(cmd.Context(), db, "inbox", slug)
			if err != nil {
				return err
			}
			if inboxID != "" {
				rep.InboxDocID = inboxID
				for _, d := range inbox {
					if jsonStr(d, "inboxDocumentId") == inboxID || jsonStr(d, "documentId") == inboxID {
						rep.InboxDoc = d
						rep.DocumentURL = jsonStr(d, "documentUrl")
						break
					}
				}
			}

			accounts, err := loadCompanyResources(cmd.Context(), db, "accounts", slug)
			if err != nil {
				return err
			}
			for _, a := range accounts {
				code := jsonStr(a, "code")
				if isCostAccount(code) {
					rep.CandidateAccount = append(rep.CandidateAccount, prepareAccount{Code: code, Name: jsonStr(a, "name")})
				}
			}
			sort.Slice(rep.CandidateAccount, func(i, j int) bool { return rep.CandidateAccount[i].Code < rep.CandidateAccount[j].Code })

			purchases, err := loadCompanyResources(cmd.Context(), db, "purchases", slug)
			if err != nil {
				return err
			}
			rep.RecentSuppliers = recentSuppliers(purchases)
			rep.AlreadyBooked = inboxAlreadyBooked(rep.InboxDoc, purchases)

			rep.ProposalTemplate = map[string]any{
				"company_slug":   slug,
				"kind":           "purchase",
				"source_system":  "inbox",
				"source_id":      inboxID,
				"bank_line_date": "<YYYY-MM-DD — the bank statement line date>",
				"confidence":     "<high|medium|low>",
				"rationale":      "<why these accounts/VAT>",
				"purchase": map[string]any{
					"kind":        "supplier",
					"paid":        false,
					"currency":    "NOK",
					"supplier_id": 0,
					"lines": []map[string]any{{
						"description": "<line description>",
						"net_price":   0,
						"vat":         0,
						"account":     "<expense account code>",
						"vat_type":    "HIGH",
					}},
				},
			}

			return emitFiken(cmd, flags, rep, func() {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "Posting context for %s\n", slug)
				if rep.DocumentURL != "" {
					fmt.Fprintf(w, "  inbox doc %s — documentUrl: %s\n", rep.InboxDocID, rep.DocumentURL)
				}
				for _, h := range rep.AlreadyBooked {
					state := "unpaid"
					if h.Paid {
						state = "paid"
					}
					fmt.Fprintf(w, "  ALREADY BOOKED? purchase %d (%s, %s, %s) matches on %s\n", h.PurchaseID, h.Date, state, h.Supplier, h.MatchedOn)
				}
				fmt.Fprintf(w, "  %d candidate expense accounts, %d recent suppliers\n", len(rep.CandidateAccount), len(rep.RecentSuppliers))
				fmt.Fprintln(w, "  fill in proposal_template, then: validate | commit --dry-run")
			})
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	return cmd
}

func isCostAccount(code string) bool {
	if code == "" {
		return false
	}
	c := code[0]
	return c >= '4' && c <= '7'
}

func recentSuppliers(purchases []map[string]json.RawMessage) []prepareSupplier {
	type agg struct {
		name     string
		count    int
		accounts []dated
		vatTypes []dated
	}
	by := map[int64]*agg{}
	for _, p := range purchases {
		sup := jsonObject(p, "supplier")
		if sup == nil {
			continue
		}
		cid, _ := jsonInt(sup, "contactId")
		if cid == 0 {
			continue
		}
		a := by[cid]
		if a == nil {
			a = &agg{name: jsonStr(sup, "name")}
			by[cid] = a
		}
		a.count++
		date := jsonStr(p, "date")
		for _, ln := range jsonObjects(p, "lines") {
			a.accounts = append(a.accounts, dated{Date: date, Value: jsonStr(ln, "account")})
			a.vatTypes = append(a.vatTypes, dated{Date: date, Value: jsonStr(ln, "vatType")})
		}
	}
	// Propose the vendor's CURRENT practice: the modal over their last 12
	// months. Only when that window is empty (a vendor not seen for a year, or
	// undated purchases) fall back to the whole-history modal — months <= 0 —
	// so prepare still proposes something for a long-dormant supplier.
	today := nowFunc().Format("2006-01-02")
	modal := func(obs []dated) string {
		v, n := recentModal(obs, today, 12)
		if n == 0 {
			v, _ = recentModal(obs, today, 0)
		}
		return v
	}
	var out []prepareSupplier
	for cid, a := range by {
		out = append(out, prepareSupplier{
			ContactID: cid, Name: a.name, PurchaseN: a.count,
			ModalAccount: modal(a.accounts), ModalVatType: modal(a.vatTypes),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PurchaseN > out[j].PurchaseN })
	if len(out) > 15 {
		out = out[:15]
	}
	return out
}

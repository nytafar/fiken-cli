// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). Lists the postings in a period that need their
// own documentation and have none: purchases and sales without an attachment,
// and journal entries that are not derived from a documented source.
//
// It used to report every journal entry with an empty attachment array, which
// on a two-month term was 214 findings and no real one (issue #28): a payment
// on an invoice, a provider settlement, a salary run and a VAT settlement all
// post an entry with no attachment of their own, because the bilag is on the
// document they belong to or there is none to have. An entry is now classified
// on its transactionType and on whether its transaction is a mirrored purchase
// or sale, never on its description text, and the classes that need no bilag
// are counted per class instead of reported. Reads only the local mirror.
package cli

// pp:data-source local

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// attachmentsField maps a mirror resource type to the field name the Fiken API
// actually uses for its attachment array. The field is NOT called "attachments"
// everywhere: spec.yaml names it purchaseAttachments on purchaseResult (~6647)
// and saleAttachments on saleResult (~6852); only journalEntry (~5941) uses the
// bare "attachments". Reading the wrong key made every purchase look
// undocumented, so keep this table aligned with the spec.
var attachmentsField = map[string]string{
	"purchases":       "purchaseAttachments",
	"sales":           "saleAttachments",
	"journal_entries": "attachments",
}

// hasAttachments reports whether a decoded document of the given mirror
// resource type carries a non-empty attachment array. An unknown resource type
// falls back to the bare "attachments" spelling.
func hasAttachments(resourceType string, m map[string]json.RawMessage) bool {
	field, ok := attachmentsField[resourceType]
	if !ok {
		field = "attachments"
	}
	return len(jsonObjects(m, field)) > 0
}

// journalEntryImpact approximates the size of a journal entry: the sum of its
// positive line amounts, i.e. one side of a balanced entry. Journal lines carry
// a signed amount with no debit/credit direction, so this is the only honest
// magnitude available from the mirror.
func journalEntryImpact(e map[string]json.RawMessage) int64 {
	var sum int64
	for _, ln := range jsonObjects(e, "lines") {
		if amt, _ := jsonInt(ln, "amount"); amt > 0 {
			sum += amt
		}
	}
	return sum
}

// The classes a journal entry can fall in. The first group is reported; the
// rest are counted under Report.Excluded unless --include names them.
const (
	bilagClassFreePosting = "free_posting"        // Fri, Gjeld: posted by hand, needs its own bilag
	bilagClassCustoms     = "customs_declaration" // Tolldeklarasjon: the declaration is the bilag
	bilagClassUnknown     = "unknown_type"        // a transactionType this table does not know

	bilagClassDocument = "document_posting"    // the posting of a purchase, sale or credit note
	bilagClassPayment  = "payment"             // a payment registered on a sale or purchase
	bilagClassProvider = "provider_settlement" // payment-provider settlements and their fees
	bilagClassVAT      = "vat_settlement"      // Mva-oppgjør
	bilagClassSalary   = "salary_and_tax"      // salary runs, employer tax, withholding, tax
	bilagClassBank     = "bank_fee_and_interest"
	bilagClassTransfer = "internal_transfer"
	bilagClassSystem   = "year_end_and_system" // closing entries, opening balance, depreciation, stock count
)

// bilagClassByType maps Fiken's transactionType onto a class. The set is
// closed on Fiken's side; a type missing here lands in unknown_type and is
// reported, so a new type is seen rather than silently excluded.
var bilagClassByType = map[string]string{
	"Fri":             bilagClassFreePosting,
	"Gjeld":           bilagClassFreePosting,
	"Tolldeklarasjon": bilagClassCustoms,

	"Kjøp":           bilagClassDocument,
	"Salg":           bilagClassDocument,
	"Kreditert salg": bilagClassDocument,

	"Betalingsoppgjør": bilagClassPayment,
	"PayPal PoS":       bilagClassProvider,
	"Mva-oppgjør":      bilagClassVAT,

	"Lønn":                           bilagClassSalary,
	"Betaling av arbeidsgiveravgift": bilagClassSalary,
	"Betaling av forskuddstrekk":     bilagClassSalary,
	"Forskuddsskatt":                 bilagClassSalary,
	"Skatteoppgjør":                  bilagClassSalary,

	"Bankomkostning": bilagClassBank,
	"Bankrenter":     bilagClassBank,

	"Intern overføring": bilagClassTransfer,

	"Årsavslutning (egne)":   bilagClassSystem,
	"Årsavslutning (system)": bilagClassSystem,
	"Inngående balanse":      bilagClassSystem,
	"Endring av eiendel":     bilagClassSystem,
	"Varetelling":            bilagClassSystem,
	"Annullering":            bilagClassSystem,
}

// bilagReportedClasses are the classes reported without being asked for.
var bilagReportedClasses = map[string]bool{
	bilagClassFreePosting: true,
	bilagClassCustoms:     true,
	bilagClassUnknown:     true,
}

// bilagClasses lists every class, for --include validation and help.
func bilagClasses() []string {
	seen := map[string]bool{bilagClassUnknown: true}
	for _, c := range bilagClassByType {
		seen[c] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// journalEntryClass classifies one journal entry. The link to a parent wins
// over the type: an entry whose transaction is a mirrored purchase or sale is
// that document's posting or a payment on it, and the document carries the
// bilag. docTx holds the transactionIds of the company's purchases, sales and
// credit notes.
func journalEntryClass(e map[string]json.RawMessage, docTx map[int64]bool) string {
	typ := jsonStr(e, "transactionType")
	class, known := bilagClassByType[typ]
	if txID, ok := jsonInt(e, "transactionId"); ok && docTx[txID] {
		if class == bilagClassPayment {
			return bilagClassPayment
		}
		return bilagClassDocument
	}
	if !known {
		return bilagClassUnknown
	}
	return class
}

// parseBilagInclude resolves --include into the set of classes to report on
// top of the default ones. "all" names every class.
func parseBilagInclude(flag string) (map[string]bool, error) {
	include := map[string]bool{}
	valid := map[string]bool{}
	for _, c := range bilagClasses() {
		valid[c] = true
	}
	for _, part := range strings.Split(flag, ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "":
		case part == "all":
			for c := range valid {
				include[c] = true
			}
		case valid[part]:
			include[part] = true
		default:
			return nil, usageErr(fmt.Errorf("unknown --include class %q (want all, or any of: %s)", part, strings.Join(bilagClasses(), ", ")))
		}
	}
	return include, nil
}

// missingBilagFindings is the pure core: the documents and entries in the
// window that need a bilag and have none, the per-class count of what was
// left out, and the number of undated documents. Pure; no Cobra/DB.
func missingBilagFindings(purchases, sales, creditNotes, entries []map[string]json.RawMessage, win Window, include map[string]bool) (findings []Finding, excluded map[string]int, undated int) {
	excluded = map[string]int{}
	docTx := map[int64]bool{}
	noteTx := func(d map[string]json.RawMessage) {
		if id, ok := jsonInt(d, "transactionId"); ok && id != 0 {
			docTx[id] = true
		}
	}

	document := func(resource, docType, idField, contactField string, d map[string]json.RawMessage, impact int64) {
		noteTx(d)
		if jsonBoolField(d, "deleted") {
			return
		}
		date := jsonStr(d, "date")
		if date == "" {
			undated++
			return
		}
		if !win.Contains(date) || hasAttachments(resource, d) {
			return
		}
		id, _ := jsonInt(d, idField)
		f := Finding{
			Kind:        "missing_attachment",
			Severity:    SeverityInfo,
			DocType:     docType,
			DocID:       id,
			Date:        date,
			Description: jsonStr(d, "kind"),
			ImpactOre:   impact,
		}
		if contact := jsonObject(d, contactField); contact != nil {
			f.ContactID, _ = jsonInt(contact, "contactId")
			f.ContactName = jsonStr(contact, "name")
		}
		if ident := jsonStr(d, "identifier"); ident != "" {
			f.Detail = map[string]any{"identifier": ident}
		}
		findings = append(findings, f)
	}
	for _, p := range purchases {
		document("purchases", "purchase", "purchaseId", "supplier", p, purchaseGross(p))
	}
	for _, sale := range sales {
		net, _ := jsonInt(sale, "netAmount")
		vat, _ := jsonInt(sale, "vatAmount")
		document("sales", "sale", "saleId", "customer", sale, net+vat)
	}
	// A credit note's own PDF is its bilag; only its transaction is needed
	// here, so its posting is recognised as a document's.
	for _, n := range creditNotes {
		if sale := jsonObject(n, "sale"); sale != nil {
			noteTx(sale)
		}
	}

	for _, e := range uniqueJournalEntries(entries) {
		date := jsonStr(e, "date")
		if date == "" {
			undated++
			continue
		}
		if !win.Contains(date) || hasAttachments("journal_entries", e) {
			continue
		}
		class := journalEntryClass(e, docTx)
		if !bilagReportedClasses[class] && !include[class] {
			excluded[class]++
			continue
		}
		id, _ := jsonInt(e, "journalEntryId")
		findings = append(findings, Finding{
			Kind:        "missing_attachment",
			Severity:    SeverityInfo,
			DocType:     "journal_entry",
			DocID:       id,
			Date:        date,
			Description: jsonStr(e, "description"),
			ImpactOre:   journalEntryImpact(e),
			Detail: map[string]any{
				"class":            class,
				"transaction_type": jsonStr(e, "transactionType"),
			},
		})
	}
	return findings, excluded, undated
}

func newNovelMissingBilagCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var dbPath string
	var flagInclude string
	var opts reportOpts

	cmd := &cobra.Command{
		Use:   "missing-bilag",
		Short: "List purchases, sales and free postings with no attached documentation",
		Long: "Reports the postings in a period that need their own bilag and have none: purchases\n" +
			"(supplier and cash) and sales without an attachment, and journal entries that are not\n" +
			"derived from a documented source (free postings, customs declarations, and any\n" +
			"transaction type the command does not know).\n\n" +
			"Journal entries that need no bilag of their own are counted per class under\n" +
			"`excluded` instead of reported: the posting of a purchase or sale and payments on it\n" +
			"(the bilag is on the document), payment-provider settlements, VAT settlement, salary\n" +
			"and tax, bank fees and interest, internal transfers, year-end and system entries.\n" +
			"An entry is classified on its transactionType and on whether its transaction is a\n" +
			"mirrored purchase or sale. --include <class,…|all> reports those classes as well.\n" +
			"Each finding carries the document's amount as its impact. Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli missing-bilag --company fiken-demo
  fiken-cli missing-bilag --company fiken-demo --period 2026-05 --agent
  fiken-cli missing-bilag --company fiken-demo --include payment,bank_fee_and_interest`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			win, err := resolvePeriod(opts.period)
			if err != nil {
				return err
			}
			include, err := parseBilagInclude(flagInclude)
			if err != nil {
				return err
			}
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

			load := func(resource string) ([]map[string]json.RawMessage, error) {
				return loadCompanyResources(cmd.Context(), db, resource, slug)
			}
			purchases, err := load("purchases")
			if err != nil {
				return err
			}
			sales, err := load("sales")
			if err != nil {
				return err
			}
			creditNotes, err := load("credit_notes")
			if err != nil {
				return err
			}
			entries, err := load("journal_entries")
			if err != nil {
				return err
			}
			findings, excluded, undated := missingBilagFindings(purchases, sales, creditNotes, entries, win, include)

			report := Report{
				Company:  slug,
				Detector: "missing_bilag",
				Window:   win,
				Undated:  undated,
				Excluded: excluded,
			}
			keyFn := func(f Finding) map[string]string {
				class, _ := f.Detail["class"].(string)
				return map[string]string{"doc_type": f.DocType, "class": class}
			}
			return finishReport(cmd, flags, report, findings, keyFn, opts)
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	cmd.Flags().StringVar(&flagInclude, "include", "", "Also report these excluded classes (comma-separated, or all): "+strings.Join(bilagClasses(), ", "))
	addReportFlags(cmd, &opts)
	return cmd
}

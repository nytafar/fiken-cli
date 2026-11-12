// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). Reconstructs a VAT-return-shaped view from the
// local mirror. Every line is mapped onto its basis, output VAT and input VAT
// by the regime authority in internal/fikencore rather than by summing the
// line's `vat` field — which is 0 on a basis line, the whole amount on a
// direct line and negative on a nondeductible reverse-charge line. A purchase
// bucket can therefore carry output VAT (reverse charge), and the reported net
// position is total output − total input across both sides. Reads only the
// local mirror.
//
// A credit note is a sale with negative lines that the `sales` resource does
// not list: it lives under `credit_notes`, each row carrying its own `sale`
// (issue #24). Its lines are folded into the sales buckets, so a credited
// invoice reduces the basis and the VAT of the bucket it was sold in.
//
// Buckets are keyed by (vatType, mva_code) — a return is read by code, and the
// five both-sided types carry a different code per side. A second pass
// cross-checks the reverse-charge purchase buckets against the 2702/2712 pair
// Fiken posted in the journal (see transaction_index.go) and reports a
// disagreement as a finding; the summary stays an aggregate report, so those
// findings ride along rather than turning it into a detector Report. A third
// pass compares the whole summary with the movement on the ledger's VAT
// accounts in the same window: a summary that disagrees with the ledger says
// so in a finding instead of exiting clean.
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

type mvaBucket struct {
	VATType      string `json:"vat_type"`
	Regime       string `json:"regime"`
	MVACode      int    `json:"mva_code,omitempty"`
	BasisOre     int64  `json:"basis_ore"`
	Basis        string `json:"basis"`
	OutputVATOre int64  `json:"output_vat_ore"`
	OutputVAT    string `json:"output_vat"`
	InputVATOre  int64  `json:"input_vat_ore"`
	InputVAT     string `json:"input_vat"`
}

// mvaCreditNotes says what the credit notes in the window took off the sales
// side, so the reduction is visible next to the buckets it was folded into.
type mvaCreditNotes struct {
	Count        int    `json:"count"`
	BasisOre     int64  `json:"basis_ore"`
	Basis        string `json:"basis"`
	OutputVATOre int64  `json:"output_vat_ore"`
	OutputVAT    string `json:"output_vat"`
	// WithoutLines counts credit notes in the window that carry no sale
	// lines to bucket. They are reported in a finding, never silently skipped.
	WithoutLines int `json:"without_lines"`
}

// mvaLedger is the ledger side of the whole-summary cross-check: the movement
// on the VAT accounts in the window, VAT settlement entries excluded.
type mvaLedger struct {
	Compared       bool   `json:"compared"`
	JournalEntries int    `json:"journal_entries"`
	OutputVATOre   int64  `json:"output_vat_ore"`
	OutputVAT      string `json:"output_vat"`
	InputVATOre    int64  `json:"input_vat_ore"`
	InputVAT       string `json:"input_vat"`
	NetVATOre      int64  `json:"net_vat_ore"`
	NetVAT         string `json:"net_vat"`
	DiffOre        int64  `json:"diff_ore"`
	Diff           string `json:"diff"`
	// ByType splits the movement by transactionType, so a disagreement can be
	// traced to the kind of posting the summary does not read.
	ByType []mvaLedgerType `json:"by_type"`
}

// mvaLedgerType is the VAT one transactionType moved in the window.
// FromDocuments is true for the types posted from a sale, credit note or
// purchase, which are the ones the summary buckets.
type mvaLedgerType struct {
	TransactionType string `json:"transaction_type"`
	Entries         int    `json:"entries"`
	OutputVATOre    int64  `json:"output_vat_ore"`
	InputVATOre     int64  `json:"input_vat_ore"`
	FromDocuments   bool   `json:"from_documents"`
}

type mvaSummaryReport struct {
	Company             string         `json:"company"`
	Window              Window         `json:"window"`
	UndatedDocuments    int            `json:"undated_documents"`
	TransactionsIndexed int            `json:"transactions_indexed"`
	CreditNotes         mvaCreditNotes `json:"credit_notes"`
	Ledger              mvaLedger      `json:"ledger"`
	Output              []mvaBucket    `json:"output"`
	Input               []mvaBucket    `json:"input"`
	TotalOutputOre      int64          `json:"total_output_vat_ore"`
	TotalOutput         string         `json:"total_output_vat"`
	TotalInputOre       int64          `json:"total_input_vat_ore"`
	TotalInput          string         `json:"total_input_vat"`
	NetVATOre           int64          `json:"net_vat_ore"`
	NetVAT              string         `json:"net_vat"`
	Findings            []Finding      `json:"findings"`
}

// mvaLine is the minimal per-line shape the summarizer needs. TxID is the
// document's transactionId, carried so the reverse-charge cross-check can find
// the journal Fiken posted for it (0 when the document has none).
type mvaLine struct {
	VATType  string
	BasisOre int64 // netPrice
	VATOre   int64 // vat
	TxID     int64
}

// mvaKind is the bucket key: a vatType alone is not enough, because the five
// both-sided types carry a different MVA code per side and a return is read by
// code. Sorting is by vatType then code.
type mvaKey struct {
	vatType string
	code    int
}

// mvaBucketKey resolves the (vatType, mva_code) key and the regime label for
// one line on a given side. An unknown vatType keeps its own key with code 0
// and regime "unknown" — visible rather than silently dropped.
func mvaBucketKey(vatType, side string) (key mvaKey, regime string) {
	info, known := fikencore.Lookup(vatType)
	if !known {
		return mvaKey{vatType: vatType}, "unknown"
	}
	code := info.Code
	if c, ok := fikencore.CodeFor(vatType, side); ok {
		code = c
	}
	return mvaKey{vatType: vatType, code: code}, info.Regime.String()
}

// summarizeMVA buckets one side's lines by (vatType, mva_code) and returns the
// sorted buckets plus the output and input VAT they total to. side is the
// document side the lines came from ("sales" or "purchases"): the five
// both-sided types cannot say from the taxonomy alone whether their VAT is
// output or input, so the document answers that. A vatType the authority does
// not know gets its own bucket with regime "unknown" and the line's raw vat on
// the document's side — visible rather than silently dropped. Pure; no
// Cobra/DB.
func summarizeMVA(lines []mvaLine, side string) (buckets []mvaBucket, outputVATOre, inputVATOre int64) {
	type agg struct {
		regime            string
		basis, output, in int64
	}
	by := map[mvaKey]*agg{}
	for _, ln := range lines {
		key, regime := mvaBucketKey(ln.VATType, side)
		info, _ := fikencore.Lookup(ln.VATType)
		if info.Side == fikencore.SideBoth || info.Side == "" {
			info.Side = side
		}
		basis, output, input := fikencore.ReturnFigures(info, ln.BasisOre, ln.VATOre)
		a := by[key]
		if a == nil {
			a = &agg{regime: regime}
			by[key] = a
		}
		a.basis += basis
		a.output += output
		a.in += input
		outputVATOre += output
		inputVATOre += input
	}
	buckets = make([]mvaBucket, 0, len(by))
	for key, a := range by {
		buckets = append(buckets, mvaBucket{
			VATType:      key.vatType,
			Regime:       a.regime,
			MVACode:      key.code,
			BasisOre:     a.basis,
			Basis:        kr(a.basis),
			OutputVATOre: a.output,
			OutputVAT:    kr(a.output),
			InputVATOre:  a.in,
			InputVAT:     kr(a.in),
		})
	}
	sort.SliceStable(buckets, func(i, j int) bool {
		if buckets[i].VATType != buckets[j].VATType {
			return buckets[i].VATType < buckets[j].VATType
		}
		return buckets[i].MVACode < buckets[j].MVACode
	})
	return buckets, outputVATOre, inputVATOre
}

// mvaCrossCheckToleranceOre is the per-bucket slack on the journal comparison:
// the summary rounds per line and Fiken rounds per posting, so a few øre of
// difference across a bucket is arithmetic, not an error.
const mvaCrossCheckToleranceOre = 5

// mvaKindReverseChargeMismatch is emitted when a reverse-charge bucket's
// computed output VAT disagrees with the 2702/2712 pair Fiken actually posted.
const mvaKindReverseChargeMismatch = "reverse_charge_journal_mismatch"

// isForeignServiceCode reports whether an MVA code is one of the
// purchase-of-services-from-abroad codes (86 high deductible, 87 high
// nondeductible, 88 low deductible, 89 low nondeductible), the only ones
// whose VAT Fiken books on the 2702/2712 pair.
func isForeignServiceCode(code int) bool {
	return code >= 86 && code <= 89
}

// crossCheckReverseCharge compares each purchase bucket for services bought
// from abroad (MVA codes 86-89, the reverse charge Fiken books on 2702/2712)
// against the journal. The summary computes that VAT from the basis and the
// rate; Fiken posts it as a 2702/2712 pair on the purchase's transaction.
// Those two must agree. Import of goods (codes 21-23) is also a basis regime
// but its VAT is settled through customs and booked elsewhere, so it is not
// compared: on a live book every such bucket showed journal 0 against a real
// computed figure, which is not a mismatch but a different account.
//
// A bucket whose purchases have no mirrored transaction at all is not
// reported: nothing was compared, so there is nothing to disagree about. That
// is also what makes an unsynced `transactions` resource silent rather than
// noisy. Pure; no Cobra/DB.
func crossCheckReverseCharge(lines []mvaLine, buckets []mvaBucket, ix *txIndex) []Finding {
	txByKey := map[mvaKey]map[int64]bool{}
	for _, ln := range lines {
		info, known := fikencore.Lookup(ln.VATType)
		if !known || !isForeignServiceCode(info.Code) || ln.TxID == 0 {
			continue
		}
		key, _ := mvaBucketKey(ln.VATType, fikencore.SidePurchases)
		if txByKey[key] == nil {
			txByKey[key] = map[int64]bool{}
		}
		txByKey[key][ln.TxID] = true
	}
	findings := []Finding{}
	for _, b := range buckets {
		ids := txByKey[mvaKey{vatType: b.VATType, code: b.MVACode}]
		if len(ids) == 0 {
			continue
		}
		var journal int64
		matched := 0
		for id := range ids {
			if !ix.has(id) {
				continue
			}
			journal += ix.reverseChargeOutputVAT(id)
			matched++
		}
		if matched == 0 {
			continue
		}
		diff := b.OutputVATOre - journal
		if absInt64(diff) <= mvaCrossCheckToleranceOre {
			continue
		}
		findings = append(findings, Finding{
			Kind:      mvaKindReverseChargeMismatch,
			Severity:  SeverityWarning,
			DocType:   "bucket",
			VATType:   b.VATType,
			ImpactOre: diff,
			Detail: map[string]any{
				"mva_code":              b.MVACode,
				"bucket_output_vat_ore": b.OutputVATOre,
				"journal_vat_ore":       journal,
				"transactions_matched":  matched,
			},
		})
	}
	return findings
}

// creditNoteLines returns the sales-side lines of the credit notes dated in
// the window. A credit note's `sale` holds the negative lines Fiken booked for
// it, with the vatType the original sale used; the note's own `lines` carry a
// percentage but no vatType, so they cannot be bucketed. The sale's date is
// the posting date (the note's issueDate when the sale has none). A note whose
// sale is already among the company's sales is skipped, so a mirror that ever
// lists the two together cannot count the credit twice. Pure; no Cobra/DB.
func creditNoteLines(notes []map[string]json.RawMessage, win Window, saleIDs map[int64]bool) (lines []mvaLine, counted, withoutLines, undated int) {
	for _, n := range notes {
		sale := jsonObject(n, "sale")
		date := jsonStr(sale, "date")
		if date == "" {
			date = jsonStr(n, "issueDate")
		}
		if date == "" {
			undated++
			continue
		}
		if !win.Contains(date) {
			continue
		}
		if id, ok := jsonInt(sale, "saleId"); ok && saleIDs[id] {
			continue
		}
		saleLines := jsonObjects(sale, "lines")
		if len(saleLines) == 0 {
			withoutLines++
			continue
		}
		counted++
		txID, _ := jsonInt(sale, "transactionId")
		// A credit reduces. The API serves the sale negative as a whole, and a
		// note can mix signs line by line (a credited item next to a reversed
		// discount), so the sign is decided once for the note, on its total,
		// and never per line: a note served positive is negated in full.
		var total int64
		for _, ln := range saleLines {
			net, _ := jsonInt(ln, "netPrice")
			vat, _ := jsonInt(ln, "vat")
			total += net + vat
		}
		sign := int64(1)
		if total > 0 {
			sign = -1
		}
		for _, ln := range saleLines {
			net, _ := jsonInt(ln, "netPrice")
			vat, _ := jsonInt(ln, "vat")
			lines = append(lines, mvaLine{VATType: jsonStr(ln, "vatType"), BasisOre: sign * net, VATOre: sign * vat, TxID: txID})
		}
	}
	return lines, counted, withoutLines, undated
}

// The ledger's VAT accounts: 2700-2709 hold output (utgående) VAT as credits
// and 2710-2719 input (inngående) VAT as debits. 2740 is the settlement
// account the term's balance is moved to and is not part of the movement.
const (
	vatOutputAccountFrom = 2700
	vatInputAccountFrom  = 2710
	vatAccountTo         = 2719
)

// vatSettlementType is the transactionType of the entries Fiken posts when a
// return is delivered and when it is paid. The delivery entry empties the VAT
// accounts onto 2740 on the term's last day, so it must be left out or the
// movement of every delivered term sums to zero.
const vatSettlementType = "Mva-oppgjør"

// vatAccountNumber returns the four-digit ledger account of a journal line's
// account code (`2701`, or `2701:12345` with a sub-account), 0 when it has none.
func vatAccountNumber(code string) int {
	if i := strings.IndexByte(code, ':'); i >= 0 {
		code = code[:i]
	}
	if len(code) != 4 {
		return 0
	}
	n := 0
	for _, r := range code {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// ledgerVAT sums the movement on the VAT accounts over the journal entries
// dated in the window. Signs are Fiken's (a credit is negative), so output VAT
// is the negated sum of 2700-2709 and input VAT the sum of 2710-2719; both
// come out positive in the ordinary case and net is output − input, the same
// quantity the summary reports. Pure; no Cobra/DB.
func ledgerVAT(entries []map[string]json.RawMessage, win Window) mvaLedger {
	l := mvaLedger{ByType: []mvaLedgerType{}}
	byType := map[string]*mvaLedgerType{}
	for _, e := range uniqueJournalEntries(entries) {
		typ := jsonStr(e, "transactionType")
		if !win.Contains(jsonStr(e, "date")) || typ == vatSettlementType {
			continue
		}
		l.JournalEntries++
		var out, in int64
		for _, ln := range jsonObjects(e, "lines") {
			amount, _ := jsonInt(ln, "amount")
			switch acct := vatAccountNumber(jsonStr(ln, "account")); {
			case acct >= vatOutputAccountFrom && acct < vatInputAccountFrom:
				out -= amount
			case acct >= vatInputAccountFrom && acct <= vatAccountTo:
				in += amount
			}
		}
		if out == 0 && in == 0 {
			continue
		}
		l.OutputVATOre += out
		l.InputVATOre += in
		t := byType[typ]
		if t == nil {
			t = &mvaLedgerType{TransactionType: typ, FromDocuments: ledgerDocumentTypes[typ]}
			byType[typ] = t
		}
		t.Entries++
		t.OutputVATOre += out
		t.InputVATOre += in
	}
	for _, t := range byType {
		l.ByType = append(l.ByType, *t)
	}
	sort.Slice(l.ByType, func(i, j int) bool { return l.ByType[i].TransactionType < l.ByType[j].TransactionType })
	l.NetVATOre = l.OutputVATOre - l.InputVATOre
	l.Compared = l.JournalEntries > 0
	return l
}

// ledgerDocumentTypes are the transaction types whose VAT the summary can see,
// because they are posted from a sale, a credit note or a purchase. VAT on any
// other type (a free posting, a customs declaration, a point-of-sale
// settlement) reaches the ledger without a document the summary buckets, and
// is the first place to look when the two disagree.
var ledgerDocumentTypes = map[string]bool{
	"Salg":           true,
	"Kreditert salg": true,
	"Kjøp":           true,
}

// uniqueJournalEntries drops repeated copies of one journal entry. A mirror
// that predates the per-company storage key can hold the same entry twice
// (doctor reports it as bare_storage_key), and a ledger summed over both
// copies is exactly double. The copy that carries a transactionType wins,
// since the settlement entries can only be excluded when the type is known.
// An entry without a journalEntryId is kept as it is.
func uniqueJournalEntries(entries []map[string]json.RawMessage) []map[string]json.RawMessage {
	index := map[int64]int{}
	out := make([]map[string]json.RawMessage, 0, len(entries))
	for _, e := range entries {
		id, ok := jsonInt(e, "journalEntryId")
		if !ok || id == 0 {
			out = append(out, e)
			continue
		}
		if i, dup := index[id]; dup {
			if jsonStr(out[i], "transactionType") == "" && jsonStr(e, "transactionType") != "" {
				out[i] = e
			}
			continue
		}
		index[id] = len(out)
		out = append(out, e)
	}
	return out
}

// mvaLedgerToleranceOre is the slack on the whole-summary comparison. The
// return is filed in whole kroner and Fiken rounds per posting where the
// summary rounds per line, so anything under a krone is arithmetic.
const mvaLedgerToleranceOre = 100

// mvaKindLedgerMismatch is emitted when the summary's net VAT disagrees with
// the movement on the ledger's VAT accounts for the same window.
const mvaKindLedgerMismatch = "ledger_vat_mismatch"

// mvaKindCreditNoteWithoutLines is emitted when a credit note in the window
// has no sale lines, so its reduction could not be bucketed.
const mvaKindCreditNoteWithoutLines = "credit_note_without_lines"

// crossCheckLedger compares the summary's totals with the ledger's. Nothing is
// reported when no journal entry was dated in the window: nothing was
// compared, which is also what keeps an unsynced `journal_entries` silent
// rather than a false alarm on every run. Pure; no Cobra/DB.
func crossCheckLedger(totalOut, totalIn int64, ledger *mvaLedger) []Finding {
	ledger.OutputVAT, ledger.InputVAT, ledger.NetVAT = kr(ledger.OutputVATOre), kr(ledger.InputVATOre), kr(ledger.NetVATOre)
	if !ledger.Compared {
		ledger.Diff = kr(0)
		return nil
	}
	ledger.DiffOre = (totalOut - totalIn) - ledger.NetVATOre
	ledger.Diff = kr(ledger.DiffOre)
	if absInt64(ledger.DiffOre) <= mvaLedgerToleranceOre {
		return nil
	}
	return []Finding{{
		Kind:      mvaKindLedgerMismatch,
		Severity:  SeverityWarning,
		DocType:   "summary",
		ImpactOre: ledger.DiffOre,
		Detail: map[string]any{
			"summary_net_vat_ore":    totalOut - totalIn,
			"ledger_net_vat_ore":     ledger.NetVATOre,
			"summary_output_vat_ore": totalOut,
			"ledger_output_vat_ore":  ledger.OutputVATOre,
			"summary_input_vat_ore":  totalIn,
			"ledger_input_vat_ore":   ledger.InputVATOre,
			"note": "the ledger is what the return is filed from; look for documents posted in this window " +
				"that the mirror does not hold (run sync) or postings made directly on a VAT account",
		},
	}}
}

func newNovelMvaSummaryCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var flagPeriod string
	var dbPath string

	cmd := &cobra.Command{
		Use:   "mva-summary",
		Short: "Summarize output/input VAT per VAT type (a VAT-return-shaped view)",
		Long: "Buckets sales, credit note and purchase lines by VAT type, mapping each line onto\n" +
			"the basis, output VAT and input VAT its vatType's regime implies (a reverse-charge\n" +
			"purchase carries both sides; a basis line's VAT is computed, not read off the line),\n" +
			"and reports the net VAT position (total output − total input). Credit notes reduce\n" +
			"the sales bucket their lines belong to. The totals are compared with the movement on\n" +
			"the ledger's VAT accounts (2700-2719) in the same window, and a difference above one\n" +
			"krone is reported as a ledger_vat_mismatch finding.\n" +
			"--period scopes to a term and defaults to the current year; pass `all` to summarize\n" +
			"everything. Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli mva-summary --company fiken-demo --period 2026-Q1
  fiken-cli mva-summary --company fiken-demo --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			win, err := resolvePeriod(flagPeriod)
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

			// A document with no date cannot be placed in any window, so it is
			// excluded always and counted instead of silently vanishing.
			undated := 0
			collect := func(docs []map[string]json.RawMessage) []mvaLine {
				var out []mvaLine
				for _, d := range docs {
					date := jsonStr(d, "date")
					if date == "" {
						undated++
						continue
					}
					if !win.Contains(date) {
						continue
					}
					txID, _ := jsonInt(d, "transactionId")
					for _, ln := range jsonObjects(d, "lines") {
						net, _ := jsonInt(ln, "netPrice")
						vat, _ := jsonInt(ln, "vat")
						out = append(out, mvaLine{
							VATType:  jsonStr(ln, "vatType"),
							BasisOre: net,
							VATOre:   vat,
							TxID:     txID,
						})
					}
				}
				return out
			}

			sales, err := loadCompanyResources(cmd.Context(), db, "sales", slug)
			if err != nil {
				return err
			}
			purchases, err := loadCompanyResources(cmd.Context(), db, "purchases", slug)
			if err != nil {
				return err
			}

			// The journal side of the cross-check. A company that never
			// synced `transactions` gets an empty index, and the
			// cross-check then emits nothing.
			ix, err := loadTxIndex(cmd.Context(), db, slug)
			if err != nil {
				return err
			}

			// Credit notes: negative sales lines the `sales` resource does not
			// carry. Folded into the sales side before bucketing.
			notes, err := loadCompanyResources(cmd.Context(), db, "credit_notes", slug)
			if err != nil {
				return err
			}
			saleIDs := map[int64]bool{}
			for _, sale := range sales {
				if id, ok := jsonInt(sale, "saleId"); ok {
					saleIDs[id] = true
				}
			}
			creditLines, creditCount, creditWithoutLines, creditUndated := creditNoteLines(notes, win, saleIDs)
			undated += creditUndated
			_, creditOut, _ := summarizeMVA(creditLines, fikencore.SideSales)
			var creditBasis int64
			for _, ln := range creditLines {
				creditBasis += ln.BasisOre
			}

			entries, err := loadCompanyResources(cmd.Context(), db, "journal_entries", slug)
			if err != nil {
				return err
			}
			ledger := ledgerVAT(entries, win)

			// A purchase can carry output VAT (reverse charge), so the totals
			// are taken across both sides, not one per side.
			purchLines := collect(purchases)
			salesBuckets, salesOut, salesIn := summarizeMVA(append(collect(sales), creditLines...), fikencore.SideSales)
			purchBuckets, purchOut, purchIn := summarizeMVA(purchLines, fikencore.SidePurchases)
			totalOut := salesOut + purchOut
			totalIn := salesIn + purchIn
			net := totalOut - totalIn
			findings := crossCheckReverseCharge(purchLines, purchBuckets, ix)
			findings = append(findings, crossCheckLedger(totalOut, totalIn, &ledger)...)
			if creditWithoutLines > 0 {
				findings = append(findings, Finding{
					Kind:     mvaKindCreditNoteWithoutLines,
					Severity: SeverityWarning,
					DocType:  "credit_note",
					Detail: map[string]any{
						"count": creditWithoutLines,
						"note":  "credit notes in the window carry no sale lines and were not subtracted; run sync --full --resources credit_notes",
					},
				})
			}
			report := mvaSummaryReport{
				Company:             slug,
				Window:              win,
				UndatedDocuments:    undated,
				TransactionsIndexed: ix.count(),
				CreditNotes: mvaCreditNotes{
					Count: creditCount, BasisOre: creditBasis, Basis: kr(creditBasis),
					OutputVATOre: creditOut, OutputVAT: kr(creditOut), WithoutLines: creditWithoutLines,
				},
				Ledger:         ledger,
				Output:         salesBuckets,
				Input:          purchBuckets,
				TotalOutputOre: totalOut,
				TotalOutput:    kr(totalOut),
				TotalInputOre:  totalIn,
				TotalInput:     kr(totalIn),
				NetVATOre:      net,
				NetVAT:         kr(net),
				Findings:       findings,
			}
			return emitFiken(cmd, flags, report, func() {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "MVA summary for %s (%s)\n", report.Company, report.Window.String())
				fmt.Fprintf(w, "Output VAT %s kr, input VAT %s kr — net (output − input) %s kr",
					report.TotalOutput, report.TotalInput, report.NetVAT)
				if report.UndatedDocuments > 0 {
					fmt.Fprintf(w, ", %d undated document(s) skipped", report.UndatedDocuments)
				}
				fmt.Fprintf(w, "\n\n")
				printBuckets := func(title string, bs []mvaBucket) {
					fmt.Fprintf(w, "%s:\n", title)
					if len(bs) == 0 {
						fmt.Fprintln(w, "  (none)")
						return
					}
					for _, b := range bs {
						code := ""
						if b.MVACode != 0 {
							code = fmt.Sprintf("kode %d", b.MVACode)
						}
						fmt.Fprintf(w, "  %-55s %-22s %-8s basis %14s kr   utg %12s kr   inng %12s kr\n",
							b.VATType, b.Regime, code, b.Basis, b.OutputVAT, b.InputVAT)
					}
				}
				printBuckets("Sales lines (credit notes subtracted)", report.Output)
				if cn := report.CreditNotes; cn.Count > 0 || cn.WithoutLines > 0 {
					fmt.Fprintf(w, "  of which %d credit note(s): basis %s kr, utg %s kr", cn.Count, cn.Basis, cn.OutputVAT)
					if cn.WithoutLines > 0 {
						fmt.Fprintf(w, "; %d more had no lines and were NOT subtracted", cn.WithoutLines)
					}
					fmt.Fprintln(w)
				}
				fmt.Fprintln(w)
				printBuckets("Purchase lines", report.Input)
				fmt.Fprintln(w)
				switch l := report.Ledger; {
				case !l.Compared:
					fmt.Fprintln(w, "Ledger check: no journal entries in this window, nothing compared")
				case absInt64(l.DiffOre) <= mvaLedgerToleranceOre:
					fmt.Fprintf(w, "Ledger check: agrees with accounts 2700-2719 (net %s kr over %d journal entries)\n", l.NetVAT, l.JournalEntries)
				default:
					fmt.Fprintf(w, "Ledger check: DISAGREES with accounts 2700-2719. Summary net %s kr, ledger net %s kr, diff %s kr\n",
						report.NetVAT, l.NetVAT, l.Diff)
					fmt.Fprintf(w, "  ledger utg %s kr, inng %s kr. The ledger is what the return is filed from.\n", l.OutputVAT, l.InputVAT)
					for _, t := range l.ByType {
						if t.FromDocuments {
							continue
						}
						name := t.TransactionType
						if name == "" {
							name = "(no transaction type)"
						}
						fmt.Fprintf(w, "  not from a sale or purchase: %-24s %4d entries   utg %12s kr   inng %12s kr\n",
							name, t.Entries, kr(t.OutputVATOre), kr(t.InputVATOre))
					}
				}
				var reverse []Finding
				for _, f := range report.Findings {
					if f.Kind == mvaKindReverseChargeMismatch {
						reverse = append(reverse, f)
					}
				}
				if len(reverse) > 0 {
					ore := func(f Finding, key string) string {
						v, _ := f.Detail[key].(int64)
						return kr(v)
					}
					fmt.Fprintf(w, "\nJournal cross-check (%d):\n", len(reverse))
					for _, f := range reverse {
						fmt.Fprintf(w, "  %-55s kode %-4v computed utg %12s kr   journal %12s kr   diff %12s kr (%v transaction(s))\n",
							f.VATType, f.Detail["mva_code"],
							ore(f, "bucket_output_vat_ore"), ore(f, "journal_vat_ore"),
							kr(f.ImpactOre), f.Detail["transactions_matched"])
					}
				}
			})
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&flagPeriod, "period", "", "Period: YYYY, YYYY-MM, YYYY-Qn, from:to, or all (default: current year)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	return cmd
}

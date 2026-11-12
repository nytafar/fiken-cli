// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The duplicate guard on a purchase create (issue #25). Fiken accepts a second
// purchase with the same supplier and invoice number without a word, and
// nothing on this side knew better: `commit` dedupes against its own
// idempotency map, which has never heard of a purchase made in the web UI, by
// `purchases create`, or from another machine. Two receipts still listed as
// unused in the inbox turned out to be booked already, and only a lookup by
// hand kept them from being booked twice.
//
// The guard asks one question before the POST: does this company already hold
// a purchase with this supplier and this invoice number (`identifier`)? It
// reads two places, because neither is enough alone:
//
//   - the live API, for the purchases dated within duplicateWindowDays of the
//     new one — current, and it sees a purchase made a minute ago elsewhere;
//   - the local mirror, for every date — free, and it sees the invoice booked
//     last year under a different date. A mirror hit is confirmed live before
//     it counts, since the mirror may still hold a purchase deleted since.
//
// A hit refuses the create with exit code 9 and names the existing purchase,
// its date and whether it is paid. `--idempotent` turns the refusal into a
// successful no-op that reports the existing id; `--allow-duplicate` skips the
// check. A purchase with no invoice number cannot be checked and is let
// through. A lookup that fails refuses too: creating blind is the silent
// failure this guard exists to remove.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"fiken-cli/internal/client"
	"fiken-cli/internal/cliutil"
)

// duplicateExitCode is the process exit code for a refused duplicate.
// Distinct from api(5) and the write guard(8): the request was never sent,
// and the reason is in the books, not in the configuration.
const duplicateExitCode = 9

// duplicateWindowDays is how far either side of the new purchase's date the
// live lookup reaches. A quarter covers the usual ways one invoice gets two
// dates (invoice date against payment date, a reminder, a late receipt);
// anything older is the mirror's to find.
const duplicateWindowDays = 92

const allowDuplicateFlag = "allow-duplicate"

// existingPurchase is what a refusal names.
type existingPurchase struct {
	PurchaseID int64  `json:"purchaseId"`
	Date       string `json:"date"`
	Paid       bool   `json:"paid"`
	Identifier string `json:"identifier"`
	SupplierID int64  `json:"supplierId,omitempty"`
	Supplier   string `json:"supplier,omitempty"`
	Source     string `json:"source"` // live | mirror
}

// addPurchaseDuplicateFlags registers the override on a command that creates
// a purchase. `--idempotent` is a root flag and needs no registration.
func addPurchaseDuplicateFlags(cmd *cobra.Command) {
	cmd.Flags().Bool(allowDuplicateFlag, false,
		"Create the purchase even when the supplier already has one with this invoice number (identifier)")
}

// normIdentifier is the comparison form of an invoice number: surrounding
// space dropped and case folded, so "inv-1001 " and "INV-1001" are one number.
// Nothing else is normalized: leading zeros and separators are significant on
// real invoices.
func normIdentifier(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// anyInt64 reads an id out of a request body value, which is a float64 when
// the body came from JSON, a string when it came from a flag and an integer
// when a proposal built it.
func anyInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i
	}
	return 0
}

// matchExistingPurchases returns the candidates that carry the same supplier
// and invoice number. A candidate marked deleted is not a purchase any more.
// A cash purchase has no supplier, so two purchases with no supplier and the
// same number match each other, and never a supplier purchase. Pure.
func matchExistingPurchases(candidates []map[string]json.RawMessage, supplierID int64, identifier, source string) []existingPurchase {
	want := normIdentifier(identifier)
	if want == "" {
		return nil
	}
	var out []existingPurchase
	for _, p := range candidates {
		if jsonBoolField(p, "deleted") || normIdentifier(jsonStr(p, "identifier")) != want {
			continue
		}
		var sid int64
		var name string
		if sup := jsonObject(p, "supplier"); sup != nil {
			sid, _ = jsonInt(sup, "contactId")
			name = jsonStr(sup, "name")
		}
		if sid != supplierID {
			continue
		}
		id, _ := jsonInt(p, "purchaseId")
		out = append(out, existingPurchase{
			PurchaseID: id,
			Date:       jsonStr(p, "date"),
			Paid:       jsonBoolField(p, "paid"),
			Identifier: jsonStr(p, "identifier"),
			SupplierID: sid,
			Supplier:   name,
			Source:     source,
		})
	}
	return out
}

// mergeExisting unions hits by purchaseId, the first source winning, ordered
// by date then id so the message and the idempotent id are reproducible.
func mergeExisting(groups ...[]existingPurchase) []existingPurchase {
	seen := map[int64]bool{}
	var out []existingPurchase
	for _, g := range groups {
		for _, e := range g {
			if seen[e.PurchaseID] {
				continue
			}
			seen[e.PurchaseID] = true
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].PurchaseID < out[j].PurchaseID
	})
	return out
}

// livePurchasesAround lists the company's purchases dated within
// duplicateWindowDays of date, straight from the API.
func livePurchasesAround(ctx context.Context, c *client.Client, slug, date string) ([]map[string]json.RawMessage, error) {
	params := map[string]string{"pageSize": "100"}
	if t, err := time.Parse("2006-01-02", date); err == nil {
		params["dateGe"] = t.AddDate(0, 0, -duplicateWindowDays).Format("2006-01-02")
		params["dateLe"] = t.AddDate(0, 0, duplicateWindowDays).Format("2006-01-02")
	}
	var all []map[string]json.RawMessage
	for page := 0; page < 200; page++ {
		params["page"] = strconv.Itoa(page)
		raw, err := c.GetNoCache(ctx, "/companies/"+slug+"/purchases", params)
		if err != nil {
			return nil, err
		}
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("purchases list is not an array: %w", err)
		}
		all = append(all, items...)
		if len(items) < 100 {
			break
		}
	}
	return all, nil
}

// confirmMirrorHits re-reads each mirror hit from the API and keeps the ones
// that still exist and still match. The mirror is a copy: a purchase deleted
// in Fiken since the last full sync must not block a legitimate create.
func confirmMirrorHits(ctx context.Context, c *client.Client, slug string, hits []existingPurchase, supplierID int64, identifier string) ([]existingPurchase, error) {
	var out []existingPurchase
	for _, h := range hits {
		raw, err := c.GetNoCache(ctx, fmt.Sprintf("/companies/%s/purchases/%d", slug, h.PurchaseID), nil)
		if err != nil {
			var apiErr *client.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
				continue
			}
			return nil, err
		}
		var p map[string]json.RawMessage
		if json.Unmarshal(raw, &p) != nil {
			continue
		}
		out = append(out, matchExistingPurchases([]map[string]json.RawMessage{p}, supplierID, identifier, "mirror")...)
	}
	return out, nil
}

// findExistingPurchases is the lookup behind the guard: live window plus
// confirmed mirror hits.
func findExistingPurchases(ctx context.Context, c *client.Client, slug string, body map[string]any) ([]existingPurchase, error) {
	identifier, _ := body["identifier"].(string)
	supplierID := anyInt64(body["supplierId"])
	date, _ := body["date"].(string)

	live, err := livePurchasesAround(ctx, c, slug, date)
	if err != nil {
		return nil, err
	}
	hits := matchExistingPurchases(live, supplierID, identifier, "live")

	var mirrorHits []existingPurchase
	if db, derr := openMirror(ctx, ""); derr == nil {
		if rows, lerr := loadCompanyResources(ctx, db, "purchases", slug); lerr == nil {
			mirrorHits = matchExistingPurchases(rows, supplierID, identifier, "mirror")
		}
		db.Close()
	}
	// A mirror hit the live window already returned needs no second read.
	known := map[int64]bool{}
	for _, h := range hits {
		known[h.PurchaseID] = true
	}
	var unconfirmed []existingPurchase
	for _, h := range mirrorHits {
		if !known[h.PurchaseID] {
			unconfirmed = append(unconfirmed, h)
		}
	}
	confirmed, err := confirmMirrorHits(ctx, c, slug, unconfirmed, supplierID, identifier)
	if err != nil {
		return nil, err
	}
	return mergeExisting(hits, confirmed), nil
}

// describeExisting renders the hits for the refusal message.
func describeExisting(hits []existingPurchase) string {
	parts := make([]string, 0, len(hits))
	for _, h := range hits {
		state := "unpaid"
		if h.Paid {
			state = "paid"
		}
		parts = append(parts, fmt.Sprintf("purchase %d (date %s, %s)", h.PurchaseID, h.Date, state))
	}
	return strings.Join(parts, ", ")
}

// duplicateVerdict is the guard's decision, separate from how it is printed so
// `purchases create` and `commit` can each speak their own envelope.
type duplicateVerdict struct {
	Checked  bool               // a lookup ran
	Existing []existingPurchase // what it found
	Skipped  string             // why no lookup ran: allow_duplicate | no_identifier | verify
}

// checkDuplicatePurchase runs the lookup unless there is nothing to look up.
// The read client is never a dry-run client: the lookup is a pair of GETs, and
// a dry run that cannot say "this would be refused" previews the wrong thing.
func checkDuplicatePurchase(ctx context.Context, flags *rootFlags, slug string, body map[string]any, allowDuplicate bool) (duplicateVerdict, error) {
	if allowDuplicate {
		return duplicateVerdict{Skipped: "allow_duplicate"}, nil
	}
	if identifier, _ := body["identifier"].(string); normIdentifier(identifier) == "" {
		return duplicateVerdict{Skipped: "no_identifier"}, nil
	}
	if cliutil.IsVerifyEnv() {
		return duplicateVerdict{Skipped: "verify"}, nil
	}
	c, err := flags.newClient()
	if err != nil {
		return duplicateVerdict{}, err
	}
	c.DryRun = false
	hits, err := findExistingPurchases(ctx, c, slug, body)
	if err != nil {
		return duplicateVerdict{}, fmt.Errorf("could not check whether this purchase already exists: %w\n"+
			"hint: nothing was created. Fix the read, or pass --%s to create without the check", err, allowDuplicateFlag)
	}
	return duplicateVerdict{Checked: true, Existing: hits}, nil
}

// duplicateError is the refusal both commands return.
func duplicateError(body map[string]any, hits []existingPurchase) error {
	identifier, _ := body["identifier"].(string)
	who := "with no supplier"
	if sid := anyInt64(body["supplierId"]); sid != 0 {
		who = fmt.Sprintf("from supplier %d", sid)
	}
	return &cliError{code: duplicateExitCode, err: fmt.Errorf(
		"invoice number %q %s is already booked: %s\n"+
			"hint: nothing was created. Paid: the purchase is done. Unpaid: register the payment on it.\n"+
			"      Pass --idempotent to treat this as done, or --%s to create a second purchase anyway",
		identifier, who, describeExisting(hits), allowDuplicateFlag)}
}

// guardDuplicatePurchase is the one call the generated `purchases create`
// makes before its POST. done=true means the command is finished (an
// idempotent no-op was printed); a non-nil error is the refusal.
func guardDuplicatePurchase(cmd *cobra.Command, flags *rootFlags, slug, path string, body map[string]any) (done bool, err error) {
	allow, _ := cmd.Flags().GetBool(allowDuplicateFlag)
	verdict, err := checkDuplicatePurchase(cmd.Context(), flags, slug, body, allow)
	if err != nil {
		if flags.dryRun {
			// A preview still previews; the real run will refuse.
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
			return false, nil
		}
		return false, &cliError{code: duplicateExitCode, err: err}
	}
	if len(verdict.Existing) == 0 {
		return false, nil
	}
	if flags.idempotent {
		first := verdict.Existing[0]
		if flags.asJSON || !isTerminal(cmd.OutOrStdout()) {
			return true, printOutput(cmd.OutOrStdout(), mustJSON(map[string]any{
				"action":   "post",
				"resource": "purchases",
				"path":     path,
				"status":   200,
				"success":  true,
				"noop":     true,
				"reason":   "already_exists",
				"id":       first.PurchaseID,
				"existing": verdict.Existing,
			}), true)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "already booked (no-op): %s\n", describeExisting(verdict.Existing))
		return true, nil
	}
	refusal := duplicateError(body, verdict.Existing)
	if flags.asJSON || !isTerminal(cmd.OutOrStdout()) {
		_ = printOutput(cmd.OutOrStdout(), mustJSON(map[string]any{
			"action":   "post",
			"resource": "purchases",
			"path":     path,
			"success":  false,
			"reason":   "duplicate_purchase",
			"code":     duplicateExitCode,
			"error":    strings.SplitN(refusal.Error(), "\n", 2)[0],
			"existing": verdict.Existing,
		}), true)
	}
	return false, refusal
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

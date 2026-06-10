// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator stub. Registers a payment on
// a sale or purchase (and optionally marks a sale settled), recording it in the
// audit log (build-spec §8.2). --dry-run previews the payment request; a real
// registration requires --yes (or --agent). Amounts are integer øre.
package cli

// pp:data-source live

import (
	"fmt"
	"strings"

	"fiken-cli/internal/cliutil"
	"fiken-cli/internal/fikencore"

	"github.com/spf13/cobra"
)

func newNovelReconcileCmd(flags *rootFlags) *cobra.Command {
	var saleID, purchaseID, account, date, currency, company, corePath string
	var amount, fee int64

	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Register a payment on a sale or purchase (idempotency-light, audited)",
		Long: "Registers a payment against a sale (--sale) or purchase (--purchase): posts {date, account, amount}\n" +
			"to the payments sub-resource and logs payment.registered. --dry-run previews; a real registration\n" +
			"requires --yes (or --agent). Amounts are integer øre (34000 = 340.00).",
		Example: strings.Trim(`
  fiken-cli reconcile --sale 2888156 --amount 34000 --account 1920:10001 --dry-run
  fiken-cli reconcile --purchase 99 --amount 12500 --account 1920:10001 --date 2026-05-20 --yes`, "\n"),
		Annotations:  map[string]string{"mcp:read-only": "false", "pp:typed-exit-codes": "0,2,5"},
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (saleID == "") == (purchaseID == "") {
				return &cliError{code: 2, err: fmt.Errorf("specify exactly one of --sale or --purchase")}
			}
			seg, id := "sales", saleID
			if purchaseID != "" {
				seg, id = "purchases", purchaseID
			}
			if currency == "" {
				currency = "NOK"
			}

			slug := strings.TrimSpace(company)
			if slug == "" {
				if db, derr := openMirror(cmd.Context(), ""); derr == nil {
					slug, _ = resolveCompanySlug(db, "")
					db.Close()
				}
			}

			body := map[string]any{"account": account, "amount": amount, "currency": currency}
			if date != "" {
				body["date"] = date
			}
			if fee != 0 {
				body["fee"] = fee
			}
			path := ""
			if slug != "" {
				path = fmt.Sprintf("/companies/%s/%s/%s/payments", slug, seg, id)
			}

			if flags.dryRun || cliutil.IsVerifyEnv() {
				return printJSONFiltered(cmd.OutOrStdout(), map[string]any{
					"would_register_payment": true, "company": slug, "path": path, "payment": body,
				}, flags)
			}
			if !flags.yes && !flags.agent {
				return &cliError{code: 2, err: fmt.Errorf("reconcile mutates real books — re-run with --dry-run, or --yes to confirm")}
			}
			if slug == "" {
				return &cliError{code: 2, err: fmt.Errorf("--company is required (could not resolve a single synced company)")}
			}
			if account == "" || amount <= 0 || date == "" {
				return &cliError{code: 2, err: fmt.Errorf("--account, a positive --amount, and --date are required for a real payment")}
			}

			rid := fikenRequestID()
			c, err := flags.newClient()
			if err != nil {
				return err
			}
			resp, status, err := c.PostWithHeaders(cmd.Context(), path, body, map[string]string{"X-Request-ID": rid})

			core, cerr := fikencore.Open(cmd.Context(), corePath)
			if cerr == nil {
				defer core.Close()
			}
			logPayment := func(result, rj string) string {
				if cerr != nil {
					return ""
				}
				eid, _ := core.LogEvent(cmd.Context(), fikencore.Event{
					Surface: "api", Operation: fikencore.OpPaymentRegistered, CompanySlug: slug,
					TargetEntityType: strings.TrimSuffix(seg, "s"), TargetEntityID: id, XRequestID: rid,
					Result: result, ResultJSON: rj,
				})
				return eid
			}
			if err != nil || status < 200 || status >= 300 {
				logPayment("failure", fmt.Sprintf(`{"status":%d}`, status))
				if err != nil {
					return &cliError{code: 5, err: fmt.Errorf("payment POST failed: %w", err)}
				}
				return &cliError{code: 5, err: fmt.Errorf("payment returned HTTP %d: %s", status, fikenTruncate(string(resp), 300))}
			}
			eventID := logPayment("success", "")
			return printJSONFiltered(cmd.OutOrStdout(), map[string]any{
				"payment_registered": true, "target": seg + "/" + id, "amount_ore": amount, "amount": kr(amount),
				"account": account, "x_request_id": rid, "event_id": eventID,
			}, flags)
		},
	}
	cmd.Flags().StringVar(&saleID, "sale", "", "Sale id to register a payment against")
	cmd.Flags().StringVar(&purchaseID, "purchase", "", "Purchase id to register a payment against")
	cmd.Flags().Int64Var(&amount, "amount", 0, "Payment amount in øre (34000 = 340.00)")
	cmd.Flags().StringVar(&account, "account", "", "Bank account code the payment hits (e.g. 1920:10001)")
	cmd.Flags().StringVar(&date, "date", "", "Payment date YYYY-MM-DD (required for a real payment)")
	cmd.Flags().StringVar(&currency, "currency", "NOK", "ISO 4217 currency code")
	cmd.Flags().Int64Var(&fee, "fee", 0, "Optional fee in øre")
	cmd.Flags().StringVar(&company, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&corePath, "core-db", "", "Core DB path (audit log)")
	return cmd
}

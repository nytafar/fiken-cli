// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator stub. Reverses a posting via
// Fiken's soft-delete (which creates a balancing reverse transaction and sets
// deleted=true), recording the reversal in the audit log with a link back to
// the original event (build-spec §8.2). --dry-run previews; a real reversal
// requires --yes (or --agent). The delete needs a required reason.
package cli

// pp:data-source live

import (
	"fmt"
	"net/url"
	"strings"

	"fiken-cli/internal/cliutil"
	"fiken-cli/internal/fikencore"

	"github.com/spf13/cobra"
)

func newNovelReverseCmd(flags *rootFlags) *cobra.Command {
	var flagID, flagType, flagCompany, reason, reversesEvent, dbPath, corePath string

	cmd := &cobra.Command{
		Use:   "reverse [fiken-id]",
		Short: "Reverse a posting (soft-delete) with an audit link to the original",
		Long: "Soft-deletes a purchase, sale, or transaction in Fiken — which creates a balancing reverse\n" +
			"transaction — and logs posting.reversed with reverses_event_id. --dry-run previews; a real\n" +
			"reversal requires --yes (or --agent) and a --reason.",
		Example: strings.Trim(`
  fiken-cli reverse 734083065 --type transaction --company fiken-demo --dry-run
  fiken-cli reverse 2888156 --type purchase --company fiken-demo --reason "duplicate" --yes`, "\n"),
		Annotations:  map[string]string{"mcp:read-only": "false", "pp:typed-exit-codes": "0,2,5"},
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := flagID
			if id == "" && len(args) > 0 {
				id = args[0]
			}
			if flagType == "" {
				flagType = "purchase"
			}
			seg, ok := map[string]string{"purchase": "purchases", "sale": "sales", "transaction": "transactions"}[flagType]
			if !ok {
				return &cliError{code: 2, err: fmt.Errorf("--type must be purchase|sale|transaction, got %q", flagType)}
			}
			if id == "" {
				if dryRunOK(flags) {
					return nil // verify probe with no id
				}
				return cmd.Help()
			}

			slug := strings.TrimSpace(flagCompany)
			if slug == "" {
				if db, derr := openMirror(cmd.Context(), dbPath); derr == nil {
					slug, _ = resolveCompanySlug(db, "")
					db.Close()
				}
			}
			if slug == "" {
				return &cliError{code: 2, err: fmt.Errorf("--company is required (could not resolve a single synced company)")}
			}

			path := fmt.Sprintf("/companies/%s/%s/%s/delete", slug, seg, id)

			if flags.dryRun || cliutil.IsVerifyEnv() {
				return printJSONFiltered(cmd.OutOrStdout(), map[string]any{
					"would_reverse": true, "type": flagType, "id": id, "company": slug,
					"path": path, "reason": reason,
				}, flags)
			}
			if !flags.yes && !flags.agent {
				return &cliError{code: 2, err: fmt.Errorf("reverse mutates real books — re-run with --dry-run, or --yes to confirm")}
			}
			if strings.TrimSpace(reason) == "" {
				return &cliError{code: 2, err: fmt.Errorf("--reason is required to reverse (Fiken records it on the reverse transaction)")}
			}

			rid := fikenRequestID()
			c, err := flags.newClient()
			if err != nil {
				return err
			}
			full := path + "?description=" + url.QueryEscape(reason)
			resp, status, err := c.PostWithHeaders(cmd.Context(), full, nil, map[string]string{"X-Request-ID": rid})

			core, cerr := fikencore.Open(cmd.Context(), corePath)
			logReversal := func(result, resultJSON string) string {
				if cerr != nil {
					return ""
				}
				eid, _ := core.LogEvent(cmd.Context(), fikencore.Event{
					Surface: "api", Operation: fikencore.OpPostingReversed, CompanySlug: slug,
					TargetEntityType: flagType, TargetEntityID: id, XRequestID: rid,
					Rationale: reason, Result: result, ResultJSON: resultJSON, ReversesEventID: reversesEvent,
				})
				return eid
			}
			if cerr == nil {
				defer core.Close()
			}

			if err != nil || status < 200 || status >= 300 {
				logReversal("failure", fmt.Sprintf(`{"status":%d}`, status))
				if err != nil {
					return &cliError{code: 5, err: fmt.Errorf("reverse POST failed: %w", err)}
				}
				return &cliError{code: 5, err: fmt.Errorf("reverse returned HTTP %d: %s", status, fikenTruncate(string(resp), 300))}
			}
			eventID := logReversal("success", "")
			return printJSONFiltered(cmd.OutOrStdout(), map[string]any{
				"reversed": true, "type": flagType, "id": id, "x_request_id": rid, "event_id": eventID,
			}, flags)
		},
	}
	cmd.Flags().StringVar(&flagID, "id", "", "Fiken entity id to reverse (or pass as a positional arg)")
	cmd.Flags().StringVar(&flagType, "type", "purchase", "Entity type: purchase|sale|transaction")
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the reversal (required for a real reverse)")
	cmd.Flags().StringVar(&reversesEvent, "reverses-event", "", "Audit event id this reversal undoes")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (for company resolution)")
	cmd.Flags().StringVar(&corePath, "core-db", "", "Core DB path (audit log)")
	return cmd
}

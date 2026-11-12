// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator stub. The gated, audited,
// idempotent write (build-spec §6/§8.2/§9). Flow: load proposal -> validate ->
// idempotency check (no-op if already committed) -> construct request
// (Fakturadato aligned to the bank-line date, §9) -> POST through the generated
// client (single-flight lock + X-Request-ID) -> record the Fiken id and log
// posting.committed in the same call. --dry-run (and verify mode) print the
// request without writing; a real write requires --yes (or --agent).
package cli

// pp:data-source live

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"fiken-cli/internal/client"
	"fiken-cli/internal/cliutil"
	"fiken-cli/internal/fikencore"

	"github.com/spf13/cobra"
)

func newNovelCommitCmd(flags *rootFlags) *cobra.Command {
	var flagFile, flagCompany, dbPath, corePath string
	var force, skipValidate, allowDuplicate bool

	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Commit a validated posting to Fiken (idempotent, audited, date-aligned)",
		Long: "Posts a purchase or general journal entry from a proposal (--file or stdin). Deduped against\n" +
			"the idempotency map (a repeat of the same source no-ops), date-aligned to the bank-line date,\n" +
			"and recorded in the append-only audit log in the same call. A purchase whose supplier already\n" +
			"has the same invoice number in Fiken is refused with exit code 9 and the existing purchase is\n" +
			"named; --idempotent reports it as done, --allow-duplicate posts anyway. --dry-run previews the\n" +
			"exact request; a real write requires --yes (or --agent).",
		Example: strings.Trim(`
  fiken-cli commit --file proposal.json --dry-run
  fiken-cli commit --file proposal.json --yes`, "\n"),
		Annotations:  map[string]string{"mcp:read-only": "false", "pp:typed-exit-codes": "0,2,5,9"},
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProposal(cmd, flagFile)
			if err != nil {
				if flags.dryRun || cliutil.IsVerifyEnv() {
					// Preview with no usable proposal: emit an informative note
					// (non-empty, exit 0) rather than silently returning nothing.
					return printJSONFiltered(cmd.OutOrStdout(), map[string]any{
						"would_commit": false,
						"note":         fmt.Sprintf("no usable proposal: %v — pass --file <proposal.json> or pipe JSON to preview the request", err),
					}, flags)
				}
				return err
			}
			if strings.TrimSpace(flagCompany) != "" {
				p.CompanySlug = strings.TrimSpace(flagCompany)
			}

			// Structural validation (account check needs the mirror; optional).
			var validAccounts map[string]bool
			if db, derr := openMirror(cmd.Context(), dbPath); derr == nil {
				if p.CompanySlug != "" {
					validAccounts = loadValidAccounts(cmd.Context(), db, p.CompanySlug)
				}
				db.Close()
			}
			vres := fikencore.ValidateProposal(*p, validAccounts)
			if !skipValidate && !vres.OK && !force {
				_ = printJSONFiltered(cmd.OutOrStdout(), map[string]any{"committed": false, "reason": "validation_failed", "validation": vres}, flags)
				return &cliError{code: 2, err: fmt.Errorf("proposal failed validation (%d error(s)); fix it, or pass --force to commit anyway", len(vres.Errors))}
			}

			path, body, entityType, err := fikencore.BuildRequest(*p)
			if err != nil {
				return &cliError{code: 2, err: err}
			}

			// A purchase the supplier already has under this invoice number is
			// refused before anything is reserved or posted (#25). The
			// idempotency map below only knows what commit itself posted; a
			// source it has already committed keeps its own no-op answer.
			var duplicates []existingPurchase
			if entityType == "purchase" && !alreadyCommitted(cmd.Context(), corePath, p) {
				verdict, derr := checkDuplicatePurchase(cmd.Context(), flags, p.CompanySlug, body, allowDuplicate)
				switch {
				case derr != nil && (flags.dryRun || cliutil.IsVerifyEnv()):
					// A preview still previews; the real run will refuse.
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", derr)
				case derr != nil:
					return &cliError{code: duplicateExitCode, err: derr}
				}
				duplicates = verdict.Existing
			}

			// Dry-run / verify: preview only, no mutation. Read-only idempotency
			// lookup so the preview shows whether it would be a no-op.
			if flags.dryRun || cliutil.IsVerifyEnv() {
				status := "new"
				if core, cerr := fikencore.Open(cmd.Context(), corePath); cerr == nil {
					if ex, found, _ := core.Lookup(cmd.Context(), p.CompanySlug, p.SourceSystem, p.SourceID); found {
						status = ex.Status
					}
					core.Close()
				}
				preview := map[string]any{
					"would_commit":       len(duplicates) == 0,
					"idempotency_status": status,
					"fiken_entity_type":  entityType,
					"path":               path,
					"request":            body,
					"validation":         vres,
				}
				if len(duplicates) > 0 {
					preview["reason"] = "duplicate_purchase"
					preview["existing"] = duplicates
				}
				return printJSONFiltered(cmd.OutOrStdout(), preview, flags)
			}
			if len(duplicates) > 0 {
				if flags.idempotent {
					return printJSONFiltered(cmd.OutOrStdout(), map[string]any{
						"committed": false, "reason": "already_exists",
						"fiken_entity_type": entityType, "fiken_entity_id": fmt.Sprintf("%d", duplicates[0].PurchaseID),
						"existing": duplicates,
					}, flags)
				}
				_ = printJSONFiltered(cmd.OutOrStdout(), map[string]any{
					"committed": false, "reason": "duplicate_purchase", "existing": duplicates,
				}, flags)
				return duplicateError(body, duplicates)
			}

			// Gate real writes behind explicit consent.
			if !flags.yes && !flags.agent {
				return &cliError{code: 2, err: fmt.Errorf("commit mutates real books — re-run with --dry-run to preview, or --yes to confirm")}
			}

			core, err := fikencore.Open(cmd.Context(), corePath)
			if err != nil {
				return err
			}
			defer core.Close()

			created, existing, err := core.Reserve(cmd.Context(), fikencore.IdempotencyRecord{
				CompanySlug: p.CompanySlug, SourceSystem: p.SourceSystem, SourceID: p.SourceID,
				SourceHash: p.SourceHash, Status: fikencore.StatusProposed,
			})
			if err != nil {
				return err
			}
			if !created && (existing.Status == fikencore.StatusCommitted || existing.Status == fikencore.StatusMatched) {
				return printJSONFiltered(cmd.OutOrStdout(), map[string]any{
					"committed": false, "reason": "already_committed",
					"fiken_entity_type": existing.FikenEntityType, "fiken_entity_id": existing.FikenEntityID,
					"status": existing.Status,
				}, flags)
			}

			rid := fikenRequestID()
			c, err := flags.newClient()
			if err != nil {
				return err
			}
			// Fiken answers a create with an empty body and a Location header,
			// so the id comes off the header when the body has none (#26).
			postCtx, created201 := client.NewLocationContext(cmd.Context())
			resp, statusCode, err := c.PostWithHeaders(postCtx, path, body, map[string]string{"X-Request-ID": rid})
			inputsJSON, _ := json.Marshal(p)
			if err != nil || statusCode < 200 || statusCode >= 300 {
				_ = core.Advance(cmd.Context(), p.CompanySlug, p.SourceSystem, p.SourceID, fikencore.StatusFailed, fikencore.AdvanceOpts{XRequestID: rid})
				_, _ = core.LogEvent(cmd.Context(), fikencore.Event{
					CorrelationID: p.SourceSystem + ":" + p.SourceID, Surface: "api", Operation: fikencore.OpPostingCommitted,
					CompanySlug: p.CompanySlug, TargetEntityType: entityType, SourceRef: p.SourceSystem + ":" + p.SourceID,
					XRequestID: rid, Confidence: p.Confidence, Rationale: p.Rationale,
					InputsJSON: string(inputsJSON), Result: "failure", ResultJSON: fmt.Sprintf(`{"status":%d}`, statusCode),
				})
				if err != nil {
					return &cliError{code: 5, err: fmt.Errorf("commit POST failed: %w", err)}
				}
				return &cliError{code: 5, err: fmt.Errorf("commit POST returned HTTP %d: %s", statusCode, fikenTruncate(string(resp), 300))}
			}

			entityID := extractEntityID(resp, entityType)
			if entityID == "" {
				entityID = created201.ID()
			}
			_ = core.Advance(cmd.Context(), p.CompanySlug, p.SourceSystem, p.SourceID, fikencore.StatusCommitted,
				fikencore.AdvanceOpts{FikenEntityType: entityType, FikenEntityID: entityID, XRequestID: rid})
			eventID, _ := core.LogEvent(cmd.Context(), fikencore.Event{
				CorrelationID: p.SourceSystem + ":" + p.SourceID, Surface: "api", Operation: fikencore.OpPostingCommitted,
				CompanySlug: p.CompanySlug, TargetEntityType: entityType, TargetEntityID: entityID,
				SourceRef: p.SourceSystem + ":" + p.SourceID, XRequestID: rid, Confidence: p.Confidence,
				Rationale: p.Rationale, InputsJSON: string(inputsJSON), Result: "success",
			})

			return printJSONFiltered(cmd.OutOrStdout(), map[string]any{
				"committed": true, "fiken_entity_type": entityType, "fiken_entity_id": entityID,
				"x_request_id": rid, "event_id": eventID,
			}, flags)
		},
	}
	cmd.Flags().StringVar(&flagFile, "file", "", "Proposal JSON file (default: read from stdin)")
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: proposal's company_slug)")
	cmd.Flags().BoolVar(&force, "force", false, "Commit even if validation reports errors")
	cmd.Flags().BoolVar(&skipValidate, "skip-validate", false, "Skip client-side validation entirely")
	cmd.Flags().BoolVar(&allowDuplicate, allowDuplicateFlag, false, "Commit a purchase even when the supplier already has one with this invoice number")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (for the account check)")
	cmd.Flags().StringVar(&corePath, "core-db", "", "Core DB path (idempotency + audit)")
	return cmd
}

// alreadyCommitted reports whether the idempotency map holds this proposal's
// source as committed or matched. Read-only: it reserves nothing.
func alreadyCommitted(ctx context.Context, corePath string, p *fikencore.Proposal) bool {
	core, err := fikencore.Open(ctx, corePath)
	if err != nil {
		return false
	}
	defer core.Close()
	ex, found, _ := core.Lookup(ctx, p.CompanySlug, p.SourceSystem, p.SourceID)
	return found && (ex.Status == fikencore.StatusCommitted || ex.Status == fikencore.StatusMatched)
}

// extractEntityID pulls the created entity's id from a Fiken create response,
// trying the type-specific id field first, then common fallbacks.
func extractEntityID(resp []byte, entityType string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(resp, &obj); err != nil {
		return ""
	}
	keys := []string{"purchaseId", "saleId", "journalEntryId", "transactionId", "id"}
	if entityType == "purchase" {
		keys = append([]string{"purchaseId", "transactionId"}, keys...)
	}
	for _, k := range keys {
		if raw, ok := obj[k]; ok {
			var n int64
			if json.Unmarshal(raw, &n) == nil && n != 0 {
				return fmt.Sprintf("%d", n)
			}
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				return s
			}
		}
	}
	return ""
}

func fikenTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

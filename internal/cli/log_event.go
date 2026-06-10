// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator stub. The permissive,
// grammar-validated audit sink the CLI and the browser "Superføring" harness
// both write to (build-spec §7/§8.2). It WARNS on an unknown-but-grammar-valid
// operation and never rejects it; only a grammar violation is refused. Writes
// to the append-only agent_events core DB.
package cli

// pp:data-source local

import (
	"fmt"
	"strings"

	"fiken-cli/internal/fikencore"

	"github.com/spf13/cobra"
)

func newNovelLogEventCmd(flags *rootFlags) *cobra.Command {
	var op, surface, company, correlationID, sourceRef, actor, confidence string
	var targetType, targetID, inputs, result, resultJSON, reverses, corePath string

	cmd := &cobra.Command{
		Use:   "log-event",
		Short: "Append a grammar-validated event to the append-only audit log",
		Long: "Records one event in the append-only audit log (agent_events). The operation must follow the\n" +
			"noun.verb grammar; an unknown-but-valid operation is accepted with a warning (vocabulary growth),\n" +
			"never rejected. Both this CLI and the browser Superføring harness write here.",
		Example: strings.Trim(`
  fiken-cli log-event --operation match.confirmed --surface browser --source-ref linje:9876543
  fiken-cli log-event --operation posting.committed --company fiken-demo --target-id 999 --result success`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "false"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if op == "" {
				return fmt.Errorf("--operation is required (noun.verb, e.g. match.confirmed)")
			}
			if !fikencore.OperationGrammarValid(op) {
				return fmt.Errorf("--operation %q violates the noun.verb grammar (lowercase, dot-separated)", op)
			}
			if surface == "" {
				surface = "api"
			}
			if surface != "api" && surface != "browser" {
				return fmt.Errorf("--surface must be api or browser, got %q", surface)
			}
			if result == "" {
				result = "success"
			}
			if result != "success" && result != "failure" {
				return fmt.Errorf("--result must be success or failure, got %q", result)
			}
			if dryRunOK(flags) {
				return nil
			}

			core, err := fikencore.Open(cmd.Context(), corePath)
			if err != nil {
				return err
			}
			defer core.Close()

			id, err := core.LogEvent(cmd.Context(), fikencore.Event{
				CorrelationID: correlationID, Surface: surface, Operation: op,
				CompanySlug: company, Actor: actor, TargetEntityType: targetType,
				TargetEntityID: targetID, SourceRef: sourceRef, Confidence: confidence,
				Rationale: "", InputsJSON: inputs, Result: result, ResultJSON: resultJSON,
				ReversesEventID: reverses,
			})
			if err != nil {
				return err
			}

			unknown := !fikencore.OperationKnown(op)
			out := map[string]any{"event_id": id, "operation": op, "result": result, "logged": true}
			if unknown {
				out["warning"] = fmt.Sprintf("operation %q is not in the seed vocabulary — logged anyway (vocabulary growth)", op)
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: operation %q not in known set — logged anyway\n", op)
			}
			return printJSONFiltered(cmd.OutOrStdout(), out, flags)
		},
	}
	cmd.Flags().StringVar(&op, "operation", "", "Event operation, noun.verb (e.g. match.confirmed)")
	cmd.Flags().StringVar(&surface, "surface", "api", "Surface that produced the event: api|browser")
	cmd.Flags().StringVar(&company, "company", "", "Company slug the event relates to")
	cmd.Flags().StringVar(&correlationID, "correlation-id", "", "Correlation id tying a multi-step arc together")
	cmd.Flags().StringVar(&sourceRef, "source-ref", "", "Upstream reference (inbox doc id, payout id, linje.id)")
	cmd.Flags().StringVar(&actor, "actor", "", "Actor: skill/session/agent id")
	cmd.Flags().StringVar(&confidence, "confidence", "", "Confidence: high|medium|low")
	cmd.Flags().StringVar(&targetType, "target-type", "", "Fiken entity type touched")
	cmd.Flags().StringVar(&targetID, "target-id", "", "Fiken entity id touched")
	cmd.Flags().StringVar(&inputs, "inputs", "", "Inputs JSON object (a top-level \"v\" is injected if absent)")
	cmd.Flags().StringVar(&result, "result", "success", "Result: success|failure")
	cmd.Flags().StringVar(&resultJSON, "result-json", "", "Result JSON object")
	cmd.Flags().StringVar(&reverses, "reverses", "", "Event id this event reverses")
	cmd.Flags().StringVar(&corePath, "core-db", "", "Core DB path (default: ~/.local/share/fiken-cli/fiken-core.db)")
	return cmd
}

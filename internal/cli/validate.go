// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator stub. The client-side
// dry-run Fiken lacks (build-spec §8.2): checks a proposed posting for balanced
// structure, account existence (against the synced chart of accounts), MVA-code
// plausibility, and date sanity BEFORE it touches the books. Read-only;
// validation is structural, not authoritative (§9). Exits non-zero when the
// proposal has errors so agents/scripts can gate on it.
package cli

// pp:data-source local

import (
	"fmt"
	"strings"

	"fiken-cli/internal/cliutil"
	"fiken-cli/internal/fikencore"

	"github.com/spf13/cobra"
)

func newNovelValidateCmd(flags *rootFlags) *cobra.Command {
	var flagFile, flagCompany, dbPath string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a proposed posting before commit (balance, accounts, MVA, dates)",
		Long: "Reads a posting proposal (--file or stdin) and checks it structurally against the synced chart\n" +
			"of accounts and the Fiken VAT-code table. Errors block a commit unless overridden; warnings are\n" +
			"advisory. Read-only. Exit code 0 = OK, 2 = validation errors.",
		Example: strings.Trim(`
  fiken-cli validate --file proposal.json --agent
  cat proposal.json | fiken-cli validate`, "\n"),
		Annotations:  map[string]string{"mcp:read-only": "true", "pp:typed-exit-codes": "0,2"},
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProposal(cmd, flagFile)
			if err != nil {
				if flags.dryRun || cliutil.IsVerifyEnv() {
					return printJSONFiltered(cmd.OutOrStdout(), map[string]any{
						"ok":   false,
						"note": fmt.Sprintf("no usable proposal: %v — pass --file <proposal.json> or pipe JSON", err),
					}, flags)
				}
				return err
			}
			if dryRunOK(flags) {
				return nil
			}

			// Account-existence check needs the mirror; if it isn't available
			// (not synced yet) we proceed with an empty set, which disables only
			// that one check rather than failing the whole validation.
			var validAccounts map[string]bool
			slug := strings.TrimSpace(flagCompany)
			if slug == "" {
				slug = p.CompanySlug
			}
			if db, derr := openMirror(cmd.Context(), dbPath); derr == nil {
				if slug != "" {
					validAccounts = loadValidAccounts(cmd.Context(), db, slug)
				}
				db.Close()
			}

			res := fikencore.ValidateProposal(*p, validAccounts)
			if err := printJSONFiltered(cmd.OutOrStdout(), res, flags); err != nil {
				return err
			}
			if !res.OK {
				return &cliError{code: 2, err: fmt.Errorf("validation failed: %d error(s)", len(res.Errors))}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flagFile, "file", "", "Proposal JSON file (default: read from stdin)")
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug for the account check (default: proposal's company_slug)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	return cmd
}

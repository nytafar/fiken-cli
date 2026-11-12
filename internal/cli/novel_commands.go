// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Registration of the commands written after the print. The generated root.go
// lists the printed commands one AddCommand at a time; a command the print
// never knew is added here instead, so root.go carries a single patched line
// however many there are.
package cli

import "github.com/spf13/cobra"

func addHandAuthoredCommands(root *cobra.Command, flags *rootFlags) {
	root.AddCommand(newNovelMvaStatusCmd(flags))
	root.AddCommand(newNovelRecurringGapsCmd(flags))
}

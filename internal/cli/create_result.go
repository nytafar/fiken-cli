// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The id of what a create made (issue #26). Fiken answers a create with 201,
// an empty body and a Location header. The generated POST commands print
// `{action, path, resource, status, success}` and nothing that identifies the
// new resource, so every `purchases create` was followed by a list on the date
// and a match on the invoice number — fragile when two purchases share a date.
//
// The fix wraps the built command tree once instead of editing 49 generated
// files: each command annotated `pp:method: POST` runs under a context whose
// responses feed a client.LocationSink, and when one of them carried a
// Location the JSON envelope gains `id` and `location`. A terminal reader, who
// gets the (empty) response body rather than the envelope, gets one line
// naming the new resource instead of nothing.
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"fiken-cli/internal/client"
)

// applyCreateResultIDs wraps every generated POST command under root.
func applyCreateResultIDs(cmd *cobra.Command, flags *rootFlags) {
	for _, sub := range cmd.Commands() {
		applyCreateResultIDs(sub, flags)
	}
	if cmd.Annotations["pp:method"] != "POST" || cmd.RunE == nil {
		return
	}
	run := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		ctx, sink := client.NewLocationContext(c.Context())
		c.SetContext(ctx)

		out := c.OutOrStdout()
		// The generated command prints the envelope when JSON was asked for or
		// stdout is not a terminal, and the raw body otherwise. Only the
		// envelope is rewritten, so the buffer goes in only where the command
		// would have printed JSON anyway: a buffer is never a terminal, and
		// swapping it in for one would change the output mode.
		if !flags.asJSON && isTerminal(out) {
			err := run(c, args)
			if err == nil && sink.Location() != "" {
				fmt.Fprintf(out, "Created %s (%s)\n", sink.ID(), sink.Location())
			}
			return err
		}

		var buf bytes.Buffer
		c.SetOut(&buf)
		err := run(c, args)
		c.SetOut(out)
		if _, werr := out.Write(withCreateResult(buf.Bytes(), sink.Location())); werr != nil && err == nil {
			err = werr
		}
		return err
	}
}

// withCreateResult adds `id` and `location` to a mutation envelope. The output
// is returned unchanged when there is no Location, when it is not the
// envelope (--csv, --quiet, a dry run's request preview) or when the envelope
// does not report success: an id must never ride on a create that failed.
func withCreateResult(output []byte, location string) []byte {
	if location == "" || len(bytes.TrimSpace(output)) == 0 {
		return output
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(output, &envelope); err != nil {
		return output
	}
	var success bool
	if raw, ok := envelope["success"]; !ok || json.Unmarshal(raw, &success) != nil || !success {
		return output
	}
	if _, isEnvelope := envelope["action"]; !isEnvelope {
		return output
	}
	envelope["id"] = createdIDJSON(client.LocationID(location))
	loc, _ := json.Marshal(location)
	envelope["location"] = loc

	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(envelope); err != nil {
		return output
	}
	return out.Bytes()
}

// createdIDJSON renders an id the way the API does: Fiken's purchase, sale,
// payment and contact ids are integers and the draft ids are UUID strings, so
// a numeric id is emitted as a number and `jq .id` prints it bare.
func createdIDJSON(id string) json.RawMessage {
	if n, err := strconv.ParseInt(id, 10, 64); err == nil && strconv.FormatInt(n, 10) == id {
		return json.RawMessage(id)
	}
	quoted, _ := json.Marshal(id)
	return quoted
}

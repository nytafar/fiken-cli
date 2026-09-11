// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Document download (issue #23). `inbox get-document` and the two attachment
// listings return metadata carrying a `documentUrl` / `downloadUrl`, but no
// command fetched the bytes behind it, so the Superføring matching workflow
// had to dig the access token out of ~/.config/fiken-cli/config.toml and curl
// the URL by hand. --output turns the same metadata read into a download:
// the URLs are read out of the response the command already made, and each
// one is streamed through the authenticated client (internal/client/
// download.go) to a file, a directory, or stdout.
//
// Also here: the --filename default for the add-attachment commands. Fiken
// answers HTTP 400 `filename must be specified` when a multipart upload omits
// it, which is a trap the CLI can close by defaulting it to the basename of
// --file. Doing it as one PreRunE wrapper over the built command tree keeps
// the generated-file diff at a single call site instead of one edit in each
// of the twelve add-attachment commands.

package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"path/filepath"
	"strings"

	"fiken-cli/internal/client"

	"github.com/spf13/cobra"
)

const documentOutputFlag = "output"

// registerDocumentOutputFlag adds --output to a generated read command whose
// payload carries document URLs.
func registerDocumentOutputFlag(cmd *cobra.Command) {
	cmd.Flags().String(documentOutputFlag, "", "Download the document file(s): a file path, a directory (trailing / or existing dir) to use the document filename, or - for stdout")
}

// documentRef is one downloadable file named by a response payload.
type documentRef struct {
	URL      string
	Filename string
}

// downloadedDocument is the --output result for one file. The JSON shape is
// the one issue #23 asked for: {"path":..., "bytes":..., "filename":...}.
type downloadedDocument struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Filename string `json:"filename"`
}

// handleDocumentOutput implements --output for a metadata read that has
// already happened. It returns handled=false when --output was not given, so
// the calling command falls through to its normal rendering.
func handleDocumentOutput(cmd *cobra.Command, flags *rootFlags, c *client.Client, apiPath string, data json.RawMessage) (bool, error) {
	target, err := cmd.Flags().GetString(documentOutputFlag)
	if err != nil || strings.TrimSpace(target) == "" {
		return false, nil
	}
	target = strings.TrimSpace(target)

	// --dry-run: the metadata GET has already printed itself through the
	// client's dry-run path and returned a stub body, so there is nothing to
	// read URLs out of. Print the download that would follow and stop.
	if flags.dryRun {
		if documentOutputWantsJSON(cmd, flags) && target != "-" {
			return true, printDocumentJSON(cmd, map[string]any{
				"dry_run": true,
				"source":  apiPath,
				"output":  target,
			})
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "would download the document(s) named by %s to %s\n", apiPath, target)
		return true, nil
	}

	refs := collectDocumentRefs(data)
	if len(refs) == 0 {
		return true, fmt.Errorf("no downloadable document URL in the response for %s (expected documentUrl or downloadUrl)", apiPath)
	}
	if target == "-" && len(refs) > 1 {
		return true, usageErr(fmt.Errorf("%s returned %d attachments; --output - streams a single file, give a directory instead", apiPath, len(refs)))
	}

	results := make([]downloadedDocument, 0, len(refs))
	for i, ref := range refs {
		res, err := downloadDocumentRef(cmd, c, target, ref, i, len(refs) > 1)
		if err != nil {
			return true, err
		}
		results = append(results, res)
	}

	return true, reportDownloads(cmd, flags, target, results)
}

// downloadDocumentRef writes one document to stdout or into the directory the
// target names. The file lands through a temporary file in the destination
// directory so an interrupted download never leaves a half-written PDF under
// the final name, and so a filename the server supplies (Content-Disposition)
// can still be used when the metadata carried none.
func downloadDocumentRef(cmd *cobra.Command, c *client.Client, target string, ref documentRef, index int, multiple bool) (downloadedDocument, error) {
	if target == "-" {
		got, err := c.DownloadFile(cmd.Context(), ref.URL, cmd.OutOrStdout())
		if err != nil {
			return downloadedDocument{}, err
		}
		return downloadedDocument{Path: "-", Bytes: got.Bytes, Filename: documentFilename(ref, got, index)}, nil
	}

	dir, fixedName := splitDownloadTarget(target, multiple)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return downloadedDocument{}, fmt.Errorf("creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".fiken-download-*")
	if err != nil {
		return downloadedDocument{}, fmt.Errorf("creating temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	got, err := c.DownloadFile(cmd.Context(), ref.URL, tmp)
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmpName)
		return downloadedDocument{}, err
	}

	name := fixedName
	if name == "" {
		name = documentFilename(ref, got, index)
	}
	dest := filepath.Join(dir, name)
	if err := os.Rename(tmpName, dest); err != nil {
		_ = os.Remove(tmpName)
		return downloadedDocument{}, fmt.Errorf("writing %s: %w", dest, err)
	}
	if err := os.Chmod(dest, 0o644); err != nil {
		return downloadedDocument{}, fmt.Errorf("setting mode on %s: %w", dest, err)
	}
	return downloadedDocument{Path: dest, Bytes: got.Bytes, Filename: name}, nil
}

// splitDownloadTarget decides whether --output named a file or a directory.
// A trailing separator, an existing directory, or more than one document to
// write all mean "directory"; anything else is the exact file to write.
func splitDownloadTarget(target string, multiple bool) (dir string, fixedName string) {
	if multiple || strings.HasSuffix(target, string(os.PathSeparator)) || strings.HasSuffix(target, "/") {
		return filepath.Clean(target), ""
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return filepath.Clean(target), ""
	}
	return filepath.Dir(target), filepath.Base(target)
}

// documentFilename picks the name to write under: the metadata filename
// first, then the one the server suggested, then a positional fallback so a
// nameless attachment still lands somewhere predictable.
func documentFilename(ref documentRef, got client.DownloadedFile, index int) string {
	if name := client.SanitizeFilename(ref.Filename); name != "" {
		return name
	}
	if name := client.SanitizeFilename(got.Filename); name != "" {
		return name
	}
	return fmt.Sprintf("document-%d", index+1)
}

// collectDocumentRefs reads every document URL out of a metadata payload.
// Handles both shapes the API uses: a single inbox document object and a bare
// array of attachments.
func collectDocumentRefs(data json.RawMessage) []documentRef {
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	var refs []documentRef
	switch v := parsed.(type) {
	case map[string]any:
		if ref, ok := documentRefFrom(v); ok {
			refs = append(refs, ref)
		}
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if ref, ok := documentRefFrom(m); ok {
					refs = append(refs, ref)
				}
			}
		}
	}
	return refs
}

// documentRefFrom reads the URL and filename fields off one object.
// documentUrl is the inbox spelling, downloadUrl the attachment spelling; the
// *WithFikenNormalUserCredentials variants are deliberately ignored because
// they need an interactive Fiken login, not an API token.
func documentRefFrom(obj map[string]any) (documentRef, bool) {
	var ref documentRef
	for _, key := range []string{"documentUrl", "downloadUrl"} {
		if s, ok := obj[key].(string); ok && strings.TrimSpace(s) != "" {
			ref.URL = strings.TrimSpace(s)
			break
		}
	}
	if ref.URL == "" {
		return ref, false
	}
	for _, key := range []string{"filename", "name"} {
		if s, ok := obj[key].(string); ok && strings.TrimSpace(s) != "" {
			ref.Filename = strings.TrimSpace(s)
			break
		}
	}
	// A nameless attachment is left nameless here on purpose: the download
	// itself can still learn a name from Content-Disposition or the URL, and
	// documentFilename() only falls back to a positional name after both.
	return ref, true
}

// reportDownloads prints what landed: the single-document JSON object issue
// #23 specified, a {"files":[...]} list for a multi-attachment download, or
// one human line per file. A download streamed to stdout reports on stderr,
// because stdout is carrying the file.
func reportDownloads(cmd *cobra.Command, flags *rootFlags, target string, results []downloadedDocument) error {
	if target == "-" {
		for _, r := range results {
			fmt.Fprintf(cmd.ErrOrStderr(), "wrote %d bytes to stdout (%s)\n", r.Bytes, r.Filename)
		}
		return nil
	}
	if documentOutputWantsJSON(cmd, flags) {
		if len(results) == 1 {
			return printDocumentJSON(cmd, results[0])
		}
		var total int64
		for _, r := range results {
			total += r.Bytes
		}
		return printDocumentJSON(cmd, map[string]any{
			"files": results,
			"count": len(results),
			"bytes": total,
		})
	}
	for _, r := range results {
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d bytes)\n", r.Path, r.Bytes)
	}
	return nil
}

func printDocumentJSON(cmd *cobra.Command, v any) error {
	encoded, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
	return err
}

// documentOutputWantsJSON mirrors the generated commands' machine-output gate
// so --agent, --json and a piped stdout all get the JSON envelope.
func documentOutputWantsJSON(cmd *cobra.Command, flags *rootFlags) bool {
	return flags.asJSON || (!isTerminal(cmd.OutOrStdout()) && !flags.csv && !flags.quiet && !flags.plain)
}

// applyAttachmentFilenameDefaults walks the built command tree and, for every
// command carrying both --file and --filename, wraps PreRunE so an unset
// --filename defaults to the basename of --file. Registration-time wrapping
// rather than twelve in-command edits: the generated files stay untouched and
// a future add-attachment command is covered the day it is printed.
func applyAttachmentFilenameDefaults(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		applyAttachmentFilenameDefaults(sub)
	}
	if cmd.Flags().Lookup("file") == nil || cmd.Flags().Lookup("filename") == nil {
		return
	}
	prevE := cmd.PreRunE
	prev := cmd.PreRun
	cmd.PreRunE = func(c *cobra.Command, args []string) error {
		if err := defaultFilenameFromFile(c); err != nil {
			return err
		}
		if prevE != nil {
			return prevE(c, args)
		}
		if prev != nil {
			prev(c, args)
		}
		return nil
	}
}

// defaultFilenameFromFile sets --filename from --file when the user left it
// unset. Setting the flag value (rather than a local variable) is what makes
// this work from outside the generated command: the generated RunE reads the
// variable the flag is bound to, so flag.Set writes straight through to it.
func defaultFilenameFromFile(cmd *cobra.Command) error {
	flagSet := cmd.Flags()
	nameFlag := flagSet.Lookup("filename")
	fileFlag := flagSet.Lookup("file")
	if nameFlag == nil || fileFlag == nil || nameFlag.Changed {
		return nil
	}
	base := client.SanitizeFilename(fileFlag.Value.String())
	if base == "" {
		return nil
	}
	return flagSet.Set("filename", base)
}

// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Pins --output (issue #23) end to end: the command tree is the real one, the
// server is an httptest Fiken that serves both the metadata and the file
// bytes, and the assertions are the three things an agent depends on — the
// bytes land unmangled, the download carries the bearer token instead of the
// operator pasting it into curl, and --dry-run fetches nothing.

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var pdfBytes = []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\nfaktura\n%%EOF\n")

// dupBytes gives the two same-named attachments distinguishable bodies, so a
// test can tell "both landed" from "the second overwrote the first".
func dupBytes(n int) []byte {
	return []byte(fmt.Sprintf("%%PDF-1.4\nattachment-%d\n%%%%EOF\n", n))
}

// docServer is a fake Fiken: one inbox document, one purchase with two
// attachments, and the files behind their URLs.
type docServer struct {
	baseURL string

	mu       sync.Mutex
	requests []string
	authSeen map[string]string
}

func (d *docServer) record(r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requests = append(d.requests, r.URL.Path)
	if d.authSeen == nil {
		d.authSeen = map[string]string{}
	}
	d.authSeen[r.URL.Path] = r.Header.Get("Authorization")
}

func (d *docServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.record(r)
	switch r.URL.Path {
	case "/companies/agensia/inbox/42":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"inboxDocumentId":42,"name":"Faktura","filename":"faktura-123.pdf","documentUrl":"` + d.baseURL + `/download/inbox/42"}`))
	case "/companies/agensia/inbox/43":
		// No filename and no name: the only thing this document can be
		// named after is its URL's last segment and its content type.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"inboxDocumentId":43,"documentUrl":"` + d.baseURL + `/download/inbox/43"}`))
	case "/companies/agensia/purchases/7/attachments":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"identifier":"24760","filename":"bilag-a.pdf","downloadUrl":"` + d.baseURL + `/download/att/a"},` +
			`{"identifier":"24761","filename":"bilag-b.pdf","downloadUrl":"` + d.baseURL + `/download/att/b"}]`))
	case "/companies/agensia/purchases/8/attachments":
		// Fiken lets two attachments of one purchase carry the same
		// filename; a scanner that names everything "bilag.pdf" is the
		// normal case, not a corner one.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"identifier":"24762","filename":"bilag.pdf","downloadUrl":"` + d.baseURL + `/download/dup/1"},` +
			`{"identifier":"24763","filename":"bilag.pdf","downloadUrl":"` + d.baseURL + `/download/dup/2"}]`))
	case "/download/inbox/42", "/download/att/a", "/download/att/b":
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdfBytes)
	case "/download/inbox/43":
		// No Content-Disposition either: the name has to come from the URL.
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdfBytes)
	case "/download/dup/1":
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(dupBytes(1))
	case "/download/dup/2":
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(dupBytes(2))
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (d *docServer) paths() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.requests))
	copy(out, d.requests)
	return out
}

func (d *docServer) authFor(path string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.authSeen[path]
}

// runCLI drives the real command tree against the fake server with a home
// directory of its own, so the write-through mirror and the HTTP cache land
// in the test's temp dir and not in the operator's.
func runCLI(t *testing.T, args ...string) (*docServer, string, string, error) {
	t.Helper()

	ds := &docServer{}
	srv := httptest.NewServer(ds)
	t.Cleanup(srv.Close)
	ds.baseURL = srv.URL

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FIKEN_CONFIG", filepath.Join(home, "config.toml"))
	t.Setenv("FIKEN_BASE_URL", srv.URL)
	t.Setenv("FIKEN_API_TOKEN", "test-token")

	var out, errOut bytes.Buffer
	root := RootCmd()
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.Execute()
	return ds, out.String(), errOut.String(), err
}

func TestInboxGetDocument_OutputWritesFileAndPrintsJSON(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "bilag.pdf")

	ds, out, _, err := runCLI(t, "inbox", "get-document", "agensia", "42", "--output", dest)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, readErr := os.ReadFile(dest)
	if readErr != nil {
		t.Fatalf("read downloaded file: %v", readErr)
	}
	if !bytes.Equal(got, pdfBytes) {
		t.Fatalf("downloaded bytes = %q, want %q", got, pdfBytes)
	}

	var res downloadedDocument
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(out)), &res); jsonErr != nil {
		t.Fatalf("stdout %q is not the {path,bytes,filename} envelope: %v", out, jsonErr)
	}
	if res.Path != dest {
		t.Fatalf("path = %q, want %q", res.Path, dest)
	}
	if res.Bytes != int64(len(pdfBytes)) {
		t.Fatalf("bytes = %d, want %d", res.Bytes, len(pdfBytes))
	}
	if res.Filename != "bilag.pdf" {
		t.Fatalf("filename = %q, want %q", res.Filename, "bilag.pdf")
	}

	// The whole point of the flag: the token rides the download request so it
	// never has to be pasted into a shell.
	if auth := ds.authFor("/download/inbox/42"); auth != "Bearer test-token" {
		t.Fatalf("download Authorization = %q, want %q", auth, "Bearer test-token")
	}
}

func TestInboxGetDocument_OutputDirectoryUsesDocumentFilename(t *testing.T) {
	dir := t.TempDir()

	_, out, _, err := runCLI(t, "inbox", "get-document", "agensia", "42", "--output", dir+string(os.PathSeparator))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	want := filepath.Join(dir, "faktura-123.pdf")
	if _, statErr := os.Stat(want); statErr != nil {
		t.Fatalf("want %s written: %v (stdout %q)", want, statErr, out)
	}
}

func TestInboxGetDocument_OutputDashStreamsToStdout(t *testing.T) {
	_, out, errOut, err := runCLI(t, "inbox", "get-document", "agensia", "42", "--output", "-")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != string(pdfBytes) {
		t.Fatalf("stdout = %q, want the file bytes %q", out, pdfBytes)
	}
	if !strings.Contains(errOut, "wrote") {
		t.Fatalf("stderr = %q, want a summary line", errOut)
	}
}

func TestInboxGetDocument_OutputDryRunDownloadsNothing(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "bilag.pdf")

	ds, _, _, err := runCLI(t, "inbox", "get-document", "agensia", "42", "--output", dest, "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Fatalf("--dry-run wrote %s", dest)
	}
	for _, p := range ds.paths() {
		t.Fatalf("--dry-run sent a request to %s", p)
	}
}

func TestPurchasesAttachmentsGet_OutputDownloadsEveryAttachment(t *testing.T) {
	dir := t.TempDir()

	_, out, _, err := runCLI(t, "purchases", "attachments", "get-purchase", "agensia", "7", "--output", dir)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	for _, name := range []string{"bilag-a.pdf", "bilag-b.pdf"} {
		path := filepath.Join(dir, name)
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v (stdout %q)", path, readErr, out)
		}
		if !bytes.Equal(got, pdfBytes) {
			t.Fatalf("%s bytes = %q, want %q", path, got, pdfBytes)
		}
	}

	var listed struct {
		Files []downloadedDocument `json:"files"`
		Count int                  `json:"count"`
		Bytes int64                `json:"bytes"`
	}
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(out)), &listed); jsonErr != nil {
		t.Fatalf("stdout %q is not the {files,...} envelope: %v", out, jsonErr)
	}
	if listed.Count != 2 || len(listed.Files) != 2 {
		t.Fatalf("count = %d, files = %d, want 2 and 2", listed.Count, len(listed.Files))
	}
	if listed.Bytes != int64(2*len(pdfBytes)) {
		t.Fatalf("bytes = %d, want %d", listed.Bytes, 2*len(pdfBytes))
	}

	// No stray temp files left behind.
	entries, dirErr := os.ReadDir(dir)
	if dirErr != nil {
		t.Fatalf("read dir: %v", dirErr)
	}
	if len(entries) != 2 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want exactly the two attachments", names)
	}
}

// Two attachments named "bilag.pdf" used to resolve to one destination: the
// second os.Rename overwrote the first while the JSON still said count: 2.
// Both files must be on disk, and files[] must name what is actually there.
func TestPurchasesAttachmentsGet_SameFilenameTwiceKeepsBothFiles(t *testing.T) {
	dir := t.TempDir()

	_, out, _, err := runCLI(t, "purchases", "attachments", "get-purchase", "agensia", "8", "--output", dir)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	want := map[string][]byte{
		filepath.Join(dir, "bilag.pdf"):   dupBytes(1),
		filepath.Join(dir, "bilag-2.pdf"): dupBytes(2),
	}
	for path, body := range want {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v (stdout %q)", path, readErr, out)
		}
		if !bytes.Equal(got, body) {
			t.Fatalf("%s = %q, want %q", path, got, body)
		}
	}

	entries, dirErr := os.ReadDir(dir)
	if dirErr != nil {
		t.Fatalf("read dir: %v", dirErr)
	}
	if len(entries) != 2 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want both attachments and nothing else", names)
	}

	var listed struct {
		Files []downloadedDocument `json:"files"`
		Count int                  `json:"count"`
	}
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(out)), &listed); jsonErr != nil {
		t.Fatalf("stdout %q is not the {files,...} envelope: %v", out, jsonErr)
	}
	if listed.Count != 2 || len(listed.Files) != 2 {
		t.Fatalf("count = %d, files = %d, want 2 and 2", listed.Count, len(listed.Files))
	}
	for _, f := range listed.Files {
		if _, statErr := os.Stat(f.Path); statErr != nil {
			t.Fatalf("files[] reports %s, which is not on disk: %v", f.Path, statErr)
		}
		if f.Filename != filepath.Base(f.Path) {
			t.Fatalf("files[] filename %q does not match path %q", f.Filename, f.Path)
		}
	}
	if listed.Files[0].Path == listed.Files[1].Path {
		t.Fatalf("both attachments report the same path %q", listed.Files[0].Path)
	}
}

// A document with no filename in its metadata and no Content-Disposition on
// the download is named after the URL's last segment -- "43" -- which without
// the content type's extension is a PDF nothing will open.
func TestInboxGetDocument_NamelessDocumentGetsContentTypeExtension(t *testing.T) {
	dir := t.TempDir()

	_, out, _, err := runCLI(t, "inbox", "get-document", "agensia", "43", "--output", dir+string(os.PathSeparator))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	want := filepath.Join(dir, "43.pdf")
	if _, statErr := os.Stat(want); statErr != nil {
		entries, _ := os.ReadDir(dir)
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("want %s written, directory holds %v (stdout %q)", want, names, out)
	}
}

func TestUniqueDestination(t *testing.T) {
	taken := map[string]bool{}
	got := []string{
		uniqueDestination("/out", "bilag.pdf", taken),
		uniqueDestination("/out", "bilag.pdf", taken),
		uniqueDestination("/out", "bilag.pdf", taken),
		uniqueDestination("/out", "other.pdf", taken),
		uniqueDestination("/out", "noext", taken),
		uniqueDestination("/out", "noext", taken),
	}
	want := []string{
		filepath.Join("/out", "bilag.pdf"),
		filepath.Join("/out", "bilag-2.pdf"),
		filepath.Join("/out", "bilag-3.pdf"),
		filepath.Join("/out", "other.pdf"),
		filepath.Join("/out", "noext"),
		filepath.Join("/out", "noext-2"),
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("destination %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSalesAttachmentsGet_HasOutputFlag(t *testing.T) {
	root := RootCmd()
	cmd, _, err := root.Find([]string{"sales", "attachments", "get-sale"})
	if err != nil {
		t.Fatalf("find sales attachments get-sale: %v", err)
	}
	if cmd.Flags().Lookup(documentOutputFlag) == nil {
		t.Fatalf("sales attachments get-sale has no --%s", documentOutputFlag)
	}
}

// The --filename trap from issue #23: Fiken answers 400 "filename must be
// specified" when a multipart upload omits it, so an unset --filename takes
// the basename of --file.
func TestAddAttachmentCommands_DefaultFilenameToFileBasename(t *testing.T) {
	commands := [][]string{
		{"purchases", "attachments", "add-to-purchase"},
		{"sales", "attachments", "add-to-sale"},
		{"contacts", "attachments", "add-to-contact"},
		{"journal-entries", "attachments", "add-to-journal-entry"},
		{"invoices", "add-attachment-to-draft"},
		{"credit-notes", "add-attachment-to-draft"},
		{"offers", "add-attachment-to-draft"},
		{"inbox", "create-document"},
	}

	for _, args := range commands {
		name := strings.Join(args, " ")
		t.Run(name, func(t *testing.T) {
			root := RootCmd()
			cmd, _, err := root.Find(args)
			if err != nil {
				t.Fatalf("find %s: %v", name, err)
			}
			if cmd.PreRunE == nil {
				t.Fatalf("%s has no PreRunE: the --filename default is not wired", name)
			}
			if setErr := cmd.Flags().Set("file", "/home/lasse/bilag/faktura 42.pdf"); setErr != nil {
				t.Fatalf("set --file: %v", setErr)
			}
			if runErr := cmd.PreRunE(cmd, []string{"agensia", "1"}); runErr != nil {
				t.Fatalf("PreRunE: %v", runErr)
			}
			if got := cmd.Flags().Lookup("filename").Value.String(); got != "faktura 42.pdf" {
				t.Fatalf("--filename = %q, want %q", got, "faktura 42.pdf")
			}
		})
	}
}

func TestAddAttachment_ExplicitFilenameWins(t *testing.T) {
	root := RootCmd()
	cmd, _, err := root.Find([]string{"purchases", "attachments", "add-to-purchase"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if setErr := cmd.Flags().Set("file", "/tmp/scan.pdf"); setErr != nil {
		t.Fatalf("set --file: %v", setErr)
	}
	if setErr := cmd.Flags().Set("filename", "bilag-2026-05.pdf"); setErr != nil {
		t.Fatalf("set --filename: %v", setErr)
	}
	if runErr := cmd.PreRunE(cmd, []string{"agensia", "1"}); runErr != nil {
		t.Fatalf("PreRunE: %v", runErr)
	}
	if got := cmd.Flags().Lookup("filename").Value.String(); got != "bilag-2026-05.pdf" {
		t.Fatalf("--filename = %q, want the explicit value", got)
	}
}

func TestSplitDownloadTarget(t *testing.T) {
	existingDir := t.TempDir()

	cases := []struct {
		name     string
		target   string
		multiple bool
		wantDir  string
		wantFile string
	}{
		{"explicit file", filepath.Join(existingDir, "sub", "a.pdf"), false, filepath.Join(existingDir, "sub"), "a.pdf"},
		{"trailing slash", existingDir + "/", false, existingDir, ""},
		{"existing dir", existingDir, false, existingDir, ""},
		{"multiple forces dir", filepath.Join(existingDir, "out"), true, filepath.Join(existingDir, "out"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, file := splitDownloadTarget(tc.target, tc.multiple)
			if dir != tc.wantDir || file != tc.wantFile {
				t.Fatalf("splitDownloadTarget(%q, %v) = (%q, %q), want (%q, %q)", tc.target, tc.multiple, dir, file, tc.wantDir, tc.wantFile)
			}
		})
	}
}

func TestCollectDocumentRefs(t *testing.T) {
	single := collectDocumentRefs(json.RawMessage(`{"filename":"a.pdf","documentUrl":"https://api.fiken.no/x"}`))
	if len(single) != 1 || single[0].URL != "https://api.fiken.no/x" || single[0].Filename != "a.pdf" {
		t.Fatalf("single = %+v", single)
	}

	list := collectDocumentRefs(json.RawMessage(`[{"downloadUrl":"https://api.fiken.no/a"},{"noUrl":true}]`))
	if len(list) != 1 || list[0].URL != "https://api.fiken.no/a" {
		t.Fatalf("list = %+v", list)
	}

	if refs := collectDocumentRefs(json.RawMessage(`{"documentUrlWithFikenNormalUserCredentials":"https://fiken.no/login"}`)); len(refs) != 0 {
		t.Fatalf("the normal-user-credentials URL is not usable with an API token, got %+v", refs)
	}
}

// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — keeps the audit vocabulary and the skills in step
// (issue #27). The skills tell an agent which operation to log; an operation
// the seed set does not know prints a warning on every call, which is noise
// the agent learns to ignore. This test reads the skill files and fails on any
// operation name they prescribe that log-event would warn about.
package fikencore

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The three ways a skill names an operation: a `--operation x.y` flag in a
// command, a logEvent('x.y', …) call in the script, and a backticked `x.y`
// on a line that talks about logging.
var (
	skillOpFlag     = regexp.MustCompile(`--operation[= ]+([a-z]+(?:\.[a-z]+)+)`)
	skillOpCall     = regexp.MustCompile(`logEvent\(\s*['"]([a-z]+(?:\.[a-z]+)+)['"]`)
	skillOpBacktick = regexp.MustCompile("`([a-z]+(?:\\.[a-z]+)+)`")
	skillLogLine    = regexp.MustCompile(`(?i)log-event|\blogg|\blog\b|\blogged\b`)
)

func skillOperations(t *testing.T) map[string][]string {
	t.Helper()
	root := filepath.Join("..", "..", "skills")
	found := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if ext := filepath.Ext(path); ext != ".md" && ext != ".mjs" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(raw), "\n") {
			var ops []string
			for _, m := range skillOpFlag.FindAllStringSubmatch(line, -1) {
				ops = append(ops, m[1])
			}
			for _, m := range skillOpCall.FindAllStringSubmatch(line, -1) {
				ops = append(ops, m[1])
			}
			if skillLogLine.MatchString(line) {
				for _, m := range skillOpBacktick.FindAllStringSubmatch(line, -1) {
					// A backticked file name (flows/x.md, superforing.mjs) has
					// the same shape as an operation; a known extension or a
					// path is not one.
					if strings.Contains(m[0], "/") || isFileName(m[1]) {
						continue
					}
					ops = append(ops, m[1])
				}
			}
			for _, op := range ops {
				found[op] = append(found[op], path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return found
}

func isFileName(s string) bool {
	switch filepath.Ext(s) {
	case ".md", ".mjs", ".js", ".json", ".go", ".sh", ".db", ".pdf", ".yaml", ".toml", ".local", ".no", ".com":
		return true
	}
	return false
}

func TestSkillOperationsAreKnown(t *testing.T) {
	found := skillOperations(t)
	if len(found) == 0 {
		t.Fatal("found no operation names under skills/; the scan is broken")
	}
	for op, files := range found {
		if !OperationGrammarValid(op) {
			t.Errorf("skill operation %q (%s) violates the noun.verb grammar", op, files[0])
			continue
		}
		if !OperationKnown(op) {
			t.Errorf("skill operation %q (%s) is not in knownOperations: log-event would warn on every call", op, files[0])
		}
	}
	// The scan must see the operations the Superføring flows are built on; if
	// it stops finding them the test above passes vacuously.
	for _, op := range []string{OpPurchaseCreated, OpInboxDeleted, OpMatchConfirmed} {
		if _, ok := found[op]; !ok {
			t.Errorf("the scan no longer finds %q in the skills", op)
		}
	}
}

func TestSuperforingOperationsKnown(t *testing.T) {
	for _, op := range []string{"purchase.created", "inbox.deleted", "attachment.added", "match.reversed", "contact.renamed"} {
		if !OperationKnown(op) {
			t.Errorf("%q must be known without a warning", op)
		}
	}
}

// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Is this binary current, and is it new enough for the skills on disk (issue
// #32)? A host ran one release behind for two months while its skills, a
// symlink into a clone, described the next one; nothing said so, and the skill
// fell back to doing by hand what the newer binary does. The skills then
// carried a manual check (version against the releases API), which is a step
// an agent can skip. `doctor` and `version --check` now do both checks:
//
//   - the latest GitHub release, compared with this binary. The answer is
//     cached for a day, the request has a short timeout, and any failure
//     (offline, rate limited, no release) yields nothing rather than an error:
//     a health check must not hang or fail because GitHub did.
//   - each installed fiken skill's minimum CLI version, read from its
//     frontmatter (`metadata.fiken-cli.requires`), against this binary.
//
// The request carries nothing about the user: a fixed URL and a User-Agent of
// the CLI's name and version.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"fiken-cli/internal/cliutil"
)

// latestReleaseURL is the endpoint asked for the newest release. A variable so
// a test can point it at a local server.
var latestReleaseURL = "https://api.github.com/repos/nytafar/fiken-cli/releases/latest"

const (
	upgradeCommand         = "curl -fsSL https://raw.githubusercontent.com/nytafar/fiken-cli/main/install.sh | bash"
	releaseCheckTTL        = 24 * time.Hour
	releaseCheckTimeout    = 3 * time.Second
	releaseCheckDisableEnv = "FIKEN_NO_UPDATE_CHECK"
)

// releaseCache is what the last successful lookup left on disk.
type releaseCache struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

func releaseCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "fiken-cli", "latest-release.json")
}

// parseVersion reads a `v2.3.0` / `2.3.0` / `2.3.0-rc1` version into its
// numeric parts. ok is false for anything else (a dev build's "dev", say),
// and an unreadable version is never compared.
func parseVersion(v string) (parts [3]int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	fields := strings.Split(v, ".")
	if v == "" || len(fields) > 3 {
		return parts, false
	}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}

// versionLess reports a < b. Either side unreadable is "not less": nothing is
// claimed about a version that cannot be read.
func versionLess(a, b string) bool {
	pa, okA := parseVersion(a)
	pb, okB := parseVersion(b)
	if !okA || !okB {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

// latestRelease returns the newest release's version without the leading v,
// or "" when it is not known. It reads the day-old cache first and asks GitHub
// only when that is missing or stale; every failure is silent.
func latestRelease(ctx context.Context) string {
	if latestReleaseURL == "" || os.Getenv(releaseCheckDisableEnv) != "" || cliutil.IsVerifyEnv() {
		return ""
	}
	path := releaseCachePath()
	var cached releaseCache
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil && json.Unmarshal(raw, &cached) == nil {
			if cached.Latest != "" && nowFunc().Sub(cached.CheckedAt) < releaseCheckTTL {
				return cached.Latest
			}
		}
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, releaseCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return cached.Latest
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "fiken-cli/"+version)
	resp, err := (&http.Client{Timeout: releaseCheckTimeout}).Do(req)
	if err != nil {
		return cached.Latest // offline: the last known answer, or nothing
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return cached.Latest
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || json.Unmarshal(raw, &body) != nil {
		return cached.Latest
	}
	latest := strings.TrimPrefix(strings.TrimSpace(body.TagName), "v")
	if _, ok := parseVersion(latest); !ok {
		return cached.Latest
	}
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			if out, merr := json.Marshal(releaseCache{Latest: latest, CheckedAt: nowFunc()}); merr == nil {
				_ = os.WriteFile(path, out, 0o644)
			}
		}
	}
	return latest
}

// skillRequirement is one installed skill's claim on the binary.
type skillRequirement struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Requires  string `json:"requires,omitempty"`
	Satisfied bool   `json:"satisfied"`
}

// skillRoots lists the directories skills are installed into: the override
// install.sh honours, then the agent homes `npx skills add` writes to, then a
// project-level directory.
func skillRoots() []string {
	var roots []string
	if dir := os.Getenv("FIKEN_SKILLS_ROOT"); dir != "" {
		roots = append(roots, dir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".agents", "skills"))
	}
	return append(roots, filepath.Join(".claude", "skills"))
}

// skillFrontmatter reads a SKILL.md's `name` and its
// `metadata.fiken-cli.requires`. The frontmatter is a small, flat YAML block,
// so it is read by indentation rather than with a YAML library: `requires:`
// counts only when its nearest less-indented parent is `fiken-cli:`.
func skillFrontmatter(content string) (name, requires string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	type level struct {
		indent int
		key    string
	}
	var stack []level
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "- ") {
			continue
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch {
		case len(stack) == 0 && key == "name":
			name = value
		case key == "requires" && value != "" && len(stack) >= 2 &&
			stack[len(stack)-1].key == "fiken-cli" && stack[len(stack)-2].key == "metadata":
			requires = value
		}
		stack = append(stack, level{indent: indent, key: strings.TrimSpace(key)})
	}
	return name, requires
}

// installedSkillRequirements finds the fiken skills on disk and compares what
// each requires with this binary. A skill that states no minimum is listed as
// satisfied with an empty `requires`: it makes no claim to check.
func installedSkillRequirements(binary string, roots []string) []skillRequirement {
	out := []skillRequirement{}
	seen := map[string]bool{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			file := filepath.Join(root, entry.Name(), "SKILL.md")
			raw, err := os.ReadFile(file) // follows a symlinked skill directory
			if err != nil {
				continue
			}
			name, requires := skillFrontmatter(string(raw))
			if name != "fiken" && !strings.HasPrefix(name, "fiken-") {
				continue
			}
			resolved := file
			if real, rerr := filepath.EvalSymlinks(file); rerr == nil {
				resolved = real
			}
			if seen[resolved] {
				continue
			}
			seen[resolved] = true
			out = append(out, skillRequirement{
				Name: name, Path: file, Requires: requires,
				Satisfied: requires == "" || !versionLess(binary, requires),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// addVersionChecks fills the doctor report: `latest_release` and
// `update_available` (absent and false when the release could not be read),
// `upgrade_command` when behind either a release or a skill, and `skills`.
func addVersionChecks(ctx context.Context, report map[string]any) {
	report["update_available"] = false
	if latest := latestRelease(ctx); latest != "" {
		report["latest_release"] = latest
		if versionLess(version, latest) {
			report["update_available"] = true
			report["upgrade_command"] = upgradeCommand
		}
	}
	skills := installedSkillRequirements(version, skillRoots())
	report["skills"] = skills
	for _, s := range skills {
		if !s.Satisfied {
			report["upgrade_command"] = upgradeCommand
		}
	}
}

// renderVersionChecks prints the two checks in the human doctor output.
func renderVersionChecks(w io.Writer, report map[string]any) {
	latest, _ := report["latest_release"].(string)
	switch {
	case report["update_available"] == true:
		fmt.Fprintf(w, "  %s Release: %s is out, this binary is %s\n", yellow("WARN"), latest, version)
	case latest != "":
		fmt.Fprintf(w, "  %s Release: %s is the latest release, this binary is %s\n", green("OK"), latest, version)
	}
	skills, _ := report["skills"].([]skillRequirement)
	for _, s := range skills {
		switch {
		case !s.Satisfied:
			fmt.Fprintf(w, "  %s Skill %s needs fiken-cli %s or newer, this binary is %s (%s)\n", yellow("WARN"), s.Name, s.Requires, version, s.Path)
		case s.Requires != "":
			fmt.Fprintf(w, "  %s Skill %s needs fiken-cli %s\n", green("OK"), s.Name, s.Requires)
		}
	}
	if cmd, ok := report["upgrade_command"].(string); ok {
		fmt.Fprintf(w, "    upgrade: %s\n", cmd)
	}
}

// addVersionCheckFlag gives the generated `version` command a --check flag
// without editing it: plain `version` prints what it always did, and
// `version --check` adds the release and skill checks.
func addVersionCheckFlag(root *cobra.Command, flags *rootFlags) {
	for _, cmd := range root.Commands() {
		if cmd.Name() != "version" {
			continue
		}
		var check bool
		cmd.Flags().BoolVar(&check, "check", false, "Also compare with the latest release and with what the installed skills require")
		plain := cmd.Run
		cmd.Run = nil
		cmd.RunE = func(c *cobra.Command, args []string) error {
			if !check {
				if plain != nil {
					plain(c, args)
				}
				return nil
			}
			report := map[string]any{"version": version}
			addVersionChecks(c.Context(), report)
			if flags.asJSON || !isTerminal(c.OutOrStdout()) {
				return printJSONFiltered(c.OutOrStdout(), report, flags)
			}
			w := c.OutOrStdout()
			fmt.Fprintf(w, "%s %s\n", c.Root().Name(), version)
			if _, known := report["latest_release"]; !known {
				fmt.Fprintln(w, "  latest release: not known (offline, or the check is disabled)")
			}
			renderVersionChecks(w, report)
			return nil
		}
	}
}

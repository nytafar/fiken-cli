// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — tests for the release and skill version checks
// (issue #32). No test reaches GitHub: the init below empties the release URL
// for the whole test binary, and the tests that exercise the lookup point it
// at a local server.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func init() { latestReleaseURL = "" }

func TestVersionLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"2.1.0", "2.2.0", true},
		{"2.2.0", "2.2.0", false},
		{"2.2.0", "2.1.9", false},
		{"2.9.0", "2.10.0", true}, // numeric, not lexical
		{"v2.1.0", "2.2", true},
		{"2.2.0-rc1", "2.2.0", false},
		{"dev", "2.2.0", false}, // an unreadable version is never called old
		{"2.2.0", "", false},
	}
	for _, tt := range tests {
		if got := versionLess(tt.a, tt.b); got != tt.want {
			t.Errorf("versionLess(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSkillFrontmatter(t *testing.T) {
	tests := []struct{ name, content, wantName, wantRequires string }{
		{"metadata.fiken-cli.requires", "---\nname: fiken\nmetadata:\n  fiken-cli:\n    requires: \"2.3.0\"\n  openclaw:\n    requires:\n      bins:\n        - fiken-cli\n---\n# x", "fiken", "2.3.0"},
		{"another tool's requires is not ours", "---\nname: fiken\nmetadata:\n  openclaw:\n    requires:\n      bins:\n        - fiken-cli\n---\n", "fiken", ""},
		{"a top-level requires is not ours", "---\nname: fiken\nrequires: \"9.9.9\"\n---\n", "fiken", ""},
		{"unquoted, after other keys", "---\nname: fiken-superforing\ndescription: \"a: b\"\nmetadata:\n  fiken-cli:\n    requires: 2.2.0\n---\n", "fiken-superforing", "2.2.0"},
		{"no frontmatter", "# Just a document\nname: fiken\n", "", ""},
		{"requires in the body is ignored", "---\nname: fiken\n---\nmetadata:\n  fiken-cli:\n    requires: 9.9.9\n", "fiken", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, requires := skillFrontmatter(tt.content)
			if name != tt.wantName || requires != tt.wantRequires {
				t.Errorf("got (%q, %q), want (%q, %q)", name, requires, tt.wantName, tt.wantRequires)
			}
		})
	}
}

func writeSkill(t *testing.T, root, dir, name, requires string) {
	t.Helper()
	content := "---\nname: " + name + "\n"
	if requires != "" {
		content += "metadata:\n  fiken-cli:\n    requires: \"" + requires + "\"\n"
	}
	content += "---\n# " + name + "\n"
	if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstalledSkillRequirements(t *testing.T) {
	clone := t.TempDir()
	writeSkill(t, clone, "fiken", "fiken", "2.3.0")
	root := t.TempDir()
	writeSkill(t, root, "fiken-superforing", "fiken-superforing", "2.2.0")
	writeSkill(t, root, "fiken-old", "fiken-old", "")
	writeSkill(t, root, "other", "something-else", "9.0.0")
	// The skill as a host really has it: a symlink into a clone, reachable
	// from two roots. It is one skill, listed once.
	if err := os.Symlink(filepath.Join(clone, "fiken"), filepath.Join(root, "fiken")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got := installedSkillRequirements("2.2.0", []string{root, clone, filepath.Join(root, "does-not-exist")})
	if len(got) != 3 {
		t.Fatalf("skills = %+v, want fiken, fiken-old and fiken-superforing, once each", got)
	}
	by := map[string]skillRequirement{}
	for _, s := range got {
		by[s.Name] = s
	}
	if s := by["fiken"]; s.Requires != "2.3.0" || s.Satisfied {
		t.Errorf("fiken = %+v, want unsatisfied: it requires 2.3.0 and the binary is 2.2.0", s)
	}
	if s := by["fiken-superforing"]; s.Requires != "2.2.0" || !s.Satisfied {
		t.Errorf("fiken-superforing = %+v, want satisfied", s)
	}
	if s := by["fiken-old"]; s.Requires != "" || !s.Satisfied {
		t.Errorf("fiken-old = %+v, want satisfied: it states no minimum", s)
	}
}

// releaseServer answers the releases API and counts the requests.
func releaseServer(t *testing.T, tag string) *atomic.Int32 {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.Contains(r.Header.Get("User-Agent"), "@") {
			t.Errorf("User-Agent %q carries an address", r.Header.Get("User-Agent"))
		}
		if tag == "" {
			http.Error(w, "rate limited", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name": "` + tag + `", "name": "release"}`))
	}))
	t.Cleanup(srv.Close)
	old := latestReleaseURL
	latestReleaseURL = srv.URL
	t.Cleanup(func() { latestReleaseURL = old })
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("FIKEN_SKILLS_ROOT", t.TempDir())
	return &hits
}

func TestLatestRelease_CachedForADay(t *testing.T) {
	hits := releaseServer(t, "v9.4.1")
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	old := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = old })

	if got := latestRelease(context.Background()); got != "9.4.1" {
		t.Fatalf("latest = %q, want 9.4.1", got)
	}
	now = now.Add(23 * time.Hour)
	if got := latestRelease(context.Background()); got != "9.4.1" || hits.Load() != 1 {
		t.Fatalf("within a day: latest = %q after %d request(s), want the cached answer and one request", got, hits.Load())
	}
	now = now.Add(2 * time.Hour)
	if latestRelease(context.Background()); hits.Load() != 2 {
		t.Fatalf("after a day: %d request(s), want a second one", hits.Load())
	}
}

func TestLatestRelease_FailuresAreSilent(t *testing.T) {
	releaseServer(t, "") // answers 403
	if got := latestRelease(context.Background()); got != "" {
		t.Errorf("a refused lookup = %q, want nothing", got)
	}
	latestReleaseURL = "http://127.0.0.1:1/unreachable"
	if got := latestRelease(context.Background()); got != "" {
		t.Errorf("an unreachable host = %q, want nothing", got)
	}
	hits := releaseServer(t, "v9.0.0")
	t.Setenv(releaseCheckDisableEnv, "1")
	if got := latestRelease(context.Background()); got != "" || hits.Load() != 0 {
		t.Errorf("disabled check = %q after %d request(s), want nothing and no request", got, hits.Load())
	}
}

func TestAddVersionChecks(t *testing.T) {
	releaseServer(t, "v99.0.0")
	writeSkill(t, os.Getenv("FIKEN_SKILLS_ROOT"), "fiken", "fiken", "98.0.0")

	report := map[string]any{}
	addVersionChecks(context.Background(), report)
	if report["latest_release"] != "99.0.0" || report["update_available"] != true || report["upgrade_command"] != upgradeCommand {
		t.Fatalf("report = %v, want an update to 99.0.0 with the upgrade command", report)
	}
	raw, _ := json.Marshal(report["skills"])
	if !strings.Contains(string(raw), `"requires":"98.0.0"`) || !strings.Contains(string(raw), `"satisfied":false`) {
		t.Fatalf("skills = %s, want fiken requiring 98.0.0, unsatisfied", raw)
	}
	var human bytes.Buffer
	renderVersionChecks(&human, report)
	for _, want := range []string{"99.0.0 is out", "Skill fiken needs fiken-cli 98.0.0", "upgrade: curl"} {
		if !strings.Contains(human.String(), want) {
			t.Errorf("human output lacks %q:\n%s", want, human.String())
		}
	}

	// Offline: no latest_release key, update_available false, never an error.
	latestReleaseURL = ""
	offline := map[string]any{}
	addVersionChecks(context.Background(), offline)
	if _, has := offline["latest_release"]; has || offline["update_available"] != false {
		t.Fatalf("offline report = %v, want no latest_release and update_available false", offline)
	}
}

func TestDoctorJSON_CarriesTheVersionChecks(t *testing.T) {
	releaseServer(t, "v"+version)
	writeSkill(t, os.Getenv("FIKEN_SKILLS_ROOT"), "fiken", "fiken", version)
	t.Setenv("FIKEN_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	var out bytes.Buffer
	root := RootCmd()
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"doctor", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	var report struct {
		Latest          string             `json:"latest_release"`
		UpdateAvailable *bool              `json:"update_available"`
		Skills          []skillRequirement `json:"skills"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, out.String())
	}
	if report.Latest != version || report.UpdateAvailable == nil || *report.UpdateAvailable {
		t.Fatalf("report = %+v, want latest_release %s and update_available false", report, version)
	}
	if len(report.Skills) != 1 || report.Skills[0].Requires != version || !report.Skills[0].Satisfied {
		t.Fatalf("skills = %+v, want the one skill, requiring %s, satisfied", report.Skills, version)
	}
}

// The skills shipped in this repo state the CLI version they need, and never
// one newer than the code beside them: a skill ahead of its binary is the
// situation the check exists to report.
func TestRepoSkillsStateTheirMinimumVersion(t *testing.T) {
	skills := installedSkillRequirements(version, []string{filepath.Join("..", "..", "skills")})
	if len(skills) < 2 {
		t.Fatalf("found %d fiken skills under skills/, want fiken and fiken-superforing", len(skills))
	}
	for _, s := range skills {
		if s.Requires == "" {
			t.Errorf("skill %s states no metadata.fiken-cli.requires", s.Name)
		}
		if !s.Satisfied {
			t.Errorf("skill %s requires %s, newer than this code's %s", s.Name, s.Requires, version)
		}
	}
}

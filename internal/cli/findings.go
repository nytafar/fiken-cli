// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — the one finding envelope every detector emits, plus
// the shared report flags (--period/--min-impact-ore/--limit) and the pure
// filter→sort→summarize→limit pipeline they run. Before this, each detector
// invented its own row type, its own period closure and its own text layout;
// an agent had to learn six output dialects and the summary never matched what
// was printed. Report is emitted as the `results` payload of the existing
// provenance envelope, so MCP callers see one shape per detector.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"fiken-cli/internal/fikencore"
)

// Severity reuses the fikencore validation vocabulary, extended with "info"
// for findings that are observations rather than problems.
type Severity string

const (
	SeverityError   Severity = fikencore.SeverityError   // "error"
	SeverityWarning Severity = fikencore.SeverityWarning // "warning"
	SeverityInfo    Severity = "info"
)

// severityRank orders severities so a group can report the worst of its
// members. Unknown severities sort below "info".
func severityRank(s Severity) int {
	switch s {
	case SeverityError:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

// Finding is one detector hit. ImpactOre is first-class and always present:
// it is what every detector sorts and thresholds on, so a caller can rank
// findings across detectors without knowing any of them.
type Finding struct {
	Kind        string         `json:"kind"`
	Severity    Severity       `json:"severity"`
	DocType     string         `json:"doc_type"`
	DocID       int64          `json:"doc_id"`
	Date        string         `json:"date"`
	ContactID   int64          `json:"contact_id,omitempty"`
	ContactName string         `json:"contact_name,omitempty"`
	Description string         `json:"description,omitempty"`
	Account     string         `json:"account,omitempty"`
	VATType     string         `json:"vat_type,omitempty"`
	ImpactOre   int64          `json:"impact_ore"`
	Detail      map[string]any `json:"detail,omitempty"`
}

// Window is the resolved period a report covers. Source records how it was
// chosen so a caller can tell an explicit --period from the default.
type Window struct {
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Source string `json:"source"` // flag | default_current_year | all
}

// Contains reports whether a YYYY-MM-DD date falls inside the window. An empty
// date is never contained (the caller counts it as undated instead, so nothing
// disappears silently); an empty bound is unbounded on that side.
func (w Window) Contains(date string) bool {
	if date == "" {
		return false
	}
	if w.From != "" && date < w.From {
		return false
	}
	if w.To != "" && date > w.To {
		return false
	}
	return true
}

// String renders the window for the text header.
func (w Window) String() string {
	switch {
	case w.From == "" && w.To == "":
		return "all dates"
	case w.From == "":
		return "through " + w.To
	case w.To == "":
		return "from " + w.From
	default:
		return w.From + ".." + w.To
	}
}

// Group is one row of a report's summary: the findings sharing a key, their
// count, their summed impact and the worst severity among them.
type Group struct {
	Key       map[string]string `json:"key"`
	Count     int               `json:"count"`
	ImpactOre int64             `json:"impact_ore"`
	Severity  Severity          `json:"severity"`
}

// Report is the payload every detector emits. Total and Summary describe the
// whole (post --min-impact-ore) finding set; Findings is the --limit-truncated
// slice actually shown.
type Report struct {
	Company  string         `json:"company"`
	Detector string         `json:"detector"`
	Window   Window         `json:"window"`
	Total    int            `json:"total_findings"`
	Undated  int            `json:"undated_documents"`
	Params   map[string]any `json:"params,omitempty"`
	Summary  []Group        `json:"summary"`
	Findings []Finding      `json:"findings"`
}

// SortFindings orders findings by |impact| desc, then date asc, then
// docType/docID so the order is total and reproducible.
func SortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if ai, bi := absInt64(a.ImpactOre), absInt64(b.ImpactOre); ai != bi {
			return ai > bi
		}
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		if a.DocType != b.DocType {
			return a.DocType < b.DocType
		}
		if a.DocID != b.DocID {
			return a.DocID < b.DocID
		}
		return a.Kind < b.Kind
	})
}

// groupKeyString canonicalizes a key map for stable grouping and tie-breaking.
func groupKeyString(key map[string]string) string {
	names := make([]string, 0, len(key))
	for k := range key {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, k := range names {
		parts = append(parts, k+"="+key[k])
	}
	return strings.Join(parts, " ")
}

// Summarize buckets findings by keyFn. Empty key values are dropped so a key
// a detector leaves blank (e.g. vat_type on a header mismatch) does not split
// the bucket. Groups are ordered by |impact| desc, then key string.
func Summarize(fs []Finding, keyFn func(Finding) map[string]string) []Group {
	if keyFn == nil {
		return nil
	}
	index := map[string]int{}
	var groups []Group
	for _, f := range fs {
		key := map[string]string{}
		for k, v := range keyFn(f) {
			if v != "" {
				key[k] = v
			}
		}
		s := groupKeyString(key)
		i, ok := index[s]
		if !ok {
			i = len(groups)
			index[s] = i
			groups = append(groups, Group{Key: key, Severity: f.Severity})
		}
		groups[i].Count++
		groups[i].ImpactOre += f.ImpactOre
		if severityRank(f.Severity) > severityRank(groups[i].Severity) {
			groups[i].Severity = f.Severity
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if a, b := absInt64(groups[i].ImpactOre), absInt64(groups[j].ImpactOre); a != b {
			return a > b
		}
		return groupKeyString(groups[i].Key) < groupKeyString(groups[j].Key)
	})
	return groups
}

// buildReport is the pure core of finishReport: drop findings below
// minImpact, sort, summarize the survivors, record the true total, then
// truncate to limit. Total and Summary always describe the untruncated set.
func buildReport(r Report, fs []Finding, keyFn func(Finding) map[string]string, minImpact int64, limit int) Report {
	kept := make([]Finding, 0, len(fs))
	for _, f := range fs {
		if absInt64(f.ImpactOre) < minImpact {
			continue
		}
		kept = append(kept, f)
	}
	SortFindings(kept)
	r.Total = len(kept)
	r.Summary = Summarize(kept, keyFn)
	if r.Summary == nil {
		// An empty report should serialize as [] like Findings does, so a
		// consumer never has to handle both null and [].
		r.Summary = []Group{}
	}
	if limit > 0 && len(kept) > limit {
		kept = kept[:limit]
	}
	r.Findings = kept
	return r
}

// RenderText writes the human view: a header line, the summary block, then the
// (already truncated) findings. limit is used only to say so when the list was
// cut short.
func RenderText(w io.Writer, r Report, limit int) {
	fmt.Fprintf(w, "%s — %s — %s (%s)\n", r.Detector, r.Company, r.Window.String(), r.Window.Source)
	fmt.Fprintf(w, "%d findings", r.Total)
	if r.Undated > 0 {
		fmt.Fprintf(w, ", %d undated document(s) skipped", r.Undated)
	}
	for _, k := range sortedParamKeys(r.Params) {
		// Structured params (bank-unverified's per-account table) belong to
		// JSON consumers; the header line only carries scalar knobs.
		switch r.Params[k].(type) {
		case map[string]any, []any, []map[string]any:
			continue
		}
		fmt.Fprintf(w, ", %s %v", k, r.Params[k])
	}
	fmt.Fprintln(w)
	if r.Total == 0 {
		fmt.Fprintln(w, "\nNothing found.")
		return
	}
	fmt.Fprintln(w, "\nSummary")
	for _, g := range r.Summary {
		fmt.Fprintf(w, "  %-9s %5d  %12s kr  %s\n", g.Severity, g.Count, kr(g.ImpactOre), groupKeyString(g.Key))
	}
	fmt.Fprintf(w, "\nFindings (%d of %d)\n", len(r.Findings), r.Total)
	for _, f := range r.Findings {
		fmt.Fprintf(w, "  %-9s %-26s %s #%d  %s  %s kr\n", f.Severity, f.Kind, f.DocType, f.DocID, f.Date, kr(f.ImpactOre))
		if line := findingDetailLine(f); line != "" {
			fmt.Fprintf(w, "      %s\n", line)
		}
	}
	if limit > 0 && r.Total > len(r.Findings) {
		fmt.Fprintf(w, "\n%d more findings not shown (--limit %d).\n", r.Total-len(r.Findings), limit)
	}
}

// findingDetailLine is the optional second line of a rendered finding: the
// context that identifies it without repeating the first line.
func findingDetailLine(f Finding) string {
	var parts []string
	if f.Account != "" {
		parts = append(parts, "account "+f.Account)
	}
	if f.VATType != "" {
		parts = append(parts, "vatType "+f.VATType)
	}
	if f.ContactName != "" {
		parts = append(parts, f.ContactName)
	}
	if f.Description != "" {
		parts = append(parts, f.Description)
	}
	if note, ok := f.Detail["note"].(string); ok && note != "" {
		parts = append(parts, note)
	}
	return strings.Join(parts, "  ")
}

func sortedParamKeys(p map[string]any) []string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// reportOpts holds the flags every Report-emitting detector shares.
type reportOpts struct {
	period       string
	minImpactOre int64
	limit        int
}

// addReportFlags registers the shared detector flags with one wording, so
// --period means the same thing everywhere.
func addReportFlags(cmd *cobra.Command, opts *reportOpts) {
	cmd.Flags().StringVar(&opts.period, "period", "", "Period: YYYY, YYYY-MM, YYYY-Qn, from:to, or all (default: current year)")
	cmd.Flags().Int64Var(&opts.minImpactOre, "min-impact-ore", 0, "Drop findings whose absolute impact is below this many øre")
	cmd.Flags().IntVar(&opts.limit, "limit", 200, "Max findings to emit; the summary and total still cover all of them")
}

// finishReport runs the shared pipeline (filter → sort → summarize → limit)
// and emits the result in the caller's chosen format.
func finishReport(cmd *cobra.Command, flags *rootFlags, r Report, fs []Finding, keyFn func(Finding) map[string]string, opts reportOpts) error {
	r = buildReport(r, fs, keyFn, opts.minImpactOre, opts.limit)
	return emitReport(cmd, flags, r, opts.limit)
}

// emitReport writes the Report as JSON inside the standard provenance envelope
// for machine consumers, else renders the text view. The JSON gate mirrors
// emitFiken so the detectors keep one output-mode rule.
func emitReport(cmd *cobra.Command, flags *rootFlags, r Report, limit int) error {
	out := cmd.OutOrStdout()
	if !(flags.asJSON || flags.agent || flags.csv || flags.quiet || !isTerminal(out)) {
		RenderText(out, r, limit)
		return nil
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	data := json.RawMessage(raw)
	// --csv/--quiet asked for a non-JSON format: no envelope, standard pipeline.
	if flags.csv || flags.quiet {
		return printOutputWithFlags(out, data, flags)
	}
	if flags.selectFields != "" {
		data = filterFields(data, flags.selectFields)
	} else if flags.compact {
		data = compactFields(data)
	}
	// synced_at is deliberately omitted: the mirror's only accessor is
	// per-resource-type and string-typed, so there is no one honest timestamp
	// for a report that reads several resource types.
	wrapped, err := wrapWithProvenance(data, DataProvenance{Source: "local", Reason: "user_requested"})
	if err != nil {
		return err
	}
	return printOutput(out, wrapped, true)
}

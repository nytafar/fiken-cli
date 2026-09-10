// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — fill-in of the generator's verify-friendly stub
// (skip-if-exists on regen). Surfaces suspected double-postings: within
// purchases (and, separately, sales), documents that share the same counterpart
// contact AND the same gross øre amount AND fall within a date window of each
// other are grouped as a suspected duplicate. Emits the shared Report envelope,
// one Finding per suspected-duplicate group. Reads only the local mirror.
package cli

// pp:data-source local

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"fiken-cli/internal/fikencore"
)

type duplicateDoc struct {
	DocID int64  `json:"doc_id"`
	Date  string `json:"date"`
}

type duplicateGroup struct {
	DocType     string         `json:"doc_type"` // purchase | sale
	ContactID   int64          `json:"contact_id,omitempty"`
	ContactName string         `json:"contact_name,omitempty"`
	AmountOre   int64          `json:"amount_ore"`
	Docs        []duplicateDoc `json:"docs"`
}

// dupCandidate is the minimal shape the detector needs.
type dupCandidate struct {
	DocType     string
	DocID       int64
	Date        string
	ContactID   int64
	ContactName string
	GrossOre    int64
}

func parseDay(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", s)
	return t, err == nil
}

// findDuplicateGroups is the pure detector. It clusters candidates that share
// (docType, contactID, grossOre) and whose dates are all within windowDays of
// at least one neighbour in the cluster. A simple transitive single-link
// clustering over the sorted-by-date members within each exact-key bucket: two
// adjacent members chain into the same group when their gap <= windowDays.
func findDuplicateGroups(cands []dupCandidate, windowDays int) []duplicateGroup {
	type key struct {
		docType   string
		contactID int64
		gross     int64
	}
	buckets := map[key][]dupCandidate{}
	for _, c := range cands {
		k := key{c.DocType, c.ContactID, c.GrossOre}
		buckets[k] = append(buckets[k], c)
	}

	var groups []duplicateGroup
	for k, members := range buckets {
		if len(members) < 2 {
			continue
		}
		sort.SliceStable(members, func(i, j int) bool { return members[i].Date < members[j].Date })
		// Single-link chain by date gap.
		var run []dupCandidate
		flush := func() {
			if len(run) >= 2 {
				docs := make([]duplicateDoc, 0, len(run))
				for _, m := range run {
					docs = append(docs, duplicateDoc{DocID: m.DocID, Date: m.Date})
				}
				groups = append(groups, duplicateGroup{
					DocType:     k.docType,
					ContactID:   k.contactID,
					ContactName: run[0].ContactName,
					AmountOre:   k.gross,
					Docs:        docs,
				})
			}
			run = nil
		}
		for i, m := range members {
			if i == 0 {
				run = []dupCandidate{m}
				continue
			}
			prev := members[i-1]
			pt, pok := parseDay(prev.Date)
			ct, cok := parseDay(m.Date)
			within := pok && cok && absDays(ct, pt) <= windowDays
			if within {
				run = append(run, m)
			} else {
				flush()
				run = []dupCandidate{m}
			}
		}
		flush()
	}

	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].DocType != groups[j].DocType {
			return groups[i].DocType < groups[j].DocType
		}
		if groups[i].Docs[0].Date != groups[j].Docs[0].Date {
			return groups[i].Docs[0].Date < groups[j].Docs[0].Date
		}
		return groups[i].AmountOre < groups[j].AmountOre
	})
	return groups
}

// duplicateFindings adapts the pure detector's groups into the shared Finding
// envelope: one finding per group, identified by its earliest document, with
// the shared gross amount as the impact — the money at stake if the document
// really was booked twice. Pure, so a test can cover the mapping.
func duplicateFindings(groups []duplicateGroup) []Finding {
	findings := make([]Finding, 0, len(groups))
	for _, g := range groups {
		if len(g.Docs) == 0 {
			continue
		}
		ids := make([]int64, 0, len(g.Docs))
		dates := make([]string, 0, len(g.Docs))
		for _, d := range g.Docs {
			ids = append(ids, d.DocID)
			dates = append(dates, d.Date)
		}
		findings = append(findings, Finding{
			Kind:        "duplicate_group",
			Severity:    SeverityWarning,
			DocType:     g.DocType,
			DocID:       g.Docs[0].DocID,
			Date:        g.Docs[0].Date,
			ContactID:   g.ContactID,
			ContactName: g.ContactName,
			ImpactOre:   g.AmountOre,
			Detail: map[string]any{
				"doc_ids":    ids,
				"dates":      dates,
				"group_size": len(g.Docs),
			},
		})
	}
	return findings
}

func absDays(a, b time.Time) int {
	d := int(a.Sub(b).Hours() / 24)
	if d < 0 {
		return -d
	}
	return d
}

func newNovelDuplicatesCmd(flags *rootFlags) *cobra.Command {
	var flagCompany string
	var dbPath string
	var windowDays int
	var opts reportOpts

	cmd := &cobra.Command{
		Use:   "duplicates",
		Short: "Find suspected double-posted purchases or sales",
		Long: "Within purchases (and separately within sales), groups documents that share the same\n" +
			"counterpart contact and the same gross amount and fall within --window-days of each other —\n" +
			"the classic signature of a double-booked bilag. Reads the local mirror.",
		Example: strings.Trim(`
  fiken-cli duplicates --company fiken-demo
  fiken-cli duplicates --company fiken-demo --window-days 3 --period 2026 --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			win, err := resolvePeriod(opts.period)
			if err != nil {
				return err
			}
			if windowDays < 0 {
				return fmt.Errorf("--window-days must be >= 0, got %d", windowDays)
			}
			if dryRunOK(flags) {
				return nil
			}
			db, err := openMirror(cmd.Context(), dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			slug, err := resolveCompanySlug(db, flagCompany)
			if err != nil {
				return err
			}

			// The gross a line contributes depends on its VAT regime (net+vat
			// only for ordinary domestic VAT), so the regime authority answers
			// it — otherwise a reverse-charge purchase and an ordinary one of a
			// different size collide into one duplicate bucket. An
			// unrecognised vatType falls back to net+vat.
			lineGross := func(m map[string]json.RawMessage) int64 {
				var g int64
				for _, ln := range jsonObjects(m, "lines") {
					net, _ := jsonInt(ln, "netPrice")
					vat, _ := jsonInt(ln, "vat")
					info, _ := fikencore.Lookup(jsonStr(ln, "vatType"))
					g += fikencore.Gross(info, net, vat)
				}
				return g
			}
			contactOf := func(m map[string]json.RawMessage, key string) (int64, string) {
				ent := jsonObject(m, key)
				if ent == nil {
					return 0, ""
				}
				id, _ := jsonInt(ent, "contactId")
				return id, jsonStr(ent, "name")
			}

			var cands []dupCandidate
			undated := 0
			purchases, err := loadCompanyResources(cmd.Context(), db, "purchases", slug)
			if err != nil {
				return err
			}
			for _, p := range purchases {
				date := jsonStr(p, "date")
				if date == "" {
					undated++
					continue
				}
				if !win.Contains(date) {
					continue
				}
				id, _ := jsonInt(p, "purchaseId")
				cid, cname := contactOf(p, "supplier")
				cands = append(cands, dupCandidate{
					DocType: "purchase", DocID: id, Date: date,
					ContactID: cid, ContactName: cname, GrossOre: lineGross(p),
				})
			}
			sales, err := loadCompanyResources(cmd.Context(), db, "sales", slug)
			if err != nil {
				return err
			}
			for _, s := range sales {
				date := jsonStr(s, "date")
				if date == "" {
					undated++
					continue
				}
				if !win.Contains(date) {
					continue
				}
				id, _ := jsonInt(s, "saleId")
				cid, cname := contactOf(s, "customer")
				net, _ := jsonInt(s, "netAmount")
				vat, _ := jsonInt(s, "vatAmount")
				cands = append(cands, dupCandidate{
					DocType: "sale", DocID: id, Date: date,
					ContactID: cid, ContactName: cname, GrossOre: net + vat,
				})
			}

			report := Report{
				Company:  slug,
				Detector: "duplicates",
				Window:   win,
				Undated:  undated,
				Params:   map[string]any{"window_days": windowDays},
			}
			keyFn := func(f Finding) map[string]string {
				return map[string]string{"doc_type": f.DocType}
			}
			groups := findDuplicateGroups(cands, windowDays)
			return finishReport(cmd, flags, report, duplicateFindings(groups), keyFn, opts)
		},
	}
	cmd.Flags().StringVar(&flagCompany, "company", "", "Company slug (default: the single synced company)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Mirror database path (default: ~/.local/share/fiken-cli/data.db)")
	cmd.Flags().IntVar(&windowDays, "window-days", 5, "Max days apart for two documents to count as a duplicate pair")
	addReportFlags(cmd, &opts)
	return cmd
}

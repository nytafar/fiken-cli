// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the pure VAT-anomaly detector.
package cli

import (
	"testing"

	"fiken-cli/internal/fikencore"
)

func TestVatAnomalyScan(t *testing.T) {
	t.Run("empty input yields nothing", func(t *testing.T) {
		if got := vatAnomalyScan(nil, 3); len(got) != 0 {
			t.Fatalf("got %d findings, want 0", len(got))
		}
	})

	t.Run("purchase-only VAT type on a sale is invalid_for_side", func(t *testing.T) {
		// HIGH_DIRECT is valid only for purchases.
		lines := []vatAnomalyLine{{
			DocType: "sale", Side: fikencore.SideSales, DocID: 1, Date: "2026-01-01",
			Account: "3000:1", VATType: "HIGH_DIRECT", Description: "Konsulenttime", VATOre: 2500, InPeriod: true,
		}}
		got := vatAnomalyScan(lines, 3)
		if len(got) != 1 || got[0].Kind != "invalid_for_side" {
			t.Fatalf("got %+v; want one invalid_for_side", got)
		}
		if got[0].Severity != SeverityError {
			t.Errorf("severity = %q; want error (the MVA return is wrong)", got[0].Severity)
		}
		// The line's own VAT is the money at stake, and the line description
		// travels with the finding.
		if got[0].ImpactOre != 2500 || got[0].Description != "Konsulenttime" {
			t.Errorf("finding = %+v; want impact 2500 and the line description", got[0])
		}
	})

	t.Run("valid type with no pattern history yields nothing", func(t *testing.T) {
		lines := []vatAnomalyLine{{
			DocType: "sale", Side: fikencore.SideSales, DocID: 1, Date: "2026-01-01",
			Account: "3000:1", VATType: "HIGH", InPeriod: true,
		}}
		if got := vatAnomalyScan(lines, 3); len(got) != 0 {
			t.Fatalf("got %+v; want none (fewer than 3 samples)", got)
		}
	})

	t.Run("deviation from modal flagged once group has >=3 samples", func(t *testing.T) {
		mk := func(id int64, vt string, inPeriod bool) vatAnomalyLine {
			return vatAnomalyLine{
				DocType: "purchase", Side: fikencore.SidePurchases, DocID: id, Date: "2026-03-0" + string(rune('0'+id)),
				ContactID: 42, ContactName: "Strøm AS", Account: "4000:1", VATType: vt, InPeriod: inPeriod,
			}
		}
		// 3 historical HIGH + 1 in-period LOW deviation = 4 samples for the pair.
		lines := []vatAnomalyLine{
			mk(1, "HIGH", false),
			mk(2, "HIGH", false),
			mk(3, "HIGH", false),
			mk(4, "LOW", true),
		}
		got := vatAnomalyScan(lines, 3)
		if len(got) != 1 {
			t.Fatalf("got %d findings %+v; want 1", len(got), got)
		}
		if got[0].Kind != "deviates_from_vendor_pattern" {
			t.Errorf("kind = %q; want deviates_from_vendor_pattern", got[0].Kind)
		}
		if got[0].Severity != SeverityWarning {
			t.Errorf("severity = %q; want warning", got[0].Severity)
		}
		if got[0].Detail["expected_vat_type"] != "HIGH" {
			t.Errorf("expected modal = %v; want HIGH", got[0].Detail["expected_vat_type"])
		}
	})

	t.Run("only in-period lines are reported even if history deviates", func(t *testing.T) {
		mk := func(id int64, vt string, inPeriod bool) vatAnomalyLine {
			return vatAnomalyLine{
				DocType: "purchase", Side: fikencore.SidePurchases, DocID: id, Date: "2025-01-01",
				ContactID: 42, Account: "4000:1", VATType: vt, InPeriod: inPeriod,
			}
		}
		// The deviating LOW line is OUT of period -> no finding.
		lines := []vatAnomalyLine{
			mk(1, "HIGH", false),
			mk(2, "HIGH", false),
			mk(3, "HIGH", false),
			mk(4, "LOW", false),
		}
		if got := vatAnomalyScan(lines, 3); len(got) != 0 {
			t.Fatalf("got %+v; want none (deviation out of period)", got)
		}
	})

	t.Run("a regime change 14 months ago does not taint this year", func(t *testing.T) {
		mk := func(id int64, date, vt string, inPeriod bool) vatAnomalyLine {
			return vatAnomalyLine{
				DocType: "purchase", Side: fikencore.SidePurchases, DocID: id, Date: date,
				ContactID: 7, ContactName: "Foreign Host Ltd", Account: "6810:1",
				Description: "Hosting", VATType: vt, InPeriod: inPeriod,
			}
		}
		// Reverse charge until mid-2025, plain domestic HIGH ever since.
		lines := []vatAnomalyLine{
			mk(1, "2024-09-01", "HIGH_FOREIGN_SERVICE_DEDUCTIBLE", false),
			mk(2, "2024-12-01", "HIGH_FOREIGN_SERVICE_DEDUCTIBLE", false),
			mk(3, "2025-03-01", "HIGH_FOREIGN_SERVICE_DEDUCTIBLE", false),
			mk(4, "2025-07-01", "HIGH", false),
			mk(5, "2025-09-01", "HIGH", false),
			mk(6, "2025-11-01", "HIGH", false),
			mk(7, "2026-02-01", "HIGH", true),
			mk(8, "2026-05-01", "HIGH", true),
		}
		if got := vatAnomalyScan(lines, 3); len(got) != 0 {
			t.Fatalf("got %+v; want none (the old regime is outside the 12-month window)", got)
		}
	})

	t.Run("two products on one contact do not flag each other", func(t *testing.T) {
		mk := func(id int64, date, desc, vt string) vatAnomalyLine {
			return vatAnomalyLine{
				DocType: "purchase", Side: fikencore.SidePurchases, DocID: id, Date: date,
				ContactID: 9, Account: "4300:1", Description: desc, VATType: vt, InPeriod: true,
			}
		}
		// Same contact and account, two descriptions with their own VAT type.
		// The normalised description keeps the groups apart; case and internal
		// whitespace do not split a group.
		lines := []vatAnomalyLine{
			mk(1, "2026-01-05", "Kaffe  bønner", "HIGH"),
			mk(2, "2026-02-05", "kaffe bønner", "HIGH"),
			mk(3, "2026-03-05", "  KAFFE bønner ", "HIGH"),
			mk(4, "2026-04-05", "kaffe bønner", "HIGH"),
			mk(5, "2026-01-06", "Aviser", "LOW"),
			mk(6, "2026-02-06", "Aviser", "LOW"),
			mk(7, "2026-03-06", "Aviser", "LOW"),
			mk(8, "2026-04-06", "Aviser", "LOW"),
		}
		if got := vatAnomalyScan(lines, 3); len(got) != 0 {
			t.Fatalf("got %+v; want none (different products, each internally consistent)", got)
		}
	})

	t.Run("min-samples applies to the support inside the window", func(t *testing.T) {
		mk := func(id int64, date, vt string, inPeriod bool) vatAnomalyLine {
			return vatAnomalyLine{
				DocType: "purchase", Side: fikencore.SidePurchases, DocID: id, Date: date,
				ContactID: 11, Account: "6540:1", Description: "Verktøy", VATType: vt, InPeriod: inPeriod,
			}
		}
		lines := []vatAnomalyLine{
			mk(1, "2026-01-01", "HIGH", false),
			mk(2, "2026-02-01", "HIGH", false),
			mk(3, "2026-03-01", "LOW", true),
		}
		if got := vatAnomalyScan(lines, 3); len(got) != 0 {
			t.Fatalf("got %+v; want none (support 2 < min-samples 3)", got)
		}
		got := vatAnomalyScan(lines, 2)
		if len(got) != 1 {
			t.Fatalf("got %d findings %+v; want 1 at min-samples 2", len(got), got)
		}
		if got[0].Detail["expected_vat_type"] != "HIGH" || got[0].Detail["modal_support"] != 2 {
			t.Errorf("detail = %+v; want expected HIGH with modal_support 2", got[0].Detail)
		}
	})

	t.Run("same-day siblings do not vote on each other", func(t *testing.T) {
		mk := func(id int64, vt string) vatAnomalyLine {
			return vatAnomalyLine{
				DocType: "purchase", Side: fikencore.SidePurchases, DocID: id, Date: "2026-04-01",
				ContactID: 13, Account: "6540:1", Description: "Verktøy", VATType: vt, InPeriod: true,
			}
		}
		// The window is exclusive of the line's own date, so three HIGH lines
		// posted the same day as the LOW one give it a support of 0.
		lines := []vatAnomalyLine{mk(1, "HIGH"), mk(2, "HIGH"), mk(3, "HIGH"), mk(4, "LOW")}
		if got := vatAnomalyScan(lines, 3); len(got) != 0 {
			t.Fatalf("got %+v; want none (same-day lines do not vote)", got)
		}
	})
}

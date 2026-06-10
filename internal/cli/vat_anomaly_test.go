// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the pure VAT-anomaly detector.
package cli

import (
	"testing"

	"fiken-cli/internal/fikencore"
)

func TestModalVATType(t *testing.T) {
	if got, n := modalVATType(nil); got != "" || n != 0 {
		t.Errorf("modalVATType(nil) = %q,%d; want \"\",0", got, n)
	}
	got, n := modalVATType(map[string]int{"HIGH": 3, "LOW": 1})
	if got != "HIGH" || n != 3 {
		t.Errorf("modalVATType = %q,%d; want HIGH,3", got, n)
	}
	// Tie broken by lexicographically smallest type.
	if got, _ := modalVATType(map[string]int{"LOW": 2, "HIGH": 2}); got != "HIGH" {
		t.Errorf("tie modal = %q; want HIGH (lexicographic tie-break)", got)
	}
}

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
			Account: "3000:1", VATType: "HIGH_DIRECT", InPeriod: true,
		}}
		got := vatAnomalyScan(lines, 3)
		if len(got) != 1 || got[0].Reason != "invalid_for_side" {
			t.Fatalf("got %+v; want one invalid_for_side", got)
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
		if got[0].Reason != "deviates_from_vendor_pattern" {
			t.Errorf("reason = %q; want deviates_from_vendor_pattern", got[0].Reason)
		}
		if got[0].ExpectedVATType != "HIGH" {
			t.Errorf("expected modal = %q; want HIGH", got[0].ExpectedVATType)
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
}

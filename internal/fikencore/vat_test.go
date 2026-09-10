// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — tests for the VAT authority: table completeness
// against the two side sets and the numeric codes, the regime/rate table
// itself, the per-regime line invariants, and parity with spec.yaml so a spec
// re-import that adds a vatType fails here instead of silently defaulting.
package fikencore

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// (a) The table and the two side sets are the same set of types.
func TestVATTypeTableCoversBothSideSets(t *testing.T) {
	union := map[string]bool{}
	for ty := range salesVATTypes {
		union[ty] = true
	}
	for ty := range purchaseVATTypes {
		union[ty] = true
	}
	for ty := range union {
		info, ok := Lookup(ty)
		if !ok {
			t.Errorf("vatType %q is in a side set but has no Lookup entry", ty)
			continue
		}
		if info.Type != ty {
			t.Errorf("Lookup(%q).Type = %q", ty, info.Type)
		}
		wantSide := SideBoth
		switch {
		case salesVATTypes[ty] && purchaseVATTypes[ty]:
		case salesVATTypes[ty]:
			wantSide = SideSales
		default:
			wantSide = SidePurchases
		}
		if info.Side != wantSide {
			t.Errorf("Lookup(%q).Side = %q, want %q", ty, info.Side, wantSide)
		}
	}
	for ty := range vatTypes {
		if !union[ty] {
			t.Errorf("vatType %q is in the regime table but in neither side set", ty)
		}
	}
	if len(union) != len(vatTypes) {
		t.Errorf("side sets hold %d types, regime table %d", len(union), len(vatTypes))
	}
	// 20 distinct types across 25 numeric codes (the five both-sided types
	// have a kjøp/salg code pair). Some docs say "22 types"; the spec's own
	// two enum lines say 20.
	if len(vatTypes) != 20 {
		t.Errorf("expected 20 vatTypes, got %d", len(vatTypes))
	}
}

// (b) Every numeric code maps to a type that maps back to that code.
func TestNumericCodesRoundTrip(t *testing.T) {
	for code, ci := range numericVATCodes {
		info, ok := Lookup(ci.Type)
		if !ok {
			t.Errorf("code %d maps to unknown type %q", code, ci.Type)
			continue
		}
		if ci.Side != SideSales && ci.Side != SidePurchases {
			t.Errorf("code %d has side %q; a numeric code is never both", code, ci.Side)
		}
		if !VATTypeValidFor(ci.Type, ci.Side) {
			t.Errorf("code %d: %q is not valid on %s", code, ci.Type, ci.Side)
		}
		back, ok := CodeFor(ci.Type, ci.Side)
		if !ok || back != code {
			t.Errorf("CodeFor(%q, %s) = %d,%v; want %d", ci.Type, ci.Side, back, ok, code)
		}
		if info.Code == 0 && ci.Type != "NONE" {
			t.Errorf("Lookup(%q).Code is 0", ci.Type)
		}
	}
	// The both-sided pairs: Code is the purchases (inngående) member.
	for ty, wantPurchase := range map[string]int{"NONE": 0, "HIGH": 1, "MEDIUM": 11, "RAW_FISH": 12, "LOW": 13} {
		info, _ := Lookup(ty)
		if info.Code != wantPurchase {
			t.Errorf("Lookup(%q).Code = %d, want the purchases code %d", ty, info.Code, wantPurchase)
		}
	}
	if c, _ := CodeFor("HIGH", SideSales); c != 3 {
		t.Errorf("CodeFor(HIGH, sales) = %d, want 3", c)
	}
	if _, ok := CodeFor("HIGH_DIRECT", SideSales); ok {
		t.Error("HIGH_DIRECT has no sales code")
	}
}

// (c) The regime/rate/code of every type, spelled out once.
func TestRegimeTable(t *testing.T) {
	want := []struct {
		ty     string
		regime Regime
		rate   float64
		code   int
	}{
		{"NONE", RegimeZero, 0, 0},
		{"HIGH", RegimeLineVAT, 0.25, 1},
		{"MEDIUM", RegimeLineVAT, 0.15, 11},
		{"RAW_FISH", RegimeLineVAT, 0.1111, 12},
		{"LOW", RegimeLineVAT, 0.12, 13},
		{"EXEMPT_IMPORT_EXPORT", RegimeZero, 0, 52},
		{"EXEMPT", RegimeZero, 0, 5},
		{"OUTSIDE", RegimeZero, 0, 6},
		{"EXEMPT_REVERSE", RegimeZero, 0, 51},
		{"HIGH_DIRECT", RegimeDirect, 0.25, 14},
		{"MEDIUM_DIRECT", RegimeDirect, 0.15, 15},
		{"HIGH_BASIS", RegimeBasis, 0.25, 21},
		{"MEDIUM_BASIS", RegimeBasis, 0.15, 22},
		{"NONE_IMPORT_BASIS", RegimeZero, 0, 23},
		{"HIGH_FOREIGN_SERVICE_DEDUCTIBLE", RegimeBasis, 0.25, 86},
		{"HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE", RegimeReverseNondeductible, 0.25, 87},
		{"LOW_FOREIGN_SERVICE_DEDUCTIBLE", RegimeBasis, 0.12, 88},
		{"LOW_FOREIGN_SERVICE_NONDEDUCTIBLE", RegimeReverseNondeductible, 0.12, 89},
		{"HIGH_PURCHASE_OF_EMISSIONSTRADING_OR_GOLD_DEDUCTIBLE", RegimeBasis, 0.25, 91},
		{"HIGH_PURCHASE_OF_EMISSIONSTRADING_OR_GOLD_NONDEDUCTIBLE", RegimeReverseNondeductible, 0.25, 92},
	}
	if len(want) != len(vatTypes) {
		t.Fatalf("the expectation table has %d rows, the authority %d types", len(want), len(vatTypes))
	}
	for _, w := range want {
		info, ok := Lookup(w.ty)
		if !ok {
			t.Errorf("Lookup(%q) missing", w.ty)
			continue
		}
		if info.Regime != w.regime {
			t.Errorf("%s regime = %s, want %s", w.ty, info.Regime, w.regime)
		}
		if info.Rate != w.rate {
			t.Errorf("%s rate = %v, want %v", w.ty, info.Rate, w.rate)
		}
		if info.Code != w.code {
			t.Errorf("%s code = %d, want %d", w.ty, info.Code, w.code)
		}
	}
	if _, ok := Lookup("HIGH_INVENTED"); ok {
		t.Error("an unknown vatType must not resolve")
	}
	if _, ok := Lookup(""); ok {
		t.Error("the empty vatType must not resolve")
	}
	if RegimeZero.String() != "zero" || RegimeReverseNondeductible.String() != "reverse_nondeductible" {
		t.Error("Regime.String is part of the report contract")
	}
}

func TestTypesFor(t *testing.T) {
	sales := TypesFor(SideSales)
	if len(sales) != len(salesVATTypes) || !sort.StringsAreSorted(sales) {
		t.Errorf("TypesFor(sales) = %v", sales)
	}
	purch := TypesFor(SidePurchases)
	if len(purch) != 16 {
		t.Errorf("TypesFor(purchases) has %d entries, want all 16", len(purch))
	}
	var hasReverse bool
	for _, ty := range purch {
		if ty == "HIGH_FOREIGN_SERVICE_DEDUCTIBLE" {
			hasReverse = true
		}
	}
	if !hasReverse {
		t.Error("the purchase menu must offer the reverse-charge types")
	}
	if TypesFor(SideBoth) != nil || TypesFor("nonsense") != nil {
		t.Error("TypesFor of a non-side must be nil")
	}
}

func TestRoundOreHalfAwayFromZero(t *testing.T) {
	cases := []struct {
		in   float64
		want int64
	}{{83.25, 83}, {83.5, 84}, {2512.5, 2513}, {-83.5, -84}, {-83.2, -83}, {0, 0}}
	for _, c := range cases {
		if got := RoundOre(c.in); got != c.want {
			t.Errorf("RoundOre(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// (d) One synthetic line per regime through the three arithmetic entry points.
func TestLineArithmeticPerRegime(t *testing.T) {
	type want struct {
		ok                    bool
		kind                  string
		diff                  int64
		gross                 int64
		basis, output, input  int64
		expectedVAT           int64
		expectedVATIsCheckble bool
	}
	cases := []struct {
		name     string
		ty       string
		side     string
		net, vat int64
		tol      int64
		w        want
	}{
		{
			name: "ordinary domestic sale is net+vat, output side",
			ty:   "HIGH", side: SideSales, net: 100000, vat: 25000, tol: 2,
			w: want{ok: true, kind: KindVATRate, gross: 125000, basis: 100000, output: 25000, expectedVAT: 25000, expectedVATIsCheckble: true},
		},
		{
			name: "ordinary domestic purchase puts the vat on the input side",
			ty:   "HIGH", side: SidePurchases, net: 100000, vat: 25000, tol: 2,
			w: want{ok: true, kind: KindVATRate, gross: 125000, basis: 100000, input: 25000, expectedVAT: 25000, expectedVATIsCheckble: true},
		},
		{
			name: "reverse-charge basis line carries no vat and nets to zero",
			ty:   "HIGH_FOREIGN_SERVICE_DEDUCTIBLE", side: SidePurchases, net: 200000, vat: 0, tol: 2,
			w: want{ok: true, kind: KindBasisHasVAT, gross: 200000, basis: 200000, output: 50000, input: 50000},
		},
		{
			name: "a basis line with vat on it is a coding error at any tolerance",
			ty:   "MEDIUM_BASIS", side: SidePurchases, net: 100000, vat: 1, tol: 5,
			w: want{ok: false, kind: KindBasisHasVAT, diff: 1, gross: 100000, basis: 100000, output: 15000, input: 15000},
		},
		{
			name: "direct line has no basis and the vat is the amount",
			ty:   "MEDIUM_DIRECT", side: SidePurchases, net: 0, vat: 43210, tol: 2,
			w: want{ok: true, kind: KindDirectHasNet, gross: 43210, input: 43210},
		},
		{
			name: "direct line with a net is a coding error",
			ty:   "HIGH_DIRECT", side: SidePurchases, net: 5000, vat: 1250, tol: 2,
			w: want{ok: false, kind: KindDirectHasNet, diff: 5000, gross: 1250, input: 1250},
		},
		{
			name: "nondeductible reverse charge: inclusive net, negative embedded vat",
			ty:   "HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE", side: SidePurchases, net: 125000, vat: -25000, tol: 2,
			w: want{ok: true, kind: KindNondeductibleEmbeddedVAT, gross: 125000, basis: 100000, output: 25000},
		},
		{
			name: "nondeductible with the sign flipped is caught",
			ty:   "HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE", side: SidePurchases, net: 125000, vat: 25000, tol: 2,
			w: want{ok: false, kind: KindNondeductibleEmbeddedVAT, diff: 50000, gross: 125000, basis: 100000, output: 25000},
		},
		{
			name: "zero-rated line is basis only",
			ty:   "NONE_IMPORT_BASIS", side: SidePurchases, net: 500000, vat: 0, tol: 2,
			w: want{ok: true, kind: KindZeroRatedHasVAT, gross: 500000, basis: 500000},
		},
		{
			name: "exempt sale with vat on it is a coding error",
			ty:   "EXEMPT", side: SideSales, net: 5000, vat: 100, tol: 5,
			w: want{ok: false, kind: KindZeroRatedHasVAT, diff: 100, gross: 5000, basis: 5000},
		},
		{
			name: "rounding edge: 3 øre off passes at 5 and fails at 2",
			ty:   "MEDIUM", side: SideSales, net: 3900, vat: 588, tol: 5,
			w: want{ok: true, kind: KindVATRate, diff: 3, gross: 4488, basis: 3900, output: 588, expectedVAT: 585, expectedVATIsCheckble: true},
		},
		{
			name: "the same line at tolerance 2 is a finding",
			ty:   "MEDIUM", side: SideSales, net: 3900, vat: 588, tol: 2,
			w: want{ok: false, kind: KindVATRate, diff: 3, gross: 4488, basis: 3900, output: 588, expectedVAT: 585, expectedVATIsCheckble: true},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info, ok := Lookup(c.ty)
			if !ok {
				t.Fatalf("Lookup(%q) failed", c.ty)
			}
			if info.Side == SideBoth {
				info.Side = c.side
			}
			gotOK, gotKind, gotDiff := LineInvariant(info, c.net, c.vat, c.tol)
			if gotOK != c.w.ok || gotKind != c.w.kind || gotDiff != c.w.diff {
				t.Errorf("LineInvariant = %v,%q,%d; want %v,%q,%d", gotOK, gotKind, gotDiff, c.w.ok, c.w.kind, c.w.diff)
			}
			if g := Gross(info, c.net, c.vat); g != c.w.gross {
				t.Errorf("Gross = %d, want %d", g, c.w.gross)
			}
			basis, out, in := ReturnFigures(info, c.net, c.vat)
			if basis != c.w.basis || out != c.w.output || in != c.w.input {
				t.Errorf("ReturnFigures = %d,%d,%d; want %d,%d,%d", basis, out, in, c.w.basis, c.w.output, c.w.input)
			}
			if out < 0 || in < 0 {
				t.Errorf("a negative line vat must never reach a return total: out %d in %d", out, in)
			}
			exp, checkable := ExpectedLineVAT(info, c.net)
			if checkable != c.w.expectedVATIsCheckble || exp != c.w.expectedVAT {
				t.Errorf("ExpectedLineVAT = %d,%v; want %d,%v", exp, checkable, c.w.expectedVAT, c.w.expectedVATIsCheckble)
			}
		})
	}
}

// The zero VATTypeInfo — what Lookup returns on a miss — must keep the
// pre-authority net+vat behaviour for gross consumers.
func TestGrossOfUnknownTypeFallsBackToNetPlusVAT(t *testing.T) {
	info, ok := Lookup("NOT_A_TYPE")
	if ok {
		t.Fatal("NOT_A_TYPE must not resolve")
	}
	if g := Gross(info, 1000, 250); g != 1250 {
		t.Errorf("Gross(zero info) = %d, want 1250", g)
	}
}

// (e) spec.yaml is the source of the two enum lists; parity keeps a re-import
// from silently adding a type the authority does not classify.
func TestSpecEnumParity(t *testing.T) {
	raw, err := os.ReadFile("../../spec.yaml")
	if err != nil {
		t.Skipf("spec.yaml not readable: %v", err)
	}
	text := string(raw)
	parse := func(marker string) []string {
		i := strings.Index(text, marker)
		if i < 0 {
			t.Fatalf("marker %q not found in spec.yaml", marker)
		}
		rest := text[i+len(marker):]
		j := strings.Index(rest, "]")
		if j < 0 {
			t.Fatalf("unterminated enum after %q", marker)
		}
		var out []string
		for _, tok := range regexp.MustCompile(`[,\s]+`).Split(rest[:j], -1) {
			if tok != "" {
				out = append(out, tok)
			}
		}
		return out
	}
	check := func(marker, side string, set map[string]bool) {
		types := parse(marker)
		if len(types) != len(set) {
			t.Errorf("%s: spec lists %d types, the authority has %d (%v)", side, len(types), len(set), types)
		}
		for _, ty := range types {
			if !set[ty] {
				t.Errorf("%s: spec.yaml has %q, the %s set does not", side, ty, side)
			}
			if _, ok := Lookup(ty); !ok {
				t.Errorf("%s: spec.yaml has %q with no regime — classify it in vatTypeRegimes", side, ty)
			}
		}
	}
	check("Vat Types for SALES: [", SideSales, salesVATTypes)
	check("Vat Types for PURCHASES: [", SidePurchases, purchaseVATTypes)
}

// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL). The single VAT authority: the closed Fiken vatType
// taxonomy plus, for each type, its REGIME (does the vatType put VAT on the
// order line at all?), its rate, its numeric MVA code and the side it is valid
// for. Numeric codes are used at the transaction/journal level
// (debitVatCode/creditVatCode); string types are used for sales/purchases
// lines (vatType). Every rate, rounding and net+vat decision in the repo goes
// through this file — no consumer may keep a private rate table and no
// consumer may prefix-match a vatType (scripts/check-boundaries.sh enforces
// the second).
package fikencore

import "sort"

// Side a VAT type/code is valid for.
const (
	SideSales     = "sales"
	SidePurchases = "purchases"
	SideBoth      = "both"
)

// Regime says what the order line's `vat` field means for a vatType. Only
// RegimeLineVAT makes netPrice*rate meaningful; the others carry 0, the VAT
// amount itself, or a negative embedded amount.
type Regime int

const (
	// RegimeLineVAT: ordinary domestic VAT. vat == round(net*rate).
	RegimeLineVAT Regime = iota
	// RegimeBasis: the line carries only the basis (grunnlag); vat == 0. The
	// VAT is computed in the MVA return, and for reverse charge Fiken posts
	// the 2702/2712 pair itself.
	RegimeBasis
	// RegimeDirect: VAT posted with no basis (a customs agent invoicing back
	// import VAT). net == 0 and vat is the amount.
	RegimeDirect
	// RegimeReverseNondeductible: reverse charge with no input deduction. net
	// is VAT-inclusive and vat == -round(net*rate/(1+rate)).
	RegimeReverseNondeductible
	// RegimeZero: no VAT at all (NONE/EXEMPT/OUTSIDE and the import basis with
	// no rate). vat == 0, rate 0.
	RegimeZero
)

// String renders a regime for JSON/report output.
func (r Regime) String() string {
	switch r {
	case RegimeLineVAT:
		return "line_vat"
	case RegimeBasis:
		return "basis"
	case RegimeDirect:
		return "direct"
	case RegimeReverseNondeductible:
		return "reverse_nondeductible"
	case RegimeZero:
		return "zero"
	default:
		return "unknown"
	}
}

// The four Norwegian MVA rates. RAW_FISH is 11.11% (the råfisk sats).
const (
	RateHigh    = 0.25
	RateMedium  = 0.15
	RateLow     = 0.12
	RateRawFish = 0.1111
)

// VATTypeInfo is everything the repo may know about a vatType.
//
// Code is the numeric MVA code for the type on its own side. The five
// both-sided types have a kjøp/salg code pair (HIGH is 1 on purchases, 3 on
// sales); for those, Code is the purchases (inngående) code — use CodeFor when
// the side matters.
type VATTypeInfo struct {
	Type   string  `json:"type"`
	Regime Regime  `json:"-"`
	Rate   float64 `json:"rate"`
	Code   int     `json:"code"`
	Side   string  `json:"side"` // SideSales | SidePurchases | SideBoth
}

// salesVATTypes is the closed set of vatType strings valid on a SALES line.
var salesVATTypes = map[string]bool{
	"NONE": true, "HIGH": true, "MEDIUM": true, "RAW_FISH": true, "LOW": true,
	"EXEMPT_IMPORT_EXPORT": true, "EXEMPT": true, "OUTSIDE": true, "EXEMPT_REVERSE": true,
}

// purchaseVATTypes is the closed set of vatType strings valid on a PURCHASE line.
var purchaseVATTypes = map[string]bool{
	"NONE": true, "HIGH": true, "MEDIUM": true, "RAW_FISH": true, "LOW": true,
	"HIGH_DIRECT": true, "HIGH_BASIS": true, "MEDIUM_DIRECT": true, "MEDIUM_BASIS": true,
	"NONE_IMPORT_BASIS":               true,
	"HIGH_FOREIGN_SERVICE_DEDUCTIBLE": true, "HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE": true,
	"LOW_FOREIGN_SERVICE_DEDUCTIBLE": true, "LOW_FOREIGN_SERVICE_NONDEDUCTIBLE": true,
	"HIGH_PURCHASE_OF_EMISSIONSTRADING_OR_GOLD_DEDUCTIBLE":    true,
	"HIGH_PURCHASE_OF_EMISSIONSTRADING_OR_GOLD_NONDEDUCTIBLE": true,
}

// VATCodeInfo describes a numeric VAT code.
type VATCodeInfo struct {
	Type string
	Side string // SideSales | SidePurchases (a numeric code is never both)
}

// numericVATCodes maps the numeric VAT codes used in journal entries to their
// type and side. The "kjøp/salg" pairs (e.g. 0/7 = NONE) appear as two codes,
// the first (lower, in spec.yaml's table) being the purchases/inngående one.
var numericVATCodes = map[int]VATCodeInfo{
	0: {"NONE", SidePurchases}, 7: {"NONE", SideSales},
	1: {"HIGH", SidePurchases}, 3: {"HIGH", SideSales},
	11: {"MEDIUM", SidePurchases}, 31: {"MEDIUM", SideSales},
	12: {"RAW_FISH", SidePurchases}, 32: {"RAW_FISH", SideSales},
	13: {"LOW", SidePurchases}, 33: {"LOW", SideSales},
	52: {"EXEMPT_IMPORT_EXPORT", SideSales},
	5:  {"EXEMPT", SideSales},
	6:  {"OUTSIDE", SideSales},
	51: {"EXEMPT_REVERSE", SideSales},
	14: {"HIGH_DIRECT", SidePurchases},
	21: {"HIGH_BASIS", SidePurchases},
	15: {"MEDIUM_DIRECT", SidePurchases},
	22: {"MEDIUM_BASIS", SidePurchases},
	23: {"NONE_IMPORT_BASIS", SidePurchases},
	86: {"HIGH_FOREIGN_SERVICE_DEDUCTIBLE", SidePurchases},
	87: {"HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE", SidePurchases},
	88: {"LOW_FOREIGN_SERVICE_DEDUCTIBLE", SidePurchases},
	89: {"LOW_FOREIGN_SERVICE_NONDEDUCTIBLE", SidePurchases},
	91: {"HIGH_PURCHASE_OF_EMISSIONSTRADING_OR_GOLD_DEDUCTIBLE", SidePurchases},
	92: {"HIGH_PURCHASE_OF_EMISSIONSTRADING_OR_GOLD_NONDEDUCTIBLE", SidePurchases},
}

// vatTypeRegimes is the regime/rate half of the authority. The rate of a
// basis/direct/nondeductible type is DATA here, never derived from its name at
// runtime. Sides and codes come from the two sets and numericVATCodes above.
var vatTypeRegimes = map[string]struct {
	regime Regime
	rate   float64
}{
	// Ordinary domestic: vat == round(net*rate).
	"HIGH":     {RegimeLineVAT, RateHigh},
	"MEDIUM":   {RegimeLineVAT, RateMedium},
	"LOW":      {RegimeLineVAT, RateLow},
	"RAW_FISH": {RegimeLineVAT, RateRawFish},
	// Basis (grunnlag): line vat is 0 by construction.
	"HIGH_BASIS":                      {RegimeBasis, RateHigh},
	"MEDIUM_BASIS":                    {RegimeBasis, RateMedium},
	"HIGH_FOREIGN_SERVICE_DEDUCTIBLE": {RegimeBasis, RateHigh},
	"LOW_FOREIGN_SERVICE_DEDUCTIBLE":  {RegimeBasis, RateLow},
	"HIGH_PURCHASE_OF_EMISSIONSTRADING_OR_GOLD_DEDUCTIBLE": {RegimeBasis, RateHigh},
	// Direct: net == 0, vat is the amount.
	"HIGH_DIRECT":   {RegimeDirect, RateHigh},
	"MEDIUM_DIRECT": {RegimeDirect, RateMedium},
	// Reverse charge without deduction: net is VAT-inclusive, vat is negative.
	"HIGH_FOREIGN_SERVICE_NONDEDUCTIBLE":                      {RegimeReverseNondeductible, RateHigh},
	"LOW_FOREIGN_SERVICE_NONDEDUCTIBLE":                       {RegimeReverseNondeductible, RateLow},
	"HIGH_PURCHASE_OF_EMISSIONSTRADING_OR_GOLD_NONDEDUCTIBLE": {RegimeReverseNondeductible, RateHigh},
	// No VAT on the line and no rate.
	"NONE":                 {RegimeZero, 0},
	"NONE_IMPORT_BASIS":    {RegimeZero, 0},
	"EXEMPT":               {RegimeZero, 0},
	"EXEMPT_IMPORT_EXPORT": {RegimeZero, 0},
	"EXEMPT_REVERSE":       {RegimeZero, 0},
	"OUTSIDE":              {RegimeZero, 0},
}

// vatTypes is the assembled table: the only place a rate or a regime is read.
var vatTypes = buildVATTypes()

func buildVATTypes() map[string]VATTypeInfo {
	out := make(map[string]VATTypeInfo, len(vatTypeRegimes))
	for t, spec := range vatTypeRegimes {
		side := ""
		switch {
		case salesVATTypes[t] && purchaseVATTypes[t]:
			side = SideBoth
		case salesVATTypes[t]:
			side = SideSales
		case purchaseVATTypes[t]:
			side = SidePurchases
		}
		code := 0
		if c, ok := codeOf(t, SidePurchases); ok {
			code = c
		} else if c, ok := codeOf(t, SideSales); ok {
			code = c
		}
		out[t] = VATTypeInfo{Type: t, Regime: spec.regime, Rate: spec.rate, Code: code, Side: side}
	}
	return out
}

func codeOf(vatType, side string) (int, bool) {
	for code, info := range numericVATCodes {
		if info.Type == vatType && info.Side == side {
			return code, true
		}
	}
	return 0, false
}

// Lookup returns the authority's entry for a vatType. It is the only way to
// obtain a rate or a regime. An unknown (or empty) vatType returns ok=false:
// consumers must turn that into a finding or a validation warning, never into
// a silent rate of 0.
func Lookup(vatType string) (VATTypeInfo, bool) {
	info, ok := vatTypes[vatType]
	return info, ok
}

// CodeFor returns the numeric MVA code a vatType carries on a given side. The
// both-sided types have a kjøp/salg pair (HIGH is 1 on purchases, 3 on sales);
// a one-sided type answers only for its own side.
func CodeFor(vatType, side string) (int, bool) {
	return codeOf(vatType, side)
}

// TypesFor returns the sorted, complete set of vatType strings valid on a
// side. Unknown side returns nil.
func TypesFor(side string) []string {
	var set map[string]bool
	switch side {
	case SideSales:
		set = salesVATTypes
	case SidePurchases:
		set = purchaseVATTypes
	default:
		return nil
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// RoundOre rounds to whole øre, half away from zero. The one rounding rule in
// the repo: an unsigned "+0.5" rounds a nondeductible line's negative VAT the
// wrong way.
func RoundOre(x float64) int64 {
	if x < 0 {
		return int64(x - 0.5)
	}
	return int64(x + 0.5)
}

func absOre(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// ExpectedLineVAT returns the VAT the order line should carry for a net
// amount. checkable is true only for RegimeLineVAT — for every other regime
// the line's vat is not a function of net*rate, and the caller must use
// LineInvariant instead of inventing a comparison.
func ExpectedLineVAT(info VATTypeInfo, netOre int64) (expectedOre int64, checkable bool) {
	if info.Regime != RegimeLineVAT {
		return 0, false
	}
	return RoundOre(float64(netOre) * info.Rate), true
}

// Finding kinds returned by LineInvariant, one per regime.
const (
	KindVATRate                  = "vat_rate"                   // RegimeLineVAT: vat off the rate
	KindBasisHasVAT              = "basis_has_vat"              // RegimeBasis: a basis line must carry vat 0
	KindZeroRatedHasVAT          = "zero_rated_has_vat"         // RegimeZero: NONE/EXEMPT/OUTSIDE must carry vat 0
	KindDirectHasNet             = "direct_has_net"             // RegimeDirect: the VAT has no basis, net must be 0
	KindNondeductibleEmbeddedVAT = "nondeductible_embedded_vat" // RegimeReverseNondeductible: vat is the negative embedded amount
	KindUnknownVATType           = "unknown_vat_type"           // Lookup missed — never silently skipped
)

// LineInvariant checks the one invariant that holds for the vatType's regime.
// ok reports whether the line is consistent; kind names the invariant that was
// violated; diffOre is actual − expected in the terms of that invariant (for
// RegimeDirect it is the net, which should be 0). The tolerance applies to the
// arithmetic regimes only: a basis or zero-rated line's vat is structurally 0
// in Fiken, so any VAT there is a coding error, not rounding.
func LineInvariant(info VATTypeInfo, netOre, vatOre, toleranceOre int64) (ok bool, kind string, diffOre int64) {
	switch info.Regime {
	case RegimeLineVAT:
		diff := vatOre - RoundOre(float64(netOre)*info.Rate)
		return absOre(diff) <= toleranceOre, KindVATRate, diff
	case RegimeBasis:
		return vatOre == 0, KindBasisHasVAT, vatOre
	case RegimeZero:
		return vatOre == 0, KindZeroRatedHasVAT, vatOre
	case RegimeDirect:
		return absOre(netOre) <= toleranceOre, KindDirectHasNet, netOre
	case RegimeReverseNondeductible:
		diff := vatOre - embeddedVAT(info, netOre)
		return absOre(diff) <= toleranceOre, KindNondeductibleEmbeddedVAT, diff
	default:
		return true, "", 0
	}
}

// embeddedVAT is the negative VAT a nondeductible reverse-charge line carries:
// netPrice is VAT-inclusive, so the VAT is net*r/(1+r), booked negative.
func embeddedVAT(info VATTypeInfo, netOre int64) int64 {
	return -RoundOre(float64(netOre) * info.Rate / (1 + info.Rate))
}

// Gross is the document-level amount a line contributes, per regime: net+vat
// only for ordinary domestic VAT. A basis or nondeductible line's net is
// already what the supplier invoiced; a direct line has no net at all.
//
// The zero VATTypeInfo (what Lookup returns on a miss) is RegimeLineVAT with
// rate 0, so an unknown type falls back to net+vat — the pre-authority
// behaviour, which is the safe answer when nothing is known.
func Gross(info VATTypeInfo, netOre, vatOre int64) int64 {
	switch info.Regime {
	case RegimeDirect:
		return vatOre
	case RegimeBasis, RegimeReverseNondeductible, RegimeZero:
		return netOre
	default: // RegimeLineVAT
		return netOre + vatOre
	}
}

// ReturnFigures maps one order line onto the three MVA-return quantities:
// the basis, the output (utgående) VAT and the input (inngående) VAT.
//
//   - RegimeLineVAT: basis is the net; the VAT lands on the side in info.Side.
//     The five both-sided types cannot answer that from the taxonomy alone, so
//     the caller resolves Side to the document's side first; an unresolved
//     SideBoth is treated as output.
//   - RegimeBasis: basis is the net and the VAT is computed here — output and
//     input are equal (reverse charge / utsatt avregning nets to zero).
//   - RegimeDirect: no basis; the amount is input VAT.
//   - RegimeReverseNondeductible: the net is VAT-inclusive, so the basis is
//     net/(1+rate); the embedded VAT is output VAT only — there is deliberately
//     no input side — and it is reported positive, never as a negative total.
//   - RegimeZero: basis only.
func ReturnFigures(info VATTypeInfo, netOre, vatOre int64) (basisOre, outputVATOre, inputVATOre int64) {
	switch info.Regime {
	case RegimeBasis:
		vat := RoundOre(float64(netOre) * info.Rate)
		return netOre, vat, vat
	case RegimeDirect:
		return 0, 0, vatOre
	case RegimeReverseNondeductible:
		return RoundOre(float64(netOre) / (1 + info.Rate)), absOre(vatOre), 0
	case RegimeZero:
		return netOre, 0, 0
	default: // RegimeLineVAT
		if info.Side == SidePurchases {
			return netOre, 0, vatOre
		}
		return netOre, vatOre, 0
	}
}

// VATTypeValidFor reports whether a vatType string is valid for the given side
// ("sales" or "purchases"). Unknown side returns false.
func VATTypeValidFor(vatType, side string) bool {
	switch side {
	case SideSales:
		return salesVATTypes[vatType]
	case SidePurchases:
		return purchaseVATTypes[vatType]
	default:
		return false
	}
}

// KnownVATType reports whether vatType is a recognized Fiken VAT type on
// either side.
func KnownVATType(vatType string) bool {
	return salesVATTypes[vatType] || purchaseVATTypes[vatType]
}

// VATCode looks up a numeric VAT code's info.
func VATCode(code int) (VATCodeInfo, bool) {
	info, ok := numericVATCodes[code]
	return info, ok
}

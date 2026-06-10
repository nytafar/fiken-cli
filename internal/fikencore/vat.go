// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL). Fiken VAT/MVA code reference, transcribed from the
// API v2 spec. Numeric codes are used at the transaction/journal level
// (debitVatCode/creditVatCode); string types are used for sales/purchases
// lines (vatType). Powers validate's MVA-plausibility check and the
// vat-anomaly detector. This is reference data, not matcher logic.
package fikencore

// Side a VAT type/code is valid for.
const (
	SideSales     = "sales"
	SidePurchases = "purchases"
	SideBoth      = "both"
)

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
	Side string // SideSales | SidePurchases | SideBoth
}

// numericVATCodes maps the numeric VAT codes used in journal entries to their
// type and side. The "kjøp/salg" pairs (e.g. 0/7 = NONE) appear as two codes.
var numericVATCodes = map[int]VATCodeInfo{
	0: {"NONE", SideBoth}, 7: {"NONE", SideBoth},
	1: {"HIGH", SideBoth}, 3: {"HIGH", SideBoth},
	11: {"MEDIUM", SideBoth}, 31: {"MEDIUM", SideBoth},
	12: {"RAW_FISH", SideBoth}, 32: {"RAW_FISH", SideBoth},
	13: {"LOW", SideBoth}, 33: {"LOW", SideBoth},
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

package deterministic

import (
	"math"

	"share/backend/internal/extraction"
	"share/backend/internal/llm"
)

// ResolvePriceFormat decides, per receipt, whether each item's
// printedPriceCents is already a line total or a per-unit price: it tries
// both interpretations' sums against subtotalCents (within a 1-cent
// tolerance, matching extraction.CheckSubtotal) and returns items converted
// to llm.ExtractedItem's existing per-unit-price convention — the
// convention httpapi/receipt.go already assumes when it multiplies
// PriceCents by Quantity to get a dish's stored line total.
//
// If neither interpretation matches, matched is false and printedPriceCents
// is treated as a line total anyway (the safer default: most receipt lines
// have quantity 1, where a line total and a per-unit price are identical,
// so this fallback is a no-op in the common case).
func ResolvePriceFormat(items []PrintedItem, subtotalCents int64) (resolved []llm.ExtractedItem, matched bool, diffCents int64) {
	asPerUnit := toExtractedItems(items, false)
	perUnitMatched, perUnitDiff := extraction.CheckSubtotal(asPerUnit, subtotalCents)
	if perUnitMatched {
		return asPerUnit, true, perUnitDiff
	}

	lineTotalMatched, lineTotalDiff := checkLineTotalSum(items, subtotalCents)
	if lineTotalMatched {
		return toExtractedItems(items, true), true, lineTotalDiff
	}

	return toExtractedItems(items, true), false, lineTotalDiff
}

func checkLineTotalSum(items []PrintedItem, subtotalCents int64) (matched bool, diffCents int64) {
	var sum int64
	for _, it := range items {
		sum += it.PrintedPriceCents
	}
	diff := sum - subtotalCents
	if diff < 0 {
		diff = -diff
	}
	return diff <= 1, diff
}

// toExtractedItems converts printed items into llm.ExtractedItem's
// per-unit-price convention. When printedIsLineTotal is true,
// printedPriceCents is divided back down to a per-unit price so downstream
// code (which multiplies PriceCents by Quantity) reconstructs the original
// printed line total rather than doubling it.
func toExtractedItems(items []PrintedItem, printedIsLineTotal bool) []llm.ExtractedItem {
	out := make([]llm.ExtractedItem, 0, len(items))
	for _, it := range items {
		qty := it.Quantity
		if qty <= 0 {
			qty = 1
		}
		priceCents := it.PrintedPriceCents
		if printedIsLineTotal {
			priceCents = int64(math.Round(float64(it.PrintedPriceCents) / qty))
		}
		out = append(out, llm.ExtractedItem{
			Name:       it.Name,
			PriceCents: priceCents,
			Quantity:   it.Quantity,
		})
	}
	return out
}

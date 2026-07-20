package extraction

import (
	"math"

	"share/backend/internal/llm"
)

// SumItemsCents computes sum(item.PriceCents * item.Quantity), rounding to
// the nearest cent per item. Shared by CheckSubtotal and by strategies that
// need the actual computed figure, not just a match/no-match verdict (e.g.
// feedback's retry prompt quotes it back to the model).
func SumItemsCents(items []llm.ExtractedItem) int64 {
	var sum int64
	for _, it := range items {
		qty := it.Quantity
		if qty <= 0 {
			qty = 1
		}
		sum += int64(math.Round(float64(it.PriceCents) * qty))
	}
	return sum
}

// CheckSubtotal reports whether SumItemsCents(items) equals subtotalCents,
// within a 1-cent tolerance for multi-item rounding. Pure, no network —
// shared by every strategy that needs to know "does the receipt's printed
// math check out."
func CheckSubtotal(items []llm.ExtractedItem, subtotalCents int64) (matched bool, diffCents int64) {
	diff := SumItemsCents(items) - subtotalCents
	if diff < 0 {
		diff = -diff
	}
	return diff <= 1, diff
}

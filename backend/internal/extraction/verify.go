package extraction

import "share/backend/internal/llm"

// CheckSubtotal reports whether sum(item.PriceCents * item.Quantity) equals
// subtotalCents, within a 1-cent tolerance for multi-item rounding. Pure,
// no network — shared by every strategy that needs to know "does the
// receipt's printed math check out."
func CheckSubtotal(items []llm.ExtractedItem, subtotalCents int64) (matched bool, diffCents int64) {
	var sum int64
	for _, it := range items {
		qty := it.Quantity
		if qty <= 0 {
			qty = 1
		}
		sum += int64(float64(it.PriceCents)*qty + 0.5)
	}
	diff := sum - subtotalCents
	if diff < 0 {
		diff = -diff
	}
	return diff <= 1, diff
}

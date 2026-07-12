package extraction

import "math"

// EstimateCostCents converts token usage into a cost, rounding rather than
// truncating — floor-ing every call systematically underreports spend
// against the daily cap, letting real spend drift past it over many calls.
func EstimateCostCents(promptTokens, completionTokens int, inputCostPer1KTokensCents, outputCostPer1KTokensCents float64) int {
	cost := float64(promptTokens)/1000.0*inputCostPer1KTokensCents +
		float64(completionTokens)/1000.0*outputCostPer1KTokensCents
	return int(math.Round(cost))
}

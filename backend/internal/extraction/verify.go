package extraction

import (
	"math"

	"share/backend/internal/llm"
)

type TaxSource string

const (
	TaxSourcePrinted       TaxSource = "printed"
	TaxSourceRateInferred  TaxSource = "rate_inferred"
	TaxSourceTotalInferred TaxSource = "total_inferred"
	TaxSourceUnresolved    TaxSource = "unresolved"
)

// Reconciliation is pure, integer-cent validation of one model result.
// Residual tax inference assumes no unmodelled adjustment (the extraction
// schema has no adjustments field), and is therefore only used when all three
// subtotal, tip, and positive total are available and the residual is >= 0.
type Reconciliation struct {
	ItemSumCents                int64
	TaxableSubtotalCents        int64
	ItemSubtotalMatched         bool
	ItemSubtotalChecked         bool
	ItemSubtotalDiffCents       int64
	ResolvedTaxCents            *int64
	TaxSource                   TaxSource
	TaxMatched                  bool
	TaxChecked                  bool
	TaxDiffCents                int64
	GrandTotalMatched           bool
	GrandTotalChecked           bool
	GrandTotalDiffCents         int64
	FailedChecks                int
	AggregateAbsDifferenceCents int64
	MultipleTaxRatesDetected    bool
	MultipleTaxRatesChecked     bool
}

func abs64(v int64) int64 {
	if v < 0 {
		if v == math.MinInt64 {
			return math.MaxInt64
		}
		return -v
	}
	return v
}

func absDiff(a, b int64) int64 {
	if a >= b {
		return abs64(a - b)
	}
	return abs64(b - a)
}

func addNonNegative(a, b int64) (int64, bool) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, false
	}
	return a + b, true
}

// Reconcile normalizes omitted item markers and evaluates only invariants whose
// inputs are known. Optional zero-valued legacy amounts remain indistinguishable
// from omission; tipKnown and the diagnostic flags make total inference safe.
func Reconcile(receipt *llm.ExtractedReceipt) Reconciliation {
	receipt.NormalizeTaxable()
	r := Reconciliation{TaxSource: TaxSourceUnresolved}
	if receipt.MultipleTaxRatesDetected != nil {
		r.MultipleTaxRatesChecked = true
		r.MultipleTaxRatesDetected = *receipt.MultipleTaxRatesDetected
	}
	itemsValid := true
	for _, it := range receipt.Items {
		if _, ok := itemCentsChecked(it); !ok {
			itemsValid = false
			r.FailedChecks++
		}
	}
	r.ItemSumCents = SumItemsCents(receipt.Items)
	for _, it := range receipt.Items {
		if *it.Taxable {
			if cents, ok := itemCentsChecked(it); ok {
				var sumOK bool
				r.TaxableSubtotalCents, sumOK = addNonNegative(r.TaxableSubtotalCents, cents)
				if !sumOK {
					itemsValid = false
					r.FailedChecks++
				}
			}
		}
	}
	// The legacy scalar cannot distinguish an omitted subtotal from a printed
	// zero; only a positive subtotal is considered verifiable.
	if receipt.SubtotalCents < 0 {
		r.ItemSubtotalChecked = true
		r.FailedChecks++
	} else if receipt.SubtotalCents != 0 && itemsValid {
		r.ItemSubtotalChecked = true
		r.ItemSubtotalDiffCents = absDiff(r.ItemSumCents, receipt.SubtotalCents)
		r.ItemSubtotalMatched = r.ItemSubtotalDiffCents <= 1
		if !r.ItemSubtotalMatched {
			r.FailedChecks++
		}
	}
	var tax int64
	haveTax := false
	validRate := receipt.TaxRateBasisPoints != nil && *receipt.TaxRateBasisPoints >= 0 && *receipt.TaxRateBasisPoints <= 1_000_000
	if receipt.TaxRateBasisPoints != nil && !validRate {
		r.TaxChecked = true
		r.TaxMatched = false
		r.FailedChecks++
	}
	if receipt.TaxCents != nil {
		if *receipt.TaxCents < 0 {
			r.TaxChecked = true
			r.TaxMatched = false
			r.FailedChecks++
		} else {
			tax, r.TaxSource, haveTax = *receipt.TaxCents, TaxSourcePrinted, true
		}
	} else if validRate && !r.MultipleTaxRatesDetected && r.TaxableSubtotalCents > 0 {
		var ok bool
		tax, ok = RoundBasisPointsCents(r.TaxableSubtotalCents, *receipt.TaxRateBasisPoints)
		if !ok {
			r.TaxChecked = true
			r.FailedChecks++
		} else {
			r.TaxSource, haveTax = TaxSourceRateInferred, true
		}
	} else if receipt.SubtotalCents > 0 && receipt.TotalPaidCents > 0 && receipt.TipCents >= 0 && receipt.TipKnown != nil && *receipt.TipKnown && receipt.HasNonTaxAdjustments != nil && !*receipt.HasNonTaxAdjustments {
		// Compare before subtracting so two large positive amounts cannot
		// underflow into a seemingly valid residual.
		if receipt.TotalPaidCents >= receipt.SubtotalCents {
			remaining := receipt.TotalPaidCents - receipt.SubtotalCents
			if remaining >= receipt.TipCents {
				tax, r.TaxSource, haveTax = remaining-receipt.TipCents, TaxSourceTotalInferred, true
			}
		}
	}
	if haveTax {
		// A tax amount is independently checked only against a separately printed rate.
		if receipt.TaxCents != nil && validRate && !r.MultipleTaxRatesDetected && r.TaxableSubtotalCents > 0 {
			expected, ok := RoundBasisPointsCents(r.TaxableSubtotalCents, *receipt.TaxRateBasisPoints)
			if !ok {
				r.TaxChecked = true
				r.TaxMatched = false
				r.FailedChecks++
			} else {
				r.TaxChecked = true
				r.TaxDiffCents = abs64(tax - expected)
				r.TaxMatched = r.TaxDiffCents <= 1
				if !r.TaxMatched {
					r.FailedChecks++
				}
			}
		}
		r.ResolvedTaxCents = &tax
	}
	if receipt.TipCents < 0 {
		r.FailedChecks++
	}
	if receipt.TotalPaidCents < 0 {
		r.GrandTotalChecked = true
		r.FailedChecks++
	} else if receipt.TotalPaidCents != 0 && receipt.SubtotalCents > 0 && haveTax && receipt.TipKnown != nil && *receipt.TipKnown && receipt.TipCents >= 0 {
		r.GrandTotalChecked = true
		expected, ok := addNonNegative(receipt.SubtotalCents, tax)
		if ok {
			expected, ok = addNonNegative(expected, receipt.TipCents)
		}
		if !ok {
			r.GrandTotalMatched = false
			r.FailedChecks++
		} else {
			r.GrandTotalDiffCents = absDiff(expected, receipt.TotalPaidCents)
			r.GrandTotalMatched = r.GrandTotalDiffCents <= 1
			if !r.GrandTotalMatched {
				r.FailedChecks++
			}
		}
	}
	var aggregateOK bool
	r.AggregateAbsDifferenceCents, aggregateOK = addNonNegative(r.ItemSubtotalDiffCents, r.TaxDiffCents)
	if aggregateOK {
		r.AggregateAbsDifferenceCents, aggregateOK = addNonNegative(r.AggregateAbsDifferenceCents, r.GrandTotalDiffCents)
	}
	if !aggregateOK {
		r.AggregateAbsDifferenceCents = math.MaxInt64
	}
	return r
}

// RoundBasisPointsCents computes round(cents*basisPoints/10000) using integer
// arithmetic and rejects negative or overflowing inputs.
func RoundBasisPointsCents(cents, basisPoints int64) (int64, bool) {
	if cents < 0 || basisPoints < 0 || basisPoints > 1_000_000 {
		return 0, false
	}
	if basisPoints == 0 {
		return 0, true
	}
	q, rem := cents/10000, cents%10000
	if q > math.MaxInt64/basisPoints {
		return 0, false
	}
	whole := q * basisPoints
	part := rem * basisPoints
	if part > math.MaxInt64-5000 {
		return 0, false
	}
	part = (part + 5000) / 10000
	if whole > math.MaxInt64-part {
		return 0, false
	}
	return whole + part, true
}

func itemCentsChecked(it llm.ExtractedItem) (int64, bool) {
	if it.PriceCents < 0 || math.IsNaN(it.Quantity) || math.IsInf(it.Quantity, 0) || it.Quantity < 0 {
		return 0, false
	}
	q := it.Quantity
	if q <= 0 {
		q = 1
	}
	value := float64(it.PriceCents)*q + .5
	// float64(math.MaxInt64) rounds to 2^63, so use >= here; converting that
	// value to int64 would wrap on some architectures.
	if math.IsInf(value, 0) || value >= float64(math.MaxInt64) {
		return 0, false
	}
	return int64(value), true
}

func itemCents(it llm.ExtractedItem) int64 {
	cents, _ := itemCentsChecked(it)
	return cents
}

// SumItemsCents computes sum(item.PriceCents * item.Quantity), rounding to
// the nearest cent per item. Shared by reconciliation and the legacy
// subtotal helper.
func SumItemsCents(items []llm.ExtractedItem) int64 {
	var sum int64
	for _, it := range items {
		cents, ok := itemCentsChecked(it)
		if ok {
			if sum > math.MaxInt64-cents {
				return math.MaxInt64
			}
			sum += cents
		}
	}
	return sum
}

// CheckSubtotal is the legacy compatibility helper. New strategies should use
// Reconcile, which also reports whether the subtotal was actually checkable.
func CheckSubtotal(items []llm.ExtractedItem, subtotalCents int64) (matched bool, diffCents int64) {
	diff := SumItemsCents(items) - subtotalCents
	if diff < 0 {
		diff = -diff
	}
	return diff <= 1, diff
}

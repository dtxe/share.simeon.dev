// Package split computes the authoritative integer-cent split for a bill.
package split

import (
	"math"
	"sort"
)

type Dish struct {
	ID             string
	UnitPriceCents int64
	Quantity       float64
	Taxable        bool
}

func (d Dish) LineTotalCents() int64 {
	if !finite(d.Quantity) || d.Quantity < 0 {
		return 0
	}
	v := float64(d.UnitPriceCents) * d.Quantity
	return roundInt64(v)
}

type Portion struct {
	DishID, PersonID string
	Shares           float64
}

type PersonBreakdown struct {
	PersonID  string `json:"personId"`
	OwedCents int64  `json:"owedCents"`
	TaxCents  int64  `json:"taxCents"`
}

type Result struct {
	SubtotalCents        int64             `json:"subtotalCents"`
	People               []PersonBreakdown `json:"people"`
	UnassignedDishIDs    []string          `json:"unassignedDishIds"`
	UnallocatedTaxCents  int64             `json:"unallocatedTaxCents"`
	TaxDetailsIncomplete bool              `json:"taxDetailsIncomplete"`
}

// Compute allocates total paid across portions. Nil tax deliberately retains
// the original whole-bill scaling behavior.
func Compute(dishes []Dish, portions []Portion, peopleIDs []string, totalPaidCents int64, taxCents *int64) Result {
	dishShares := make(map[string]float64, len(dishes))
	personShares := make(map[string]map[string]float64, len(peopleIDs))
	for _, id := range peopleIDs {
		personShares[id] = map[string]float64{}
	}
	for _, p := range portions {
		if !finite(p.Shares) || p.Shares <= 0 {
			continue
		}
		dishShares[p.DishID] += p.Shares
		if personShares[p.PersonID] == nil {
			personShares[p.PersonID] = map[string]float64{}
		}
		personShares[p.PersonID][p.DishID] += p.Shares
	}

	line := make(map[string]int64, len(dishes))
	var subtotal, taxableSubtotal int64
	unassigned := []string{}
	hasExempt := false
	for _, d := range dishes {
		line[d.ID] = d.LineTotalCents()
		subtotal = addInt64(subtotal, line[d.ID])
		if d.Taxable {
			taxableSubtotal = addInt64(taxableSubtotal, line[d.ID])
		} else {
			hasExempt = true
		}
		if dishShares[d.ID] <= 0 {
			unassigned = append(unassigned, d.ID)
		}
	}
	known := taxCents != nil
	r := Result{SubtotalCents: subtotal, People: make([]PersonBreakdown, 0, len(peopleIDs)), UnassignedDishIDs: unassigned, TaxDetailsIncomplete: !known && hasExempt}
	taxByDish := make(map[string]float64, len(dishes))
	adjusted := make(map[string]float64, len(dishes))
	if known {
		tax := *taxCents
		// Subtract in float space so intermediate int64 overflow cannot turn a
		// large (but otherwise valid) adjustment into the wrong sign.
		residual := float64(totalPaidCents) - float64(subtotal) - float64(tax)
		var unallocated float64
		for _, d := range dishes {
			lt := float64(line[d.ID])
			if d.Taxable && taxableSubtotal != 0 {
				taxByDish[d.ID] = float64(tax) * lt / float64(taxableSubtotal)
			}
			if dishShares[d.ID] <= 0 {
				unallocated += taxByDish[d.ID]
			}
			adjusted[d.ID] = lt + taxByDish[d.ID]
			if subtotal != 0 {
				adjusted[d.ID] += residual * lt / float64(subtotal)
			}
		}
		if taxableSubtotal == 0 {
			r.UnallocatedTaxCents = tax
		}
		if r.UnallocatedTaxCents == 0 && unallocated != 0 {
			r.UnallocatedTaxCents = roundInt64(unallocated)
		}
	}

	raw, rawTax := map[string]float64{}, map[string]float64{}
	for _, id := range peopleIDs {
		// Walk dishes, rather than personShares, so floating-point accumulation
		// is independent of Go's randomized map iteration order.
		for _, d := range dishes {
			shares := personShares[id][d.ID]
			total := dishShares[d.ID]
			if total <= 0 {
				continue
			}
			fraction := shares / total
			if known {
				raw[id] += fraction * adjusted[d.ID]
				rawTax[id] += fraction * taxByDish[d.ID]
			} else {
				raw[id] += fraction * float64(line[d.ID])
			}
		}
	}
	if !known {
		scale := 0.0
		if subtotal > 0 {
			scale = float64(totalPaidCents) / float64(subtotal)
		}
		for _, id := range peopleIDs {
			raw[id] *= scale
		}
	}
	totalIdeal := sumByIDs(raw, peopleIDs)
	owed := roundAlloc(raw, peopleIDs, totalIdeal)
	taxRounded := roundAlloc(rawTax, peopleIDs, sumByIDs(rawTax, peopleIDs))
	for _, id := range peopleIDs {
		r.People = append(r.People, PersonBreakdown{PersonID: id, OwedCents: owed[id], TaxCents: taxRounded[id]})
	}
	return r
}

func sumByIDs(m map[string]float64, ids []string) float64 {
	var n float64
	for _, id := range ids {
		n += m[id]
	}
	return n
}

// roundAlloc uses input order as the deterministic tie breaker.
func roundAlloc(ideal map[string]float64, ids []string, target float64) map[string]int64 {
	type remainder struct {
		id   string
		frac float64
	}
	out := make(map[string]int64, len(ids))
	rems := make([]remainder, 0, len(ids))
	var floors int64
	for _, id := range ids {
		value := ideal[id]
		if !finite(value) {
			value = 0
		}
		f := math.Floor(value)
		floor := roundInt64(f)
		out[id] = floor
		floors = addInt64(floors, floor)
		rems = append(rems, remainder{id, value - f})
	}
	sort.SliceStable(rems, func(i, j int) bool { return rems[i].frac > rems[j].frac })
	left := subtractInt64(roundInt64(target), floors)
	if len(rems) == 0 {
		return out
	}
	if left > 0 {
		for i := int64(0); i < left; i++ {
			out[rems[i%int64(len(rems))].id] = addInt64(out[rems[i%int64(len(rems))].id], 1)
		}
	} else if left < 0 {
		// This is normally unreachable (a rounded sum cannot be below the
		// sum of floors), but handles floating error and negative inputs safely.
		for i := int64(0); i > left; i-- {
			idx := len(rems) - 1 - int((-i-1)%int64(len(rems)))
			out[rems[idx].id] = addInt64(out[rems[idx].id], -1)
		}
	}
	return out
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func roundInt64(v float64) int64 {
	if math.IsNaN(v) || v <= float64(math.MinInt64) {
		return math.MinInt64
	}
	if v >= float64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(math.Round(v))
}

func addInt64(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	if b < 0 && a < math.MinInt64-b {
		return math.MinInt64
	}
	return a + b
}

func subtractInt64(a, b int64) int64 {
	if b > 0 && a < math.MinInt64+b {
		return math.MinInt64
	}
	if b < 0 && a > math.MaxInt64+b {
		return math.MaxInt64
	}
	return a - b
}

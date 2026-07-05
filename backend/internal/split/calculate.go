// Package split computes how much each diner owes for a bill. It is the
// single source of truth for that math — both the owner-facing breakdown
// endpoint and the public share view call Compute directly, so the two can
// never disagree.
package split

import (
	"math"
	"sort"
)

type Dish struct {
	ID             string
	UnitPriceCents int64
	Quantity       float64
}

// LineTotalCents rounds to the nearest cent (Quantity may be fractional).
func (d Dish) LineTotalCents() int64 {
	return int64(math.Round(float64(d.UnitPriceCents) * d.Quantity))
}

type Portion struct {
	DishID   string
	PersonID string
	Shares   float64
}

type PersonBreakdown struct {
	PersonID  string `json:"personId"`
	OwedCents int64  `json:"owedCents"`
}

type Result struct {
	SubtotalCents     int64             `json:"subtotalCents"`
	People            []PersonBreakdown `json:"people"`
	UnassignedDishIDs []string          `json:"unassignedDishIds"`
}

// Compute allocates totalPaidCents across peopleIDs proportional to each
// person's share of each dish's value. A dish with zero total assigned
// shares is reported in UnassignedDishIDs and contributes nothing to
// anyone's total (its cost is not silently spread over the rest of the
// group) — callers should surface that as a warning before treating the
// result as final.
func Compute(dishes []Dish, portions []Portion, peopleIDs []string, totalPaidCents int64) Result {
	dishTotalShares := make(map[string]float64, len(dishes))
	personShareOfDish := make(map[string]map[string]float64, len(peopleIDs))
	for _, id := range peopleIDs {
		personShareOfDish[id] = make(map[string]float64)
	}
	for _, p := range portions {
		dishTotalShares[p.DishID] += p.Shares
		if _, ok := personShareOfDish[p.PersonID]; !ok {
			personShareOfDish[p.PersonID] = make(map[string]float64)
		}
		personShareOfDish[p.PersonID][p.DishID] += p.Shares
	}

	var subtotalCents int64
	unassigned := []string{}
	lineTotal := make(map[string]int64, len(dishes))
	for _, d := range dishes {
		lt := d.LineTotalCents()
		lineTotal[d.ID] = lt
		subtotalCents += lt
		if dishTotalShares[d.ID] <= 0 {
			unassigned = append(unassigned, d.ID)
		}
	}

	rawCents := make(map[string]float64, len(peopleIDs))
	for _, personID := range peopleIDs {
		var raw float64
		for dishID, shares := range personShareOfDish[personID] {
			total := dishTotalShares[dishID]
			if total <= 0 {
				continue
			}
			raw += (shares / total) * float64(lineTotal[dishID])
		}
		rawCents[personID] = raw
	}

	var scale float64
	if subtotalCents > 0 {
		scale = float64(totalPaidCents) / float64(subtotalCents)
	}

	ideal := make(map[string]float64, len(peopleIDs))
	var sumIdeal float64
	for _, personID := range peopleIDs {
		v := rawCents[personID] * scale
		ideal[personID] = v
		sumIdeal += v
	}

	// Largest-remainder rounding: floor everyone, then hand out the leftover
	// cents (the gap between the rounded target and the sum of floors) to
	// whoever has the largest fractional remainder. The target is the sum of
	// ideal allocations actually computed above, NOT always totalPaidCents —
	// those two only coincide when every dish has assigned shares; if a dish
	// is unassigned, its value is intentionally excluded from everyone's
	// ideal share, so the reconciled sum should match that reduced total.
	type rem struct {
		personID string
		frac     float64
	}
	floors := make(map[string]int64, len(peopleIDs))
	var sumFloors int64
	rems := make([]rem, 0, len(peopleIDs))
	for _, personID := range peopleIDs {
		f := math.Floor(ideal[personID])
		floors[personID] = int64(f)
		sumFloors += int64(f)
		rems = append(rems, rem{personID: personID, frac: ideal[personID] - f})
	}

	target := int64(math.Round(sumIdeal))
	leftover := target - sumFloors

	sort.SliceStable(rems, func(i, j int) bool { return rems[i].frac > rems[j].frac })
	for i := int64(0); i < leftover && i < int64(len(rems)); i++ {
		floors[rems[i].personID]++
	}

	out := make([]PersonBreakdown, 0, len(peopleIDs))
	for _, personID := range peopleIDs {
		out = append(out, PersonBreakdown{PersonID: personID, OwedCents: floors[personID]})
	}

	return Result{
		SubtotalCents:     subtotalCents,
		People:            out,
		UnassignedDishIDs: unassigned,
	}
}

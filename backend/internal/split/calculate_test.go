package split

import "testing"

func sumOwed(r Result) int64 {
	var s int64
	for _, p := range r.People {
		s += p.OwedCents
	}
	return s
}

func TestEvenSplit(t *testing.T) {
	dishes := []Dish{{ID: "d1", UnitPriceCents: 3000, Quantity: 1}}
	portions := []Portion{
		{DishID: "d1", PersonID: "alice", Shares: 1},
		{DishID: "d1", PersonID: "bob", Shares: 1},
		{DishID: "d1", PersonID: "cara", Shares: 1},
	}
	people := []string{"alice", "bob", "cara"}

	r := Compute(dishes, portions, people, 3000)

	if r.SubtotalCents != 3000 {
		t.Fatalf("subtotal = %d, want 3000", r.SubtotalCents)
	}
	if sumOwed(r) != 3000 {
		t.Fatalf("sum owed = %d, want 3000", sumOwed(r))
	}
	// 3000/3 = 1000 exactly, no remainder juggling needed
	for _, p := range r.People {
		if p.OwedCents != 1000 {
			t.Errorf("person %s owed %d, want 1000", p.PersonID, p.OwedCents)
		}
	}
}

func TestUnevenShares(t *testing.T) {
	dishes := []Dish{{ID: "d1", UnitPriceCents: 1000, Quantity: 1}}
	portions := []Portion{
		{DishID: "d1", PersonID: "alice", Shares: 2},
		{DishID: "d1", PersonID: "bob", Shares: 1},
	}
	people := []string{"alice", "bob"}

	r := Compute(dishes, portions, people, 1000)

	owed := map[string]int64{}
	for _, p := range r.People {
		owed[p.PersonID] = p.OwedCents
	}
	// alice: 2/3 of 1000 = 666.67 -> 667, bob: 1/3 = 333.33 -> 333 (or vice
	// versa depending on remainder tie-break) but must sum to exactly 1000.
	if sumOwed(r) != 1000 {
		t.Fatalf("sum owed = %d, want 1000", sumOwed(r))
	}
	if owed["alice"] < owed["bob"] {
		t.Errorf("alice (2 shares) should owe more than bob (1 share): alice=%d bob=%d", owed["alice"], owed["bob"])
	}
}

func TestUnassignedDishFlaggedAndExcluded(t *testing.T) {
	dishes := []Dish{
		{ID: "d1", UnitPriceCents: 1000, Quantity: 1},
		{ID: "d2", UnitPriceCents: 500, Quantity: 1}, // nobody assigned
	}
	portions := []Portion{
		{DishID: "d1", PersonID: "alice", Shares: 1},
	}
	people := []string{"alice"}

	r := Compute(dishes, portions, people, 1000)

	if len(r.UnassignedDishIDs) != 1 || r.UnassignedDishIDs[0] != "d2" {
		t.Fatalf("expected d2 flagged unassigned, got %v", r.UnassignedDishIDs)
	}
	if r.SubtotalCents != 1500 {
		t.Fatalf("subtotal should include unassigned dish's value: got %d, want 1500", r.SubtotalCents)
	}
	// alice's ideal share is only ever computed against her assigned dish,
	// scaled by totalPaid/subtotal — she should NOT be charged for d2.
	if sumOwed(r) == 1000 {
		t.Fatalf("alice should not absorb the unassigned dish's cost")
	}
}

func TestRoundingReconciliation(t *testing.T) {
	// A total-paid that doesn't divide evenly by 3, to exercise largest-
	// remainder rounding across many people.
	dishes := []Dish{{ID: "d1", UnitPriceCents: 3333, Quantity: 1}}
	people := []string{"a", "b", "c", "d", "e", "f", "g"}
	portions := make([]Portion, 0, len(people))
	for _, p := range people {
		portions = append(portions, Portion{DishID: "d1", PersonID: p, Shares: 1})
	}

	r := Compute(dishes, portions, people, 10007)

	if sumOwed(r) != 10007 {
		t.Fatalf("sum owed = %d, want 10007 (exact reconciliation with total paid)", sumOwed(r))
	}
}

func TestZeroSubtotalNoPanic(t *testing.T) {
	r := Compute(nil, nil, []string{"alice"}, 0)
	if sumOwed(r) != 0 {
		t.Fatalf("sum owed = %d, want 0", sumOwed(r))
	}
}

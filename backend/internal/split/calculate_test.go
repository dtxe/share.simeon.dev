package split

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

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

	r := Compute(dishes, portions, people, 3000, nil)

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

	r := Compute(dishes, portions, people, 1000, nil)

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

	r := Compute(dishes, portions, people, 1000, nil)

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

	r := Compute(dishes, portions, people, 10007, nil)

	if sumOwed(r) != 10007 {
		t.Fatalf("sum owed = %d, want 10007 (exact reconciliation with total paid)", sumOwed(r))
	}
}

func TestZeroSubtotalNoPanic(t *testing.T) {
	r := Compute(nil, nil, []string{"alice"}, 0, nil)
	if sumOwed(r) != 0 {
		t.Fatalf("sum owed = %d, want 0", sumOwed(r))
	}
}

func TestKnownTaxAllTaxableMatchesWholeScale(t *testing.T) {
	tax := int64(100)
	dishes := []Dish{{ID: "a", UnitPriceCents: 1000, Quantity: 1, Taxable: true}, {ID: "b", UnitPriceCents: 2000, Quantity: 1, Taxable: true}}
	p := []Portion{{DishID: "a", PersonID: "alice", Shares: 1}, {DishID: "b", PersonID: "bob", Shares: 1}}
	r := Compute(dishes, p, []string{"alice", "bob"}, 3300, &tax)
	if sumOwed(r) != 3300 || r.People[0].OwedCents != 1100 || r.People[1].OwedCents != 2200 {
		t.Fatalf("got %+v", r)
	}
}

func TestExemptDishReceivesNoTax(t *testing.T) {
	tax := int64(100)
	dishes := []Dish{{ID: "tax", UnitPriceCents: 1000, Quantity: 1, Taxable: true}, {ID: "exempt", UnitPriceCents: 1000, Quantity: 1}}
	p := []Portion{{DishID: "tax", PersonID: "a", Shares: 1}, {DishID: "exempt", PersonID: "b", Shares: 1}}
	r := Compute(dishes, p, []string{"a", "b"}, 2100, &tax)
	if r.People[0].TaxCents != 100 || r.People[1].TaxCents != 0 {
		t.Fatalf("tax allocation = %+v", r.People)
	}
	if r.People[1].OwedCents != 1000 || sumOwed(r) != 2100 {
		t.Fatalf("owed = %+v", r.People)
	}
}

func TestTaxFollowsUnevenPortions(t *testing.T) {
	tax := int64(90)
	r := Compute([]Dish{{ID: "d", UnitPriceCents: 1000, Quantity: 1, Taxable: true}}, []Portion{{DishID: "d", PersonID: "a", Shares: 2}, {DishID: "d", PersonID: "b", Shares: 1}}, []string{"a", "b"}, 1090, &tax)
	if r.People[0].TaxCents != 60 || r.People[1].TaxCents != 30 || sumOwed(r) != 1090 {
		t.Fatalf("got %+v", r.People)
	}
}

func TestKnownResidualPositiveAndNegative(t *testing.T) {
	for _, tc := range []struct {
		name        string
		total, want int64
	}{{"tip", 1200, 1200}, {"discount", 900, 900}} {
		t.Run(tc.name, func(t *testing.T) {
			tax := int64(100)
			r := Compute([]Dish{{ID: "d", UnitPriceCents: 1000, Quantity: 1, Taxable: true}}, []Portion{{DishID: "d", PersonID: "a", Shares: 1}}, []string{"a"}, tc.total, &tax)
			if sumOwed(r) != tc.want {
				t.Fatalf("sum=%d want %d", sumOwed(r), tc.want)
			}
		})
	}
}

func TestTaxWarningsAndUnassignedTax(t *testing.T) {
	tax := int64(100)
	r := Compute([]Dish{{ID: "d", UnitPriceCents: 1000, Quantity: 1, Taxable: true}}, nil, []string{"a"}, 1100, &tax)
	if r.UnallocatedTaxCents != 100 || len(r.UnassignedDishIDs) != 1 {
		t.Fatalf("got %+v", r)
	}
	r = Compute([]Dish{{ID: "e", UnitPriceCents: 1000, Quantity: 1}}, []Portion{{DishID: "e", PersonID: "a", Shares: 1}}, []string{"a"}, 1000, nil)
	if !r.TaxDetailsIncomplete {
		t.Fatal("unknown tax with exempt dish should be incomplete")
	}
}

func TestBreakdownJSONUsesEmptyArrays(t *testing.T) {
	b, err := json.Marshal(Compute(nil, nil, nil, 0, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"people":[]`, `"unassignedDishIds":[]`} {
		if !strings.Contains(string(b), field) {
			t.Fatalf("JSON missing %s: %s", field, b)
		}
	}
}

func TestNilTaxMatchesLegacyWholeBillScaling(t *testing.T) {
	dishes := []Dish{{ID: "a", UnitPriceCents: 1001, Quantity: 1, Taxable: true}, {ID: "b", UnitPriceCents: 2003, Quantity: 1, Taxable: true}}
	portions := []Portion{{DishID: "a", PersonID: "a", Shares: 1}, {DishID: "b", PersonID: "b", Shares: 2}, {DishID: "b", PersonID: "c", Shares: 1}}
	people := []string{"a", "b", "c"}
	want := Compute(dishes, portions, people, 3211, nil)
	nilTax := (*int64)(nil)
	got := Compute(dishes, portions, people, 3211, nilTax)
	if jsonValue(t, got) != jsonValue(t, want) {
		t.Fatalf("nil tax changed legacy result: got %+v want %+v", got, want)
	}
}

func TestKnownTaxZeroIsStillKnown(t *testing.T) {
	tax := int64(0)
	r := Compute([]Dish{{ID: "d", UnitPriceCents: 1000, Quantity: 1, Taxable: true}}, []Portion{{DishID: "d", PersonID: "a", Shares: 1}}, []string{"a"}, 1000, &tax)
	if r.TaxDetailsIncomplete || r.UnallocatedTaxCents != 0 || r.People[0].TaxCents != 0 || r.People[0].OwedCents != 1000 {
		t.Fatalf("zero tax result = %+v", r)
	}
}

func TestResidualSpansTaxableAndExemptDishes(t *testing.T) {
	dishes := []Dish{{ID: "tax", UnitPriceCents: 1000, Quantity: 1, Taxable: true}, {ID: "exempt", UnitPriceCents: 1000, Quantity: 1, Taxable: false}}
	p := []Portion{{DishID: "tax", PersonID: "a", Shares: 1}, {DishID: "exempt", PersonID: "b", Shares: 1}}
	tax := int64(100)
	positive := Compute(dishes, p, []string{"a", "b"}, 2200, &tax)
	negative := Compute(dishes, p, []string{"a", "b"}, 1900, &tax)
	if positive.People[0].OwedCents != 1150 || positive.People[1].OwedCents != 1050 {
		t.Fatalf("positive residual = %+v", positive.People)
	}
	if negative.People[0].OwedCents != 1000 || negative.People[1].OwedCents != 900 {
		t.Fatalf("negative residual = %+v", negative.People)
	}
}

func TestPartiallyUnassignedTaxableDishExcludesTaxAndResidual(t *testing.T) {
	dishes := []Dish{{ID: "tax", UnitPriceCents: 1000, Quantity: 1, Taxable: true}, {ID: "e", UnitPriceCents: 1000, Quantity: 1, Taxable: false}}
	p := []Portion{{DishID: "tax", PersonID: "a", Shares: 1}, {DishID: "tax", PersonID: "b", Shares: 1}, {DishID: "e", PersonID: "a", Shares: 1}}
	tax := int64(100)
	r := Compute(dishes, p, []string{"a", "b"}, 2100, &tax)
	if r.People[0].OwedCents != 1550 || r.People[1].OwedCents != 550 || r.UnallocatedTaxCents != 0 {
		t.Fatalf("partially assigned result = %+v, unallocated=%d", r.People, r.UnallocatedTaxCents)
	}

	p[1].Shares = 0
	dishes = append(dishes, Dish{ID: "unassigned", UnitPriceCents: 1000, Quantity: 1, Taxable: true})
	tax = 200
	r = Compute(dishes, p, []string{"a", "b"}, 3100, &tax)
	if r.People[0].OwedCents != 2033 || r.UnallocatedTaxCents != 100 {
		t.Fatalf("unassigned result = %+v, unallocated=%d", r.People, r.UnallocatedTaxCents)
	}
}

func TestPerPersonTaxLargestRemainder(t *testing.T) {
	tax := int64(1)
	r := Compute([]Dish{{ID: "d", UnitPriceCents: 100, Quantity: 1, Taxable: true}}, []Portion{
		{DishID: "d", PersonID: "a", Shares: 1}, {DishID: "d", PersonID: "b", Shares: 1}, {DishID: "d", PersonID: "c", Shares: 1},
	}, []string{"a", "b", "c"}, 101, &tax)
	if r.People[0].TaxCents != 1 || r.People[1].TaxCents != 0 || r.People[2].TaxCents != 0 {
		t.Fatalf("tax tie allocation = %+v", r.People)
	}
}

func TestNoTaxableItemsLeaveTaxUnallocated(t *testing.T) {
	tax := int64(75)
	r := Compute([]Dish{{ID: "e", UnitPriceCents: 1000, Quantity: 1}}, []Portion{{DishID: "e", PersonID: "a", Shares: 1}}, []string{"a"}, 1075, &tax)
	if r.UnallocatedTaxCents != 75 || r.People[0].OwedCents != 1000 || r.People[0].TaxCents != 0 {
		t.Fatalf("no taxable items = %+v, unallocated=%d", r.People, r.UnallocatedTaxCents)
	}
}

func TestInvalidValuesAndUnknownIDsAreSafe(t *testing.T) {
	r := Compute([]Dish{{ID: "d", UnitPriceCents: math.MaxInt64, Quantity: math.Inf(1)}}, []Portion{
		{DishID: "unknown", PersonID: "unknown", Shares: math.NaN()}, {DishID: "d", PersonID: "a", Shares: math.Inf(1)},
	}, []string{"a"}, math.MaxInt64, nil)
	if len(r.People) != 1 || r.People[0].PersonID != "a" || r.People[0].OwedCents != 0 {
		t.Fatalf("invalid input result = %+v", r)
	}
	if got := Compute(nil, nil, nil, 0, nil); len(got.People) != 0 {
		t.Fatalf("no people result = %+v", got)
	}
}

func jsonValue(t *testing.T, v Result) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

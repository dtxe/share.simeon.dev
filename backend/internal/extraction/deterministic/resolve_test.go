package deterministic

import "testing"

func TestResolvePriceFormat(t *testing.T) {
	tests := []struct {
		name          string
		items         []PrintedItem
		subtotalCents int64
		wantMatched   bool
		wantPrices    []int64 // resolved per-unit PriceCents, in item order
	}{
		{
			name: "printed price is already per-unit",
			items: []PrintedItem{
				{Name: "Pad Thai", PrintedPriceCents: 1200, Quantity: 1},
				{Name: "Soda", PrintedPriceCents: 300, Quantity: 2},
			},
			subtotalCents: 1800, // 1200*1 + 300*2
			wantMatched:   true,
			wantPrices:    []int64{1200, 300},
		},
		{
			name: "printed price is a line total",
			items: []PrintedItem{
				{Name: "Pad Thai", PrintedPriceCents: 1200, Quantity: 1},
				{Name: "Soda", PrintedPriceCents: 600, Quantity: 2}, // line total for 2 sodas
			},
			subtotalCents: 1800, // 1200 + 600, NOT 1200 + 600*2
			wantMatched:   true,
			wantPrices:    []int64{1200, 300}, // 600/2 = 300 per-unit
		},
		{
			name: "off-by-rounding still matches within tolerance",
			items: []PrintedItem{
				{Name: "A", PrintedPriceCents: 333, Quantity: 3}, // 999.999... rounds to 1000
			},
			subtotalCents: 1000,
			wantMatched:   true,
			wantPrices:    []int64{333},
		},
		{
			name: "neither interpretation matches",
			items: []PrintedItem{
				{Name: "Pad Thai", PrintedPriceCents: 1200, Quantity: 1},
			},
			subtotalCents: 5000,
			wantMatched:   false,
			wantPrices:    []int64{1200}, // fallback: treated as line total, qty 1 so unchanged
		},
		{
			name: "single-item receipt, per-unit matches",
			items: []PrintedItem{
				{Name: "Burger", PrintedPriceCents: 899, Quantity: 1},
			},
			subtotalCents: 899,
			wantMatched:   true,
			wantPrices:    []int64{899},
		},
		{
			name:          "zero items",
			items:         nil,
			subtotalCents: 0,
			wantMatched:   true,
			wantPrices:    []int64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, matched, _ := ResolvePriceFormat(tt.items, tt.subtotalCents)
			if matched != tt.wantMatched {
				t.Errorf("matched = %v, want %v", matched, tt.wantMatched)
			}
			if len(resolved) != len(tt.wantPrices) {
				t.Fatalf("resolved %d items, want %d", len(resolved), len(tt.wantPrices))
			}
			for i, want := range tt.wantPrices {
				if resolved[i].PriceCents != want {
					t.Errorf("item[%d].PriceCents = %d, want %d", i, resolved[i].PriceCents, want)
				}
			}
		})
	}
}

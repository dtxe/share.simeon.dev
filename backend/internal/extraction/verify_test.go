package extraction

import (
	"testing"

	"share/backend/internal/llm"
)

func TestCheckSubtotal(t *testing.T) {
	cases := []struct {
		name          string
		items         []llm.ExtractedItem
		subtotalCents int64
		wantMatched   bool
		wantDiff      int64
	}{
		{
			name: "exact match",
			items: []llm.ExtractedItem{
				{Name: "burger", PriceCents: 1200, Quantity: 1},
				{Name: "fries", PriceCents: 500, Quantity: 1},
			},
			subtotalCents: 1700,
			wantMatched:   true,
			wantDiff:      0,
		},
		{
			name: "quantity multiplies price",
			items: []llm.ExtractedItem{
				{Name: "soda", PriceCents: 300, Quantity: 3},
			},
			subtotalCents: 900,
			wantMatched:   true,
			wantDiff:      0,
		},
		{
			name: "refund subtracts from subtotal",
			items: []llm.ExtractedItem{
				{Name: "pitcher", PriceCents: 531, Quantity: 4},
				{Name: "chips", PriceCents: 509, Quantity: 2},
				{Name: "pitcher refund", PriceCents: -531, Quantity: 1},
			},
			subtotalCents: 2611,
			wantMatched:   true,
			wantDiff:      0,
		},
		{
			name: "off by rounding within tolerance",
			items: []llm.ExtractedItem{
				{Name: "a", PriceCents: 333, Quantity: 1},
				{Name: "b", PriceCents: 333, Quantity: 1},
				{Name: "c", PriceCents: 334, Quantity: 1},
			},
			subtotalCents: 1001,
			wantMatched:   true,
			wantDiff:      1,
		},
		{
			name: "no match",
			items: []llm.ExtractedItem{
				{Name: "burger", PriceCents: 1200, Quantity: 1},
			},
			subtotalCents: 500,
			wantMatched:   false,
			wantDiff:      700,
		},
		{
			name:          "zero items",
			items:         nil,
			subtotalCents: 0,
			wantMatched:   true,
			wantDiff:      0,
		},
		{
			name: "single item",
			items: []llm.ExtractedItem{
				{Name: "coffee", PriceCents: 400, Quantity: 1},
			},
			subtotalCents: 400,
			wantMatched:   true,
			wantDiff:      0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched, diff := CheckSubtotal(tc.items, tc.subtotalCents)
			if matched != tc.wantMatched || diff != tc.wantDiff {
				t.Fatalf("CheckSubtotal() = (%v, %d), want (%v, %d)", matched, diff, tc.wantMatched, tc.wantDiff)
			}
		})
	}
}

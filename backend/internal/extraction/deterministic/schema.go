// Package deterministic implements strategy 01 from
// docs/plans/01-strategy-deterministic-format-check.md: a single vision-LLM
// call that transcribes each item's printed price with no interpretation of
// whether it's a per-unit price or a line total, then a pure Go pass
// (ResolvePriceFormat) that decides which interpretation is correct by
// checking which one's sum matches the receipt's printed subtotal.
package deterministic

// PrintedItem is the wire shape the model returns: a line item's name,
// quantity, and its printed price exactly as shown, deliberately without
// resolving whether that price is per-unit or a line total.
type PrintedItem struct {
	Name              string  `json:"name"`
	PrintedPriceCents int64   `json:"printedPriceCents"`
	Quantity          float64 `json:"quantity"`
}

type printedReceipt struct {
	RestaurantName string        `json:"restaurantName,omitempty"`
	Date           string        `json:"date,omitempty"`
	SubtotalCents  int64         `json:"subtotalCents,omitempty"`
	TipCents       int64         `json:"tipCents,omitempty"`
	TotalPaidCents int64         `json:"totalPaidCents,omitempty"`
	Items          []PrintedItem `json:"items"`
}

const extractionPrompt = `You are transcribing a photo of a restaurant receipt. Do not interpret or
compute anything — record values exactly as printed.
Return the restaurant name, the date (ISO 8601, best effort), and every line item with its name,
quantity, and printedPriceCents: the price printed next to that line, in integer cents, exactly as
printed on the receipt. Do not decide whether printedPriceCents is a per-unit price or a line total —
just transcribe the number that appears.
Also return, in integer cents: the pre-tax subtotal (subtotalCents), any tip/gratuity amount
(tipCents), and the final total actually charged including tax and tip (totalPaidCents) — prefer a
credit-card/charged-amount line for totalPaidCents when one is printed. Only include amounts clearly
printed on the receipt; if a field can't be determined, omit it rather than guessing wildly.
Do not attempt to reconcile item prices against the subtotal or correct anything that looks wrong —
transcribe exactly what is printed, even if the math doesn't obviously add up to you.
Call the extract_receipt function with the result — do not respond in plain text.`

var extractionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"restaurantName": map[string]any{"type": "string"},
		"date":           map[string]any{"type": "string"},
		"subtotalCents":  map[string]any{"type": "integer"},
		"tipCents":       map[string]any{"type": "integer"},
		"totalPaidCents": map[string]any{"type": "integer"},
		"items": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":              map[string]any{"type": "string"},
					"printedPriceCents": map[string]any{"type": "integer", "description": "the price printed next to this line item, in cents, exactly as printed — do not decide whether it is a unit price or a line total"},
					"quantity":          map[string]any{"type": "number"},
				},
				"required": []string{"name", "printedPriceCents", "quantity"},
			},
		},
	},
	"required": []string{"items"},
}

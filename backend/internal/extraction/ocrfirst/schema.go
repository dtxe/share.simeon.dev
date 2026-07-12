package ocrfirst

const structuringPrompt = `You are structuring the raw OCR text transcribed from a photo of a restaurant
receipt. The OCR engine sometimes drops characters or misreads similarly-shaped glyphs (e.g. "0"/"O",
"1"/"l", "5"/"S") — use context (item pricing patterns, receipt layout conventions, arithmetic that
should reconcile) to reconstruct the most likely correct values, rather than transcribing OCR mistakes
literally.
Return the restaurant name, the date (ISO 8601, best effort), and every line item with its name,
unit price in integer cents (priceCents is the price for a single item, not the line total), and
quantity. Also return, in integer cents: the pre-tax subtotal (subtotalCents), any tip/gratuity
amount (tipCents), and the final total actually charged including tax and tip (totalPaidCents) —
prefer a credit-card/charged-amount line for totalPaidCents when one is printed. Only include
amounts you can reasonably reconstruct from the text; if a field can't be determined, omit it
rather than guessing wildly.
Where a pre-tax subtotal is present, verify that the sum of each item's priceCents times its
quantity equals subtotalCents. If they don't match, re-examine the OCR text for the most likely
misread — the most common errors are an OCR digit substitution, or the line total (unit price times
quantity) being recorded as priceCents instead of the per-item unit price.
Call the extract_receipt function with the result — do not respond in plain text.

---OCR TEXT---
`

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
					"name":       map[string]any{"type": "string"},
					"priceCents": map[string]any{"type": "integer", "description": "per-item unit price in cents, not the line total"},
					"quantity":   map[string]any{"type": "number"},
				},
				"required": []string{"name", "priceCents", "quantity"},
			},
		},
	},
	"required": []string{"items"},
}

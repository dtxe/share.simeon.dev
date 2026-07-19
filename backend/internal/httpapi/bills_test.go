package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUpdateSessionBodyTaxPresence(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		present bool
		value   *int64
	}{
		{name: "omitted", body: `{}`, present: false},
		{name: "null", body: `{"taxCents":null}`, present: true},
		{name: "zero", body: `{"taxCents":0}`, present: true, value: int64ptr(0)},
		{name: "amount", body: `{"taxCents":123}`, present: true, value: int64ptr(123)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body updateSessionBody
			decoder := json.NewDecoder(strings.NewReader(tt.body))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.TaxCents.Present != tt.present {
				t.Fatalf("present = %v, want %v", body.TaxCents.Present, tt.present)
			}
			if (body.TaxCents.Value == nil) != (tt.value == nil) {
				t.Fatalf("value = %v, want %v", body.TaxCents.Value, tt.value)
			}
			if tt.value != nil && *body.TaxCents.Value != *tt.value {
				t.Fatalf("value = %d, want %d", *body.TaxCents.Value, *tt.value)
			}
		})
	}
}

func int64ptr(value int64) *int64 { return &value }

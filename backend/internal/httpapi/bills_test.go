package httpapi

import (
	"strings"
	"testing"
)

func TestNormalizeNotes(t *testing.T) {
	value := func(s string) *string { return &s }

	tests := []struct {
		name string
		in   *string
		want *string
		ok   bool
	}{
		{name: "omitted", in: nil, want: nil, ok: true},
		{name: "trimmed", in: value("  Payment to Alex  "), want: value("Payment to Alex"), ok: true},
		{name: "blank clears", in: value(" \t\n "), want: value(""), ok: true},
		{name: "500 unicode characters", in: value(strings.Repeat("é", maxNotesCharacters)), want: value(strings.Repeat("é", maxNotesCharacters)), ok: true},
		{name: "501 unicode characters", in: value(strings.Repeat("é", maxNotesCharacters+1)), want: nil, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normalizeNotes(tt.in)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if got == nil || tt.want == nil {
				if got != nil || tt.want != nil {
					t.Fatalf("notes = %v, want %v", got, tt.want)
				}
				return
			}
			if *got != *tt.want {
				t.Fatalf("notes = %q, want %q", *got, *tt.want)
			}
		})
	}
}

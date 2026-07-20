package config

import "testing"

func TestValidateImageConverterURL(t *testing.T) {
	for _, raw := range []string{"http://image-converter:8080", "http://converter", "http://converter:65535/", "http://localhost:8080"} {
		if err := validateImageConverterURL(raw); err != nil {
			t.Errorf("%q rejected: %v", raw, err)
		}
	}
	for _, raw := range []string{"https://converter", "http://user:pass@converter", "http://converter/path", "http://converter?x=1", "http://converter#x", "http://converter:bad"} {
		if err := validateImageConverterURL(raw); err == nil {
			t.Errorf("%q accepted", raw)
		}
	}
}

func TestValidateImageConverterTimeout(t *testing.T) {
	if err := validateImageConverterTimeout(25); err != nil {
		t.Fatal(err)
	}
	for _, value := range []int{0, 26} {
		if err := validateImageConverterTimeout(value); err == nil {
			t.Errorf("timeout %d accepted", value)
		}
	}
}

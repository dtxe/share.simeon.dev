package openaicompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractReceiptParsesResponse(t *testing.T) {
	var gotAuth, gotModel string
	var gotBody chatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		gotModel = gotBody.Model

		if gotBody.ResponseFormat.Type != "json_schema" {
			t.Errorf("expected json_schema response_format, got %q", gotBody.ResponseFormat.Type)
		}
		if len(gotBody.Messages) != 1 || len(gotBody.Messages[0].Content) != 2 {
			t.Fatalf("expected one message with text+image_url parts, got %+v", gotBody.Messages)
		}
		if gotBody.Messages[0].Content[1].ImageURL == nil {
			t.Fatalf("expected image_url content part")
		}

		resp := chatResponse{}
		resp.Choices = []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{
			{Message: struct {
				Content string `json:"content"`
			}{Content: `{"restaurant_name":"Thai Basil","date":"2026-07-05","items":[{"name":"Pad Thai","price_cents":1200,"quantity":1}]}`}},
		}
		resp.Usage.PromptTokens = 500
		resp.Usage.CompletionTokens = 80

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key", "test-model")
	result, err := c.ExtractReceipt(context.Background(), []byte("fake-jpeg-bytes"), "image/jpeg")
	if err != nil {
		t.Fatalf("ExtractReceipt: %v", err)
	}

	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotModel != "test-model" {
		t.Errorf("model = %q, want %q", gotModel, "test-model")
	}
	if result.Receipt.RestaurantName != "Thai Basil" {
		t.Errorf("restaurant name = %q, want %q", result.Receipt.RestaurantName, "Thai Basil")
	}
	if len(result.Receipt.Items) != 1 || result.Receipt.Items[0].Name != "Pad Thai" || result.Receipt.Items[0].PriceCents != 1200 {
		t.Errorf("unexpected items: %+v", result.Receipt.Items)
	}
	if result.Usage.PromptTokens != 500 || result.Usage.CompletionTokens != 80 {
		t.Errorf("unexpected usage: %+v", result.Usage)
	}
}

func TestExtractReceiptUpstreamErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key", "test-model")
	_, err := c.ExtractReceipt(context.Background(), []byte("x"), "image/jpeg")
	if err == nil {
		t.Fatal("expected an error on non-200 upstream response")
	}
}

func TestExtractReceiptRejectsUnknownFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{}
		resp.Choices = []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{
			{Message: struct {
				Content string `json:"content"`
			}{Content: `{"items":[],"unexpected_field":"should cause a decode error"}`}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key", "test-model")
	_, err := c.ExtractReceipt(context.Background(), []byte("x"), "image/jpeg")
	if err == nil {
		t.Fatal("expected decode to reject an unexpected field in the model's JSON output")
	}
}

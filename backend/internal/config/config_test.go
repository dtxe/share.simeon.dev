package config

import (
	"strings"
	"testing"
)

func TestLoadDefaultsToMinimaxM3Pricing(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LLMModel != defaultLLMModel {
		t.Fatalf("LLMModel = %q, want %q", cfg.LLMModel, defaultLLMModel)
	}
	if cfg.LLMInputCostPer1KTokensCents != 0.03 || cfg.LLMOutputCostPer1KTokensCents != 0.12 {
		t.Fatalf("LLM costs = input %v output %v, want input 0.03 output 0.12",
			cfg.LLMInputCostPer1KTokensCents, cfg.LLMOutputCostPer1KTokensCents)
	}
}

func TestLoadUsesSupportedModelPricing(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LLM_MODEL", "accounts/fireworks/models/kimi-k2p7-code")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LLMInputCostPer1KTokensCents != 0.095 || cfg.LLMOutputCostPer1KTokensCents != 0.4 {
		t.Fatalf("LLM costs = input %v output %v, want input 0.095 output 0.4",
			cfg.LLMInputCostPer1KTokensCents, cfg.LLMOutputCostPer1KTokensCents)
	}
}

func TestLoadAllowsCustomPricingForUnsupportedModel(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LLM_MODEL", "accounts/example/models/custom")
	t.Setenv("LLM_INPUT_COST_PER_1K_TOKENS_CENTS", "1.5")
	t.Setenv("LLM_OUTPUT_COST_PER_1K_TOKENS_CENTS", "2.5")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LLMInputCostPer1KTokensCents != 1.5 || cfg.LLMOutputCostPer1KTokensCents != 2.5 {
		t.Fatalf("LLM costs = input %v output %v, want input 1.5 output 2.5",
			cfg.LLMInputCostPer1KTokensCents, cfg.LLMOutputCostPer1KTokensCents)
	}
}

func TestLoadRejectsUnsupportedModelWithoutPricing(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LLM_MODEL", "accounts/example/models/custom")

	_, err := Load()
	if err == nil {
		t.Fatal("expected unsupported model without explicit pricing to fail")
	}
	if !strings.Contains(err.Error(), "has no built-in pricing") {
		t.Fatalf("error = %q, want no built-in pricing", err.Error())
	}
}

func TestLoadRequiresInputAndOutputPricingTogether(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LLM_INPUT_COST_PER_1K_TOKENS_CENTS", "1.5")

	_, err := Load()
	if err == nil {
		t.Fatal("expected partial explicit pricing to fail")
	}
	if !strings.Contains(err.Error(), "must be set together") {
		t.Fatalf("error = %q, want must be set together", err.Error())
	}
}

func TestLoadDefaultsReceiptSpendCap(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLMMaxSpendPerReceiptCents != 5 {
		t.Fatalf("LLMMaxSpendPerReceiptCents = %d, want 5", cfg.LLMMaxSpendPerReceiptCents)
	}
}

func TestLoadRejectsNonPositiveReceiptSpendCap(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LLM_MAX_SPEND_PER_RECEIPT_CENTS", "0")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "LLM_MAX_SPEND_PER_RECEIPT_CENTS must be positive") {
		t.Fatalf("Load error = %v, want positive receipt spend cap error", err)
	}
}

func TestParseS3CredentialsRequiresExactFields(t *testing.T) {
	got, err := ParseS3Credentials(`{"userName":"u","accessKey":"a","secretKey":"s"}`)
	if err != nil || got.AccessKey != "a" {
		t.Fatalf("ParseS3Credentials = %#v, %v", got, err)
	}
	if _, err := ParseS3Credentials(`{"userName":"u","accessKey":"a"}`); err == nil {
		t.Fatal("expected missing secretKey error")
	}
	if _, err := ParseS3Credentials(`{"userName":"u","accessKey":"a","secretKey":"s","extra":"x"}`); err == nil {
		t.Fatal("expected unknown field error")
	}
	if _, err := ParseS3Credentials(`{"userName":"u","accessKey":"a","secretKey":"s"} {}`); err == nil {
		t.Fatal("expected trailing JSON error")
	}
}

func TestLoadRejectsInvalidS3Endpoint(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("RECEIPT_STORAGE", "s3")
	t.Setenv("S3_CREDENTIALS", `{"userName":"u","accessKey":"a","secretKey":"s"}`)
	t.Setenv("S3_ENDPOINT", "http://localhost:9000")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "absolute HTTPS") {
		t.Fatalf("Load error = %v", err)
	}
}

func setRequiredEnv(t *testing.T) {
	t.Helper()

	t.Setenv("DATABASE_URL", "postgres://share_app:password@localhost:5432/share?sslmode=disable")
	t.Setenv("RECEIPT_STORAGE", "local")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("EMAIL_OTP_ENABLED", "false")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("LLM_MODEL_FILE", "")
	t.Setenv("LLM_INPUT_COST_PER_1K_TOKENS_CENTS", "")
	t.Setenv("LLM_INPUT_COST_PER_1K_TOKENS_CENTS_FILE", "")
	t.Setenv("LLM_OUTPUT_COST_PER_1K_TOKENS_CENTS", "")
	t.Setenv("LLM_OUTPUT_COST_PER_1K_TOKENS_CENTS_FILE", "")
	t.Setenv("LLM_MAX_SPEND_PER_RECEIPT_CENTS", "")
}

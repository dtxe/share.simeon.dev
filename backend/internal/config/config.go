package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr          string
	PublicBaseURL     string
	CORSAllowedOrigin string
	TrustedProxy      bool

	DatabaseURL string
	RedisURL    string

	AnonAccountsEnabled       bool
	AnonIdentityTransport     string // "cookie" | "header"
	AnonSessionCookieName     string
	AnonSessionHeaderName     string
	AnonSessionTTLDays        int
	SessionTouchMinIntervalHr int

	PasskeyAccountsEnabled bool
	PasskeyRPID            string
	PasskeyRPName          string
	PasskeyOrigin          string

	EmailOTPEnabled          bool
	OTPCodeTTLSeconds        int
	OTPMaxAttempts           int
	OTPResendCooldownSeconds int
	OTPRequestRatePerIPPerHr int

	EmailProvider string
	SMTPHost      string
	SMTPPort      int
	SMTPUser      string
	SMTPPass      string
	SMTPFrom      string

	LLMProvider             string
	LLMBaseURL              string
	LLMModel                string
	LLMAPIKey               string
	LLMCostPer1KTokensCents float64
	LLMDailySpendCapCents   int

	UploadDir string
}

// Load reads configuration from the environment. Any FOO env var may instead
// be supplied as FOO_FILE, pointing at a file whose contents are the value
// (used for docker secrets in production).
func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:          getEnv("HTTP_ADDR", ":8080"),
		PublicBaseURL:     getEnv("PUBLIC_BASE_URL", "http://localhost:5173"),
		CORSAllowedOrigin: getEnv("CORS_ALLOWED_ORIGIN", ""),
		TrustedProxy:      getBool("TRUSTED_PROXY", false),

		DatabaseURL: getEnv("DATABASE_URL", ""),
		RedisURL:    getEnv("REDIS_URL", ""),

		AnonAccountsEnabled:       getBool("ANON_ACCOUNTS_ENABLED", true),
		AnonIdentityTransport:     getEnv("ANON_IDENTITY_TRANSPORT", "cookie"),
		AnonSessionCookieName:     getEnv("ANON_SESSION_COOKIE_NAME", "cher_sid"),
		AnonSessionHeaderName:     getEnv("ANON_SESSION_HEADER_NAME", "X-Anon-Session-Token"),
		AnonSessionTTLDays:        getInt("ANON_SESSION_TTL_DAYS", 730),
		SessionTouchMinIntervalHr: getInt("SESSION_TOUCH_MIN_INTERVAL_HOURS", 24),

		PasskeyAccountsEnabled: getBool("PASSKEY_ACCOUNTS_ENABLED", false),
		PasskeyRPID:            getEnv("PASSKEY_RP_ID", "localhost"),
		PasskeyRPName:          getEnv("PASSKEY_RP_NAME", "Cher"),
		PasskeyOrigin:          getEnv("PASSKEY_ORIGIN", "http://localhost:5173"),

		EmailOTPEnabled:          getBool("EMAIL_OTP_ENABLED", true),
		OTPCodeTTLSeconds:        getInt("OTP_CODE_TTL_SECONDS", 600),
		OTPMaxAttempts:           getInt("OTP_MAX_ATTEMPTS", 5),
		OTPResendCooldownSeconds: getInt("OTP_RESEND_COOLDOWN_SECONDS", 60),
		OTPRequestRatePerIPPerHr: getInt("OTP_REQUEST_RATE_PER_IP_PER_HOUR", 10),

		EmailProvider: getEnv("EMAIL_PROVIDER", "smtp"),
		SMTPHost:      getEnv("SMTP_HOST", ""),
		SMTPPort:      getInt("SMTP_PORT", 1025),
		SMTPUser:      getEnv("SMTP_USER", ""),
		SMTPPass:      getEnv("SMTP_PASS", ""),
		SMTPFrom:      getEnv("SMTP_FROM", "cher@localhost"),

		LLMProvider:             getEnv("LLM_PROVIDER", "fireworks"),
		LLMBaseURL:              getEnv("LLM_BASE_URL", "https://api.fireworks.ai/inference/v1"),
		LLMModel:                getEnv("LLM_MODEL", "accounts/fireworks/models/kimi-k2p7-code"),
		LLMAPIKey:               getEnv("LLM_API_KEY", ""),
		LLMCostPer1KTokensCents: getFloat("LLM_COST_PER_1K_TOKENS_CENTS", 0.2),
		LLMDailySpendCapCents:   getInt("LLM_DAILY_SPEND_CAP_CENTS", 100),

		UploadDir: getEnv("UPLOAD_DIR", "/data/uploads"),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.RedisURL == "" {
		return nil, fmt.Errorf("REDIS_URL is required")
	}
	if cfg.AnonIdentityTransport != "cookie" && cfg.AnonIdentityTransport != "header" {
		return nil, fmt.Errorf("ANON_IDENTITY_TRANSPORT must be 'cookie' or 'header', got %q", cfg.AnonIdentityTransport)
	}

	return cfg, nil
}

// getEnv resolves FOO from the environment, preferring FOO_FILE (whose
// contents are read and trimmed) when present, for docker-secrets support.
func getEnv(key, def string) string {
	if filePath := os.Getenv(key + "_FILE"); filePath != "" {
		b, err := os.ReadFile(filePath)
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getBool(key string, def bool) bool {
	v := getEnv(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getInt(key string, def int) int {
	v := getEnv(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getFloat(key string, def float64) float64 {
	v := getEnv(key, "")
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

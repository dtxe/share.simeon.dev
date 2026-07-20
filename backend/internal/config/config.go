package config

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"
)

type Config struct {
	ReceiptStorage                           string
	S3Credentials                            S3Credentials
	S3Endpoint, S3Bucket, S3Region, S3Prefix string
	S3ProxyHost                              string
	HTTPAddr                                 string
	PublicBaseURL                            string
	CORSAllowedOrigin                        string
	TrustedProxy                             bool
	// RealIPHeader, when set and TrustedProxy is true, is the request header
	// that carries the true client address (e.g. "CF-Connecting-IP" behind
	// Cloudflare). Empty falls back to X-Forwarded-For's last hop.
	RealIPHeader string

	DatabaseURL string
	RedisURL    string

	// DB* are used to build DatabaseURL when DATABASE_URL itself isn't set —
	// lets the password come from a docker secret file (DB_PASSWORD_FILE)
	// without needing compose to interpolate secret contents into env vars.
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

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
	OTPVerifyRatePerIPPerHr  int
	OTPHashPepper            string

	EmailProvider string
	SMTPHost      string
	SMTPPort      int
	SMTPUser      string
	SMTPPass      string
	SMTPFrom      string
	SMTPTLSMode   string

	LLMProvider                   string
	LLMBaseURL                    string
	LLMModel                      string
	LLMAPIKey                     string
	LLMInputCostPer1KTokensCents  float64
	LLMOutputCostPer1KTokensCents float64
	LLMDailySpendCapCents         int
	LLMMaxSpendPerReceiptCents    int

	ExtractionStrategy string

	UploadDir string

	Debug bool
}

type S3Credentials struct {
	UserName  string `json:"userName"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

// MigrationConfig contains only the configuration needed by receipt-migrate.
// Keeping this separate prevents an operations command from requiring the
// application's Redis, SMTP, or LLM settings.
type MigrationConfig struct {
	DatabaseURL, UploadDir                                string
	S3Endpoint, S3Bucket, S3Region, S3Prefix, S3ProxyHost string
	S3Credentials                                         S3Credentials
}

func LoadMigration() (*MigrationConfig, error) {
	var err error
	c := &MigrationConfig{}
	if c.DatabaseURL, err = getEnvSafe("DATABASE_URL", ""); err != nil {
		return nil, err
	}
	if c.UploadDir, err = getEnvSafe("UPLOAD_DIR", "/data/uploads"); err != nil {
		return nil, err
	}
	if c.S3Endpoint, err = getEnvSafe("S3_ENDPOINT", "https://s3.bhs.io.cloud.ovh.net"); err != nil {
		return nil, err
	}
	if c.S3Bucket, err = getEnvSafe("S3_BUCKET", "share-app"); err != nil {
		return nil, err
	}
	if c.S3Region, err = getEnvSafe("S3_REGION", "bhs"); err != nil {
		return nil, err
	}
	if c.S3Prefix, err = getEnvSafe("S3_PREFIX", "receipts"); err != nil {
		return nil, err
	}
	if c.S3ProxyHost, err = getEnvSafe("S3_PROXY_HOST", "share-app.s3.bhs.io.cloud.ovh.net"); err != nil {
		return nil, err
	}
	if c.DatabaseURL == "" {
		var u, p, n string
		if u, err = getEnvSafe("DB_USER", ""); err != nil {
			return nil, err
		}
		if p, err = getEnvSafe("DB_PASSWORD", ""); err != nil {
			return nil, err
		}
		if n, err = getEnvSafe("DB_NAME", ""); err != nil {
			return nil, err
		}
		var host, port string
		if host, err = getEnvSafe("DB_HOST", "postgres"); err != nil {
			return nil, err
		}
		if port, err = getEnvSafe("DB_PORT", "5432"); err != nil {
			return nil, err
		}
		if u == "" || p == "" || n == "" {
			return nil, fmt.Errorf("DATABASE_URL is required (or DB_HOST/DB_PORT/DB_USER/DB_PASSWORD[_FILE]/DB_NAME)")
		}
		c.DatabaseURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", url.QueryEscape(u), url.QueryEscape(p), host, port, url.QueryEscape(n))
	}
	if err := validateS3Settings(c.S3Endpoint, c.S3Bucket, c.S3Region, c.S3Prefix, c.S3ProxyHost); err != nil {
		return nil, err
	}
	var rawCredentials string
	if rawCredentials, err = getEnvSafe("S3_CREDENTIALS", ""); err != nil {
		return nil, err
	}
	c.S3Credentials, err = ParseS3Credentials(rawCredentials)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func validateS3Settings(endpointRaw, bucket, region, prefix, proxyHost string) error {
	endpoint, err := url.Parse(endpointRaw)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.Hostname() == "" || endpoint.User != nil ||
		endpoint.Port() != "" || (endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("S3_ENDPOINT must be an HTTPS URL with a host and no user, port, query, fragment, or non-root path")
	}
	if bucket == "" || region == "" || proxyHost == "" {
		return fmt.Errorf("S3_BUCKET, S3_REGION, and S3_PROXY_HOST are required")
	}
	if strings.ContainsAny(proxyHost, "/?#@: \t\r\n") {
		return fmt.Errorf("S3_PROXY_HOST must be a hostname without scheme, port, or path")
	}
	wantHost := bucket + "." + endpoint.Hostname()
	if !strings.EqualFold(proxyHost, wantHost) {
		return fmt.Errorf("S3_PROXY_HOST must equal %q for virtual-hosted addressing", wantHost)
	}
	if prefix == "" || strings.HasPrefix(prefix, "/") || strings.HasSuffix(prefix, "/") ||
		strings.Contains(prefix, "..") || strings.ContainsAny(prefix, "\\?#") {
		return fmt.Errorf("S3_PREFIX must be a non-empty safe path prefix")
	}
	for _, r := range prefix {
		if unicode.IsControl(r) {
			return fmt.Errorf("S3_PREFIX must not contain control characters")
		}
	}
	for _, segment := range strings.Split(prefix, "/") {
		if segment == "" {
			return fmt.Errorf("S3_PREFIX must not contain empty path segments")
		}
	}
	return nil
}

func getEnvSafe(key, def string) (string, error) {
	if p := os.Getenv(key + "_FILE"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("%s_FILE (%s): %w", key, p, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	if v := os.Getenv(key); v != "" {
		return v, nil
	}
	return def, nil
}

func ParseS3Credentials(raw string) (S3Credentials, error) {
	var c S3Credentials
	if strings.TrimSpace(raw) == "" {
		return c, fmt.Errorf("S3_CREDENTIALS[_FILE] is required")
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("S3_CREDENTIALS is invalid JSON")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return c, fmt.Errorf("S3_CREDENTIALS must contain exactly one JSON object")
	}
	if c.UserName == "" || c.AccessKey == "" || c.SecretKey == "" {
		return c, fmt.Errorf("S3_CREDENTIALS must contain non-empty userName, accessKey, and secretKey")
	}
	return c, nil
}

type LLMPricing struct {
	InputCostPer1KTokensCents  float64
	OutputCostPer1KTokensCents float64
}

const defaultLLMModel = "accounts/fireworks/models/kimi-k2p7-code"

var supportedLLMModelPricing = map[string]LLMPricing{
	"accounts/fireworks/models/minimax-m3": {
		InputCostPer1KTokensCents:  0.03,
		OutputCostPer1KTokensCents: 0.12,
	},
	"accounts/fireworks/models/kimi-k2p7-code": {
		InputCostPer1KTokensCents:  0.095,
		OutputCostPer1KTokensCents: 0.4,
	},
}

// Load reads configuration from the environment. Any FOO env var may instead
// be supplied as FOO_FILE, pointing at a file whose contents are the value
// (used for docker secrets in production).
func Load() (*Config, error) {
	cfg := &Config{
		ReceiptStorage:    getEnv("RECEIPT_STORAGE", "s3"),
		S3Endpoint:        getEnv("S3_ENDPOINT", "https://s3.bhs.io.cloud.ovh.net"),
		S3Bucket:          getEnv("S3_BUCKET", "share-app"),
		S3Region:          getEnv("S3_REGION", "bhs"),
		S3Prefix:          getEnv("S3_PREFIX", "receipts"),
		S3ProxyHost:       getEnv("S3_PROXY_HOST", "share-app.s3.bhs.io.cloud.ovh.net"),
		HTTPAddr:          getEnv("HTTP_ADDR", ":8080"),
		PublicBaseURL:     getEnv("PUBLIC_BASE_URL", "http://localhost:5173"),
		CORSAllowedOrigin: getEnv("CORS_ALLOWED_ORIGIN", ""),
		TrustedProxy:      getBool("TRUSTED_PROXY", false),
		RealIPHeader:      getEnv("REAL_IP_HEADER", ""),

		DatabaseURL: getEnv("DATABASE_URL", ""),
		RedisURL:    getEnv("REDIS_URL", ""),

		DBHost:     getEnv("DB_HOST", "postgres"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", ""),
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBName:     getEnv("DB_NAME", ""),

		AnonAccountsEnabled:       getBool("ANON_ACCOUNTS_ENABLED", true),
		AnonIdentityTransport:     getEnv("ANON_IDENTITY_TRANSPORT", "cookie"),
		AnonSessionCookieName:     getEnv("ANON_SESSION_COOKIE_NAME", "share_sid"),
		AnonSessionHeaderName:     getEnv("ANON_SESSION_HEADER_NAME", "X-Anon-Session-Token"),
		AnonSessionTTLDays:        getInt("ANON_SESSION_TTL_DAYS", 30),
		SessionTouchMinIntervalHr: getInt("SESSION_TOUCH_MIN_INTERVAL_HOURS", 24),

		PasskeyAccountsEnabled: getBool("PASSKEY_ACCOUNTS_ENABLED", false),
		PasskeyRPID:            getEnv("PASSKEY_RP_ID", "localhost"),
		PasskeyRPName:          getEnv("PASSKEY_RP_NAME", "Share"),
		PasskeyOrigin:          getEnv("PASSKEY_ORIGIN", "http://localhost:5173"),

		EmailOTPEnabled:          getBool("EMAIL_OTP_ENABLED", true),
		OTPCodeTTLSeconds:        getInt("OTP_CODE_TTL_SECONDS", 600),
		OTPMaxAttempts:           getInt("OTP_MAX_ATTEMPTS", 5),
		OTPResendCooldownSeconds: getInt("OTP_RESEND_COOLDOWN_SECONDS", 60),
		OTPRequestRatePerIPPerHr: getInt("OTP_REQUEST_RATE_PER_IP_PER_HOUR", 10),
		OTPVerifyRatePerIPPerHr:  getInt("OTP_VERIFY_RATE_PER_IP_PER_HOUR", 30),
		OTPHashPepper:            getEnv("OTP_HASH_PEPPER", ""),

		EmailProvider: getEnv("EMAIL_PROVIDER", "smtp"),
		SMTPHost:      getEnv("SMTP_HOST", ""),
		SMTPPort:      getInt("SMTP_PORT", 587),
		SMTPUser:      getEnv("SMTP_USER", ""),
		SMTPPass:      getEnv("SMTP_PASS", ""),
		SMTPFrom:      getEnv("SMTP_FROM", ""),
		SMTPTLSMode:   getEnv("SMTP_TLS_MODE", "starttls"),

		LLMProvider:                getEnv("LLM_PROVIDER", "fireworks"),
		LLMBaseURL:                 getEnv("LLM_BASE_URL", "https://api.fireworks.ai/inference/v1"),
		LLMModel:                   getEnv("LLM_MODEL", defaultLLMModel),
		LLMAPIKey:                  getEnv("LLM_API_KEY", ""),
		LLMDailySpendCapCents:      getInt("LLM_DAILY_SPEND_CAP_CENTS", 100),
		LLMMaxSpendPerReceiptCents: getInt("LLM_MAX_SPEND_PER_RECEIPT_CENTS", 8),

		ExtractionStrategy: getEnv("EXTRACTION_STRATEGY", "baseline"),

		UploadDir: getEnv("UPLOAD_DIR", "/data/uploads"),

		Debug: getBool("DEBUG", false),
	}
	if cfg.ReceiptStorage != "s3" && cfg.ReceiptStorage != "local" {
		return nil, fmt.Errorf("RECEIPT_STORAGE must be 's3' or 'local', got %q", cfg.ReceiptStorage)
	}
	if cfg.ReceiptStorage == "s3" {
		if err := validateS3Settings(cfg.S3Endpoint, cfg.S3Bucket, cfg.S3Region, cfg.S3Prefix, cfg.S3ProxyHost); err != nil {
			return nil, err
		}
		var err error
		cfg.S3Credentials, err = ParseS3Credentials(getEnv("S3_CREDENTIALS", ""))
		if err != nil {
			return nil, err
		}
	}

	if cfg.DatabaseURL == "" {
		if cfg.DBUser == "" || cfg.DBPassword == "" || cfg.DBName == "" {
			return nil, fmt.Errorf("DATABASE_URL is required (or DB_HOST/DB_PORT/DB_USER/DB_PASSWORD[_FILE]/DB_NAME)")
		}
		cfg.DatabaseURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			url.QueryEscape(cfg.DBUser), url.QueryEscape(cfg.DBPassword), cfg.DBHost, cfg.DBPort, cfg.DBName)
	}
	if cfg.RedisURL == "" {
		return nil, fmt.Errorf("REDIS_URL is required")
	}
	if cfg.LLMDailySpendCapCents <= 0 {
		return nil, fmt.Errorf("LLM_DAILY_SPEND_CAP_CENTS must be positive")
	}
	if cfg.LLMMaxSpendPerReceiptCents <= 0 {
		return nil, fmt.Errorf("LLM_MAX_SPEND_PER_RECEIPT_CENTS must be positive")
	}
	if cfg.AnonIdentityTransport != "cookie" && cfg.AnonIdentityTransport != "header" {
		return nil, fmt.Errorf("ANON_IDENTITY_TRANSPORT must be 'cookie' or 'header', got %q", cfg.AnonIdentityTransport)
	}
	inputCost, inputCostSet := getOptionalFloat("LLM_INPUT_COST_PER_1K_TOKENS_CENTS")
	outputCost, outputCostSet := getOptionalFloat("LLM_OUTPUT_COST_PER_1K_TOKENS_CENTS")
	if inputCostSet != outputCostSet {
		return nil, fmt.Errorf("LLM_INPUT_COST_PER_1K_TOKENS_CENTS and LLM_OUTPUT_COST_PER_1K_TOKENS_CENTS must be set together")
	}
	if inputCostSet {
		cfg.LLMInputCostPer1KTokensCents = inputCost
		cfg.LLMOutputCostPer1KTokensCents = outputCost
	} else {
		pricing, ok := supportedLLMModelPricing[cfg.LLMModel]
		if !ok {
			return nil, fmt.Errorf("LLM_MODEL %q has no built-in pricing; set LLM_INPUT_COST_PER_1K_TOKENS_CENTS and LLM_OUTPUT_COST_PER_1K_TOKENS_CENTS", cfg.LLMModel)
		}
		cfg.LLMInputCostPer1KTokensCents = pricing.InputCostPer1KTokensCents
		cfg.LLMOutputCostPer1KTokensCents = pricing.OutputCostPer1KTokensCents
	}
	if cfg.EmailOTPEnabled {
		if cfg.OTPHashPepper == "" {
			return nil, fmt.Errorf("OTP_HASH_PEPPER[_FILE] is required when EMAIL_OTP_ENABLED=true")
		}
		if cfg.EmailProvider != "smtp" {
			return nil, fmt.Errorf("EMAIL_PROVIDER must be 'smtp', got %q", cfg.EmailProvider)
		}
		if cfg.SMTPHost == "" || cfg.SMTPFrom == "" || cfg.SMTPUser == "" || cfg.SMTPPass == "" {
			return nil, fmt.Errorf("SMTP_HOST, SMTP_FROM, SMTP_USER, and SMTP_PASS[_FILE] are required when EMAIL_OTP_ENABLED=true")
		}
		if cfg.SMTPPort <= 0 || cfg.SMTPPort > 65535 {
			return nil, fmt.Errorf("SMTP_PORT must be between 1 and 65535")
		}
		if cfg.SMTPTLSMode != "starttls" && cfg.SMTPTLSMode != "smtps" {
			return nil, fmt.Errorf("SMTP_TLS_MODE must be 'starttls' or 'smtps', got %q", cfg.SMTPTLSMode)
		}
	}

	return cfg, nil
}

// getEnv resolves FOO from the environment, preferring FOO_FILE (whose
// contents are read and trimmed) when present, for docker-secrets support.
// A FOO_FILE that's set but unreadable is a misconfiguration, not a signal
// to silently fall through to FOO/the default — that would mask a broken
// secret mount as if it were simply unset.
func getEnv(key, def string) string {
	if filePath := os.Getenv(key + "_FILE"); filePath != "" {
		b, err := os.ReadFile(filePath)
		if err != nil {
			log.Fatalf("config: reading %s_FILE (%s): %v", key, filePath, err)
		}
		return strings.TrimSpace(string(b))
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
		log.Fatalf("config: %s: invalid bool %q: %v", key, v, err)
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
		log.Fatalf("config: %s: invalid int %q: %v", key, v, err)
	}
	return n
}

func getOptionalFloat(key string) (float64, bool) {
	v := getEnv(key, "")
	if v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		log.Fatalf("config: %s: invalid float %q: %v", key, v, err)
	}
	return f, true
}

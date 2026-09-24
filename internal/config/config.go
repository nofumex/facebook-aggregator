package config

import (
	"encoding/base64"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL               string
	TelegramToken             string
	AdminIDs                  map[int64]bool
	EncryptionKey             []byte
	HTTPAddr                  string
	DefaultPoll               time.Duration
	WorkerConcurrency         int
	DBMaxConns                int
	DBMinConns                int
	DBBackgroundMaxConns      int
	BackfillConcurrency       int
	ExtractionRetryInterval   time.Duration
	ExtractionRetryBatch      int
	RerankInterval            time.Duration
	RerankBatch               int
	CollectionRefreshInterval time.Duration
	CollectionRefreshTimeout  time.Duration
	Facebook                  Facebook
	Extraction                LLMExtraction
}

type LLMExtraction struct {
	Enabled       bool
	BaseURL       string
	APIKey        string
	Timeout       time.Duration
	Concurrency   int
	SchemaVersion string
	Models        []string
	AutoModel     string
	ModelTimeouts map[string]time.Duration
	RetryBase     time.Duration
	MaxAttempts   int
}

type Facebook struct {
	SB, DATR, CUser, XS, FR, PSL, PSN string
	MinRequestGap                     time.Duration
	MaxRetries                        int
	DisableHTTP2                      bool
	DocIDs                            map[string]string
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"), TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		HTTPAddr: env("HTTP_ADDR", ":8080"), DefaultPoll: duration("DEFAULT_POLL_INTERVAL", 5*time.Minute),
		WorkerConcurrency: integer("SYNC_CONCURRENCY", 3),
		DBMaxConns:        integer("DB_MAX_CONNS", 5), DBMinConns: integer("DB_MIN_CONNS", 1), DBBackgroundMaxConns: integer("DB_BACKGROUND_MAX_CONNS", 2), BackfillConcurrency: integer("BACKFILL_CONCURRENCY", 2),
		ExtractionRetryInterval: duration("EXTRACTION_RETRY_INTERVAL", 5*time.Minute), ExtractionRetryBatch: integer("EXTRACTION_RETRY_BATCH", 20), RerankInterval: duration("RERANK_INTERVAL", 15*time.Minute), RerankBatch: integer("RERANK_BATCH", 100),
		CollectionRefreshInterval: duration("COLLECTION_REFRESH_INTERVAL", 10*time.Minute), CollectionRefreshTimeout: duration("COLLECTION_REFRESH_TIMEOUT", 90*time.Second),
		Extraction: LLMExtraction{
			Enabled: boolEnv("LLM_EXTRACTION_ENABLED", false), BaseURL: strings.TrimRight(os.Getenv("LLM_EXTRACTION_BASE_URL"), "/"), APIKey: os.Getenv("LLM_EXTRACTION_API_KEY"),
			Timeout: duration("LLM_EXTRACTION_TIMEOUT", 25*time.Second), Concurrency: integer("LLM_EXTRACTION_CONCURRENCY", 2), SchemaVersion: env("LLM_EXTRACTION_SCHEMA_VERSION", "rental-v1"),
			Models:        csv(env("LLM_EXTRACTION_MODELS", "ministral-3-8b,gpt-oss-20b,gemma-sea-lion-v4-27b,gemini-3.5-flash-lite")),
			AutoModel:     strings.TrimSpace(os.Getenv("LLM_EXTRACTION_AUTO_MODEL")),
			ModelTimeouts: durationMap(os.Getenv("LLM_EXTRACTION_MODEL_TIMEOUTS")), RetryBase: duration("LLM_EXTRACTION_RETRY_BASE", 500*time.Millisecond), MaxAttempts: integer("LLM_EXTRACTION_MAX_ATTEMPTS", 4),
		},
		Facebook: Facebook{SB: envAny("FB_SB", "FACEBOOK_SB"), DATR: envAny("FB_DATR", "FACEBOOK_DATR"), CUser: envAny("FB_CUSER", "FACEBOOK_C_USER"), XS: envAny("FB_XS", "FACEBOOK_XS"), FR: envAny("FB_FR", "FACEBOOK_FR"), PSL: envAny("FB_PSL", "FACEBOOK_PS_L"), PSN: envAny("FB_PSN", "FACEBOOK_PS_N"), MinRequestGap: duration("FB_MIN_REQUEST_GAP", 1200*time.Millisecond), MaxRetries: integer("FB_MAX_RETRIES", 4), DisableHTTP2: boolEnv("FB_DISABLE_HTTP2", false), DocIDs: parseMap(os.Getenv("FB_DOC_IDS"))},
		AdminIDs: map[int64]bool{},
	}
	for _, raw := range strings.Split(os.Getenv("TELEGRAM_ADMIN_IDS"), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
			c.AdminIDs[id] = true
		}
	}
	if v := os.Getenv("SETTINGS_ENCRYPTION_KEY"); v != "" {
		key, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(key) != 32 {
			return c, errors.New("SETTINGS_ENCRYPTION_KEY must be base64 of exactly 32 bytes")
		}
		c.EncryptionKey = key
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	return c, nil
}

func durationMap(s string) map[string]time.Duration {
	m := map[string]time.Duration{}
	for _, pair := range strings.Split(s, ",") {
		p := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(p) == 2 {
			if d, e := time.ParseDuration(strings.TrimSpace(p[1])); e == nil && d > 0 {
				m[strings.TrimSpace(p[0])] = d
			}
		}
	}
	return m
}

func boolEnv(k string, d bool) bool {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return d
	}
	b, err := strconv.ParseBool(v)
	return err == nil && b
}

func csv(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envAny(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}
func integer(k string, d int) int {
	v, e := strconv.Atoi(os.Getenv(k))
	if e == nil && v > 0 {
		return v
	}
	return d
}
func duration(k string, d time.Duration) time.Duration {
	v, e := time.ParseDuration(os.Getenv(k))
	if e == nil && v > 0 {
		return v
	}
	return d
}
func parseMap(s string) map[string]string {
	m := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		p := strings.SplitN(pair, "=", 2)
		if len(p) == 2 {
			m[strings.TrimSpace(p[0])] = strings.TrimSpace(p[1])
		}
	}
	return m
}

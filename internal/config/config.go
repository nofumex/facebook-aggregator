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
	DatabaseURL       string
	TelegramToken     string
	AdminIDs          map[int64]bool
	EncryptionKey     []byte
	HTTPAddr          string
	DefaultPoll       time.Duration
	WorkerConcurrency int
	Facebook          Facebook
	Extraction        LLMExtraction
}

type LLMExtraction struct {
	Enabled       bool
	BaseURL       string
	APIKey        string
	Timeout       time.Duration
	Concurrency   int
	SchemaVersion string
	Models        []string
}

type Facebook struct {
	SB, DATR, CUser, XS, FR, PSL, PSN string
	MinRequestGap                     time.Duration
	MaxRetries                        int
	DocIDs                            map[string]string
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"), TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		HTTPAddr: env("HTTP_ADDR", ":8080"), DefaultPoll: duration("DEFAULT_POLL_INTERVAL", 5*time.Minute),
		WorkerConcurrency: integer("SYNC_CONCURRENCY", 3),
		Extraction: LLMExtraction{
			Enabled: boolEnv("LLM_EXTRACTION_ENABLED", false), BaseURL: strings.TrimRight(os.Getenv("LLM_EXTRACTION_BASE_URL"), "/"), APIKey: os.Getenv("LLM_EXTRACTION_API_KEY"),
			Timeout: duration("LLM_EXTRACTION_TIMEOUT", 25*time.Second), Concurrency: integer("LLM_EXTRACTION_CONCURRENCY", 2), SchemaVersion: env("LLM_EXTRACTION_SCHEMA_VERSION", "rental-v1"),
			Models: csv(env("LLM_EXTRACTION_MODELS", "llama-3.2-1b-instruct,ministral-3b,llama-3.2-3b,ministral-3-8b,gpt-oss-20b,gemma-sea-lion-v4-27b,gemini-3.5-flash-lite")),
		},
		Facebook:          Facebook{SB: envAny("FB_SB", "FACEBOOK_SB"), DATR: envAny("FB_DATR", "FACEBOOK_DATR"), CUser: envAny("FB_CUSER", "FACEBOOK_C_USER"), XS: envAny("FB_XS", "FACEBOOK_XS"), FR: envAny("FB_FR", "FACEBOOK_FR"), PSL: envAny("FB_PSL", "FACEBOOK_PS_L"), PSN: envAny("FB_PSN", "FACEBOOK_PS_N"), MinRequestGap: duration("FB_MIN_REQUEST_GAP", 1200*time.Millisecond), MaxRetries: integer("FB_MAX_RETRIES", 4), DocIDs: parseMap(os.Getenv("FB_DOC_IDS"))},
		AdminIDs:          map[int64]bool{},
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

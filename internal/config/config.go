package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL, TelegramToken, HTTPAddr                                                 string
	AdminIDs                                                                             map[int64]bool
	DefaultPoll                                                                          time.Duration
	WorkerConcurrency, DBMaxConns, DBMinConns, DBBackgroundMaxConns, BackfillConcurrency int
	RerankInterval                                                                       time.Duration
	RerankBatch                                                                          int
	CollectionRefreshInterval, CollectionRefreshTimeout                                  time.Duration
	ProfileLLM                                                                           LLMProfile
}
type LLMProfile struct {
	BaseURL, APIKey, Model string
	Timeout                time.Duration
}

func Load() (Config, error) {
	c := Config{DatabaseURL: os.Getenv("DATABASE_URL"), TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"), HTTPAddr: env("HTTP_ADDR", ":8080"), DefaultPoll: duration("DEFAULT_POLL_INTERVAL", 5*time.Minute), WorkerConcurrency: integer("SYNC_CONCURRENCY", 3), DBMaxConns: integer("DB_MAX_CONNS", 5), DBMinConns: integer("DB_MIN_CONNS", 1), DBBackgroundMaxConns: integer("DB_BACKGROUND_MAX_CONNS", 2), BackfillConcurrency: integer("BACKFILL_CONCURRENCY", 2), RerankInterval: duration("RERANK_INTERVAL", 15*time.Minute), RerankBatch: integer("RERANK_BATCH", 100), CollectionRefreshInterval: duration("COLLECTION_REFRESH_INTERVAL", 10*time.Minute), CollectionRefreshTimeout: duration("COLLECTION_REFRESH_TIMEOUT", 90*time.Second), ProfileLLM: LLMProfile{BaseURL: strings.TrimRight(os.Getenv("BASE_URL"), "/"), APIKey: os.Getenv("FREE_LLM_API_KEY"), Model: env("LLM_PROFILE_MODEL", "gpt-oss-20b"), Timeout: duration("LLM_PROFILE_TIMEOUT", 45*time.Second)}, AdminIDs: map[int64]bool{}}
	for _, raw := range strings.Split(os.Getenv("TELEGRAM_ADMIN_IDS"), ",") {
		if id, e := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); e == nil {
			c.AdminIDs[id] = true
		}
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	return c, nil
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
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

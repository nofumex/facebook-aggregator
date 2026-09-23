package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/egori/facebook-aggregator/internal/ranking"
	"github.com/egori/facebook-aggregator/internal/secrets"
	"github.com/jackc/pgx/v5"
)

func (s *Store) RankingConfig(ctx context.Context) ranking.RankingConfig {
	cfg := ranking.DefaultConfig()
	_ = s.Setting(ctx, "ranking.config", &cfg)
	return cfg
}

func (s *Store) SetSetting(ctx context.Context, key string, value any) error {
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	_, e = s.DB.Exec(ctx, `INSERT INTO application_settings(key,value,is_secret) VALUES($1,$2,false) ON CONFLICT(key) DO UPDATE SET value=$2,encrypted_value=NULL,is_secret=false,updated_at=now()`, key, b)
	return e
}
func (s *Store) SetSecret(ctx context.Context, c *secrets.Cipher, key, value string) error {
	if c == nil {
		return fmt.Errorf("SETTINGS_ENCRYPTION_KEY is required to store secrets")
	}
	b, e := c.Encrypt([]byte(value))
	if e != nil {
		return e
	}
	_, e = s.DB.Exec(ctx, `INSERT INTO application_settings(key,encrypted_value,is_secret) VALUES($1,$2,true) ON CONFLICT(key) DO UPDATE SET value=NULL,encrypted_value=$2,is_secret=true,updated_at=now()`, key, b)
	return e
}
func (s *Store) Setting(ctx context.Context, key string, out any) error {
	var b []byte
	e := s.DB.QueryRow(ctx, "SELECT value FROM application_settings WHERE key=$1 AND NOT is_secret", key).Scan(&b)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, out)
}
func (s *Store) Secret(ctx context.Context, c *secrets.Cipher, key string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("settings encryption unavailable")
	}
	var b []byte
	e := s.DB.QueryRow(ctx, "SELECT encrypted_value FROM application_settings WHERE key=$1 AND is_secret", key).Scan(&b)
	if e != nil {
		return "", e
	}
	plain, e := c.Decrypt(b)
	return string(plain), e
}
func IsNotFound(err error) bool { return err == pgx.ErrNoRows }

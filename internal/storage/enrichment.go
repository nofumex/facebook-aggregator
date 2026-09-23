package storage

import (
	"context"
	"encoding/json"

	"github.com/egori/facebook-aggregator/internal/domain"
	"github.com/egori/facebook-aggregator/internal/enrichment"
	"github.com/jackc/pgx/v5"
)

func (s *Store) LoadEnrichment(ctx context.Context, hash [32]byte, version string) (enrichment.CacheEntry, bool, error) {
	var raw []byte
	var e enrichment.CacheEntry
	err := s.DB.QueryRow(ctx, `SELECT response,coalesce(model,''),coalesce(latency_ms,0),coalesce(input_tokens,0),coalesce(output_tokens,0) FROM llm_enrichments WHERE content_hash=$1 AND schema_version=$2`, hash[:], version).Scan(&raw, &e.Model, &e.LatencyMS, &e.InputTokens, &e.OutputTokens)
	if err == pgx.ErrNoRows {
		return e, false, nil
	}
	if err != nil {
		return e, false, err
	}
	if err = json.Unmarshal(raw, &e.Result); err != nil {
		return e, false, err
	}
	return e, true, nil
}

func (s *Store) SaveEnrichment(ctx context.Context, hash [32]byte, version string, e enrichment.CacheEntry) error {
	raw, err := json.Marshal(e.Result)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO llm_enrichments(content_hash,schema_version,provider,model,response,latency_ms,input_tokens,output_tokens) VALUES($1,$2,'OpenAI-compatible',$3,$4,$5,$6,$7)
		ON CONFLICT(content_hash,schema_version) DO UPDATE SET model=$3,response=$4,latency_ms=$5,input_tokens=$6,output_tokens=$7,updated_at=now()`, hash[:], version, e.Model, raw, e.LatencyMS, e.InputTokens, e.OutputTokens)
	return err
}

var _ = domain.Enrichment{}

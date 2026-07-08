package store

import (
	"context"
	"time"
)

const (
	CatalogCacheRegions       = "regions"
	CatalogCacheInstanceTypes = "instance_types"
	CatalogCacheArchitecture  = "architecture"
	CatalogCacheImages        = "images"
)

type CatalogCacheEntry struct {
	AccountID int64
	Kind      string
	Region    string
	LookupKey string
	Payload   string
	FetchedAt time.Time
}

func (s *Store) UpsertCatalogCache(ctx context.Context, e CatalogCacheEntry) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cloud_catalog_cache
		 (cloud_account_id, kind, region, lookup_key, payload_json, fetched_at)
		 VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(cloud_account_id, kind, region, lookup_key)
		 DO UPDATE SET payload_json = excluded.payload_json, fetched_at = CURRENT_TIMESTAMP`,
		e.AccountID, e.Kind, e.Region, e.LookupKey, e.Payload,
	)
	return err
}

func (s *Store) GetCatalogCache(ctx context.Context, accountID int64, kind, region, lookupKey string) (CatalogCacheEntry, error) {
	var e CatalogCacheEntry
	err := s.db.QueryRowContext(ctx,
		`SELECT cloud_account_id, kind, region, lookup_key, payload_json, fetched_at
		 FROM cloud_catalog_cache
		 WHERE cloud_account_id = ? AND kind = ? AND region = ? AND lookup_key = ?`,
		accountID, kind, region, lookupKey,
	).Scan(&e.AccountID, &e.Kind, &e.Region, &e.LookupKey, &e.Payload, &e.FetchedAt)
	if err != nil {
		return CatalogCacheEntry{}, err
	}
	return e, nil
}

package store

import (
	"context"
	"testing"
)

func TestCatalogCacheUpsertAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, aid := seedProjectAndAccount(t, s)

	entry := CatalogCacheEntry{
		AccountID: aid,
		Kind:      CatalogCacheRegions,
		Payload:   `["ap-east-1","us-west-2"]`,
	}
	if err := s.UpsertCatalogCache(ctx, entry); err != nil {
		t.Fatalf("UpsertCatalogCache: %v", err)
	}

	got, err := s.GetCatalogCache(ctx, aid, CatalogCacheRegions, "", "")
	if err != nil {
		t.Fatalf("GetCatalogCache: %v", err)
	}
	if got.Payload != entry.Payload {
		t.Fatalf("payload = %q, want %q", got.Payload, entry.Payload)
	}
	if got.FetchedAt.IsZero() {
		t.Fatal("FetchedAt should be populated")
	}

	entry.Payload = `["ap-southeast-1"]`
	if err := s.UpsertCatalogCache(ctx, entry); err != nil {
		t.Fatalf("second UpsertCatalogCache: %v", err)
	}
	got, err = s.GetCatalogCache(ctx, aid, CatalogCacheRegions, "", "")
	if err != nil {
		t.Fatalf("second GetCatalogCache: %v", err)
	}
	if got.Payload != entry.Payload {
		t.Fatalf("updated payload = %q, want %q", got.Payload, entry.Payload)
	}
}

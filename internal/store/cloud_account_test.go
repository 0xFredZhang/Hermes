package store

import (
	"context"
	"testing"
)

func sampleAccount() CloudAccount {
	return CloudAccount{
		Name:            "prod-main",
		Provider:        "aws",
		DefaultRegion:   "ap-southeast-1",
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: "topsecret",
		AWSAccountID:    "123456789012",
		ARN:             "arn:aws:iam::123456789012:user/x",
	}
}

func TestCreateAndGetCloudAccount_RoundTripsSecret(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.CreateCloudAccount(ctx, sampleAccount())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetCloudAccount(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SecretAccessKey != "topsecret" {
		t.Fatalf("SecretAccessKey = %q, want decrypted plaintext", got.SecretAccessKey)
	}
	if got.Name != "prod-main" || got.AWSAccountID != "123456789012" {
		t.Fatalf("unexpected account: %+v", got)
	}
}

func TestSecretIsEncryptedAtRest(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id, _ := s.CreateCloudAccount(ctx, sampleAccount())

	var stored string
	err := s.DB().QueryRow(
		`SELECT secret_access_key_enc FROM cloud_accounts WHERE id = ?`, id,
	).Scan(&stored)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if stored == "topsecret" || stored == "" {
		t.Fatalf("secret stored in plaintext or empty: %q", stored)
	}
}

func TestListOmitsSecret(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _ = s.CreateCloudAccount(ctx, sampleAccount())

	list, err := s.ListCloudAccounts(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].SecretAccessKey != "" {
		t.Fatal("List must not expose SecretAccessKey")
	}
	if list[0].Name != "prod-main" {
		t.Fatalf("Name = %q", list[0].Name)
	}
}

func TestDeleteCloudAccount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id, _ := s.CreateCloudAccount(ctx, sampleAccount())

	if err := s.DeleteCloudAccount(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetCloudAccount(ctx, id); err == nil {
		t.Fatal("expected error getting deleted account")
	}
}

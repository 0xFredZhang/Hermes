package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type CloudAccount struct {
	ID              int64
	Name            string
	Provider        string
	DefaultRegion   string
	AccessKeyID     string
	SecretAccessKey string // plaintext, in-memory only
	AWSAccountID    string
	ARN             string
	CreatedAt       time.Time
}

// ErrDuplicateAccount is returned by CreateCloudAccount when a cloud account
// with the same AWS account id already exists (enforced by a unique index).
var ErrDuplicateAccount = errors.New("a cloud account with this AWS account id already exists")

// ErrCloudAccountReferenced is returned when a blueprint or environment still
// owns the cloud account and the database refuses its deletion.
var ErrCloudAccountReferenced = errors.New("cloud account is referenced")

func (s *Store) CreateCloudAccount(ctx context.Context, a CloudAccount) (int64, error) {
	enc, err := s.cipher.Encrypt(a.SecretAccessKey)
	if err != nil {
		return 0, err
	}
	if a.Provider == "" {
		a.Provider = "aws"
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO cloud_accounts
		 (name, provider, default_region, access_key_id, secret_access_key_enc, aws_account_id, arn)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.Name, a.Provider, a.DefaultRegion, a.AccessKeyID, enc, a.AWSAccountID, a.ARN,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, ErrDuplicateAccount
		}
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetCloudAccount(ctx context.Context, id int64) (CloudAccount, error) {
	var a CloudAccount
	var enc string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, provider, default_region, access_key_id,
		        secret_access_key_enc, aws_account_id, arn, created_at
		 FROM cloud_accounts WHERE id = ?`, id,
	).Scan(&a.ID, &a.Name, &a.Provider, &a.DefaultRegion, &a.AccessKeyID,
		&enc, &a.AWSAccountID, &a.ARN, &a.CreatedAt)
	if err != nil {
		return CloudAccount{}, err
	}
	secret, err := s.cipher.Decrypt(enc)
	if err != nil {
		return CloudAccount{}, err
	}
	a.SecretAccessKey = secret
	return a, nil
}

func (s *Store) ListCloudAccounts(ctx context.Context) ([]CloudAccount, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, provider, default_region, access_key_id, aws_account_id, arn, created_at
		 FROM cloud_accounts ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CloudAccount
	for rows.Next() {
		var a CloudAccount
		if err := rows.Scan(&a.ID, &a.Name, &a.Provider, &a.DefaultRegion,
			&a.AccessKeyID, &a.AWSAccountID, &a.ARN, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a) // SecretAccessKey intentionally left empty
	}
	return out, rows.Err()
}

func (s *Store) DeleteCloudAccount(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM cloud_accounts WHERE id = ?`, id)
	if err != nil {
		if isSQLiteForeignKeyConstraint(err) {
			return fmt.Errorf("%w: %v", ErrCloudAccountReferenced, err)
		}
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

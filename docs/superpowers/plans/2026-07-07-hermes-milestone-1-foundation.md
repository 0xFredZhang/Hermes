# Hermes M1:地基 + 云账号管理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建 Hermes 的可运行地基 —— 一个能登录、能添加/验证/加密存储多个 AWS 云账号的 Go Web 应用。

**Architecture:** 分层(config/crypto/store/cloud/auth/api/web),编排与引擎在 M2 加入。本里程碑用标准库 `net/http`(Go 1.22+ 路由)+ `html/template` + htmx;SQLite 用纯 Go 驱动 `modernc.org/sqlite`;AWS 账号凭证用 AES-256-GCM 加密后存 SQLite,添加时用 STS `GetCallerIdentity` 验证有效性。

**Tech Stack:** Go 1.23、`net/http`、`html/template`、htmx、`modernc.org/sqlite`、`aws-sdk-go-v2`、AES-256-GCM。

## Global Constraints

- Go 版本:1.23+(使用标准库 `http.ServeMux` 的 method+path 路由)
- module path:`github.com/0xFredZhang/Hermes`
- SQLite 驱动:`modernc.org/sqlite`(纯 Go,无 CGO;`database/sql` driver 名为 `"sqlite"`)
- AWS SDK:`github.com/aws/aws-sdk-go-v2`(config、credentials、service/sts)
- 前端:标准库 `html/template` + htmx;htmx 静态文件用 `go:embed` 打进二进制(不依赖外网 CDN)
- 主密钥:环境变量 `HERMES_MASTER_KEY`(base64 编码的 32 字节),用于 AES-256-GCM
- 登录口令:环境变量 `HERMES_LOGIN_PASSWORD`
- 凭证加密:AES-256-GCM,存储格式 `base64(nonce || ciphertext)`
- 测试:标准 `testing` + 表驱动;需要真实 AWS 的测试用 `//go:build integration` 标记,默认不跑
- 提交:conventional commits,英文 message

---

### Task 1:项目脚手架与可运行的 HTTP 服务器

**Files:**
- Create: `go.mod`(通过 `go mod init`)
- Create: `cmd/hermes/main.go`
- Create: `internal/api/server.go`
- Test: `internal/api/server_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `api.NewRouter() http.Handler`(返回配置好路由的 handler);`GET /healthz` → 200 `"ok"`

- [ ] **Step 1: 初始化 module 与依赖**

Run:
```bash
go mod init github.com/0xFredZhang/Hermes
go mod edit -go=1.23
```
Expected: 生成 `go.mod`,首行 `module github.com/0xFredZhang/Hermes`

- [ ] **Step 2: Write the failing test**

Create `internal/api/server_test.go`:
```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	NewRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("body = %q, want \"ok\"", got)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestHealthz`
Expected: FAIL(编译错误:`undefined: NewRouter`)

- [ ] **Step 4: Write minimal implementation**

Create `internal/api/server.go`:
```go
package api

import "net/http"

// NewRouter builds the HTTP handler with all routes wired.
func NewRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}
```

Create `cmd/hermes/main.go`:
```go
package main

import (
	"log"
	"net/http"

	"github.com/0xFredZhang/Hermes/internal/api"
)

func main() {
	addr := ":8080"
	log.Printf("hermes listening on %s", addr)
	if err := http.ListenAndServe(addr, api.NewRouter()); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestHealthz -v`
Expected: PASS

- [ ] **Step 6: Verify it builds and runs**

Run:
```bash
go build ./... && go run ./cmd/hermes &
sleep 1 && curl -s localhost:8080/healthz && kill %1
```
Expected: 打印 `ok`

- [ ] **Step 7: Commit**

```bash
git add go.mod cmd/hermes/main.go internal/api/server.go internal/api/server_test.go
git commit -m "feat: scaffold hermes http server with healthz"
```

---

### Task 2:配置与主密钥加载(config 包)

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `type Config struct { Addr string; DBPath string; MasterKey []byte; LoginPassword string }`
  - `func Load() (Config, error)` —— 从环境变量读取;`MasterKey` 是解码并校验为 32 字节的密钥

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:
```go
package config

import (
	"encoding/base64"
	"testing"
)

func validKeyB64() string {
	return base64.StdEncoding.EncodeToString(make([]byte, 32)) // 32 zero bytes
}

func TestLoad(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		t.Setenv("HERMES_MASTER_KEY", validKeyB64())
		t.Setenv("HERMES_LOGIN_PASSWORD", "secret")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cfg.MasterKey) != 32 {
			t.Fatalf("MasterKey len = %d, want 32", len(cfg.MasterKey))
		}
		if cfg.LoginPassword != "secret" {
			t.Fatalf("LoginPassword = %q", cfg.LoginPassword)
		}
		if cfg.Addr != ":8080" {
			t.Fatalf("Addr = %q, want default :8080", cfg.Addr)
		}
	})

	t.Run("wrong key length", func(t *testing.T) {
		t.Setenv("HERMES_MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 16)))
		t.Setenv("HERMES_LOGIN_PASSWORD", "secret")
		if _, err := Load(); err == nil {
			t.Fatal("expected error for 16-byte key, got nil")
		}
	})

	t.Run("missing password", func(t *testing.T) {
		t.Setenv("HERMES_MASTER_KEY", validKeyB64())
		t.Setenv("HERMES_LOGIN_PASSWORD", "")
		if _, err := Load(); err == nil {
			t.Fatal("expected error for missing password, got nil")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoad`
Expected: FAIL(`undefined: Load`)

- [ ] **Step 3: Write minimal implementation**

Create `internal/config/config.go`:
```go
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

type Config struct {
	Addr          string
	DBPath        string
	MasterKey     []byte
	LoginPassword string
}

func Load() (Config, error) {
	cfg := Config{
		Addr:          envOr("HERMES_ADDR", ":8080"),
		DBPath:        envOr("HERMES_DB_PATH", "hermes.db"),
		LoginPassword: os.Getenv("HERMES_LOGIN_PASSWORD"),
	}

	rawKey := os.Getenv("HERMES_MASTER_KEY")
	if rawKey == "" {
		return Config{}, errors.New("HERMES_MASTER_KEY is required")
	}
	key, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil {
		return Config{}, fmt.Errorf("HERMES_MASTER_KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return Config{}, fmt.Errorf("HERMES_MASTER_KEY must decode to 32 bytes, got %d", len(key))
	}
	cfg.MasterKey = key

	if cfg.LoginPassword == "" {
		return Config{}, errors.New("HERMES_LOGIN_PASSWORD is required")
	}
	return cfg, nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestLoad -v`
Expected: PASS(3 个子测试全过)

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: add config loader with master key validation"
```

---

### Task 3:AES-256-GCM 加解密(crypto 包)

**Files:**
- Create: `internal/crypto/cipher.go`
- Test: `internal/crypto/cipher_test.go`

**Interfaces:**
- Consumes: `config.Config.MasterKey`(32 字节)
- Produces:
  - `type Cipher interface { Encrypt(plaintext string) (string, error); Decrypt(ciphertext string) (string, error) }`
  - `func NewCipher(key []byte) (Cipher, error)` —— key 必须 32 字节
  - 加密输出为 `base64(nonce || ciphertext)`

- [ ] **Step 1: Write the failing test**

Create `internal/crypto/cipher_test.go`:
```go
package crypto

import (
	"strings"
	"testing"
)

func newTestCipher(t *testing.T) Cipher {
	t.Helper()
	c, err := NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c := newTestCipher(t)
	plain := "AKIA-secret-value-123"

	enc, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(enc, plain) {
		t.Fatal("ciphertext must not contain the plaintext")
	}

	dec, err := c.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if dec != plain {
		t.Fatalf("Decrypt = %q, want %q", dec, plain)
	}
}

func TestEncryptIsNonDeterministic(t *testing.T) {
	c := newTestCipher(t)
	a, _ := c.Encrypt("same")
	b, _ := c.Encrypt("same")
	if a == b {
		t.Fatal("two encryptions of same plaintext must differ (random nonce)")
	}
}

func TestDecryptTamperedFails(t *testing.T) {
	c := newTestCipher(t)
	enc, _ := c.Encrypt("payload")
	tampered := enc[:len(enc)-2] + "AA" // flip last base64 chars
	if _, err := c.Decrypt(tampered); err == nil {
		t.Fatal("expected error decrypting tampered ciphertext")
	}
}

func TestNewCipherRejectsBadKey(t *testing.T) {
	if _, err := NewCipher(make([]byte, 16)); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/crypto/`
Expected: FAIL(`undefined: NewCipher`, `undefined: Cipher`)

- [ ] **Step 3: Write minimal implementation**

Create `internal/crypto/cipher.go`:
```go
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

type Cipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

type gcmCipher struct {
	aead cipher.AEAD
}

func NewCipher(key []byte) (Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &gcmCipher{aead: aead}, nil
}

func (g *gcmCipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, g.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := g.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (g *gcmCipher) Decrypt(ciphertext string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	ns := g.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	plain, err := g.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/crypto/ -v`
Expected: PASS(4 个测试全过)

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/
git commit -m "feat: add AES-256-GCM cipher for credential encryption"
```

---

### Task 4:SQLite 存储基础与 migration(store 包)

**Files:**
- Create: `internal/store/store.go`
- Create: `internal/store/migrations/0001_cloud_accounts.sql`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `crypto.Cipher`(供后续 task 使用,本 task 先接线保存)
- Produces:
  - `type Store struct { ... }`(持有 `*sql.DB` 和 `crypto.Cipher`)
  - `func Open(dbPath string, c crypto.Cipher) (*Store, error)` —— 打开 DB 并运行 migration
  - `func (s *Store) DB() *sql.DB`;`func (s *Store) Close() error`

- [ ] **Step 1: Add the SQLite driver dependency**

Run: `go get modernc.org/sqlite`
Expected: `go.mod` 新增 `modernc.org/sqlite`

- [ ] **Step 2: Write the migration file**

Create `internal/store/migrations/0001_cloud_accounts.sql`:
```sql
CREATE TABLE cloud_accounts (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    name                  TEXT    NOT NULL,
    provider              TEXT    NOT NULL DEFAULT 'aws',
    default_region        TEXT    NOT NULL,
    access_key_id         TEXT    NOT NULL,
    secret_access_key_enc TEXT    NOT NULL,
    aws_account_id        TEXT    NOT NULL DEFAULT '',
    arn                   TEXT    NOT NULL DEFAULT '',
    created_at            TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

- [ ] **Step 3: Write the failing test**

Create `internal/store/store_test.go`:
```go
package store

import (
	"testing"

	"github.com/0xFredZhang/Hermes/internal/crypto"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	c, err := crypto.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	s, err := Open(":memory:", c)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenRunsMigrations(t *testing.T) {
	s := newTestStore(t)

	var name string
	err := s.DB().QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='cloud_accounts'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("cloud_accounts table not found: %v", err)
	}
	if name != "cloud_accounts" {
		t.Fatalf("got table %q", name)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	c, _ := crypto.NewCipher(make([]byte, 32))
	// A shared file: reopen should not error re-running migrations.
	path := t.TempDir() + "/h.db"
	s1, err := Open(path, c)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_ = s1.Close()
	s2, err := Open(path, c)
	if err != nil {
		t.Fatalf("second Open (idempotent migrate): %v", err)
	}
	_ = s2.Close()
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestOpen`
Expected: FAIL(`undefined: Open`)

- [ ] **Step 5: Write minimal implementation**

Create `internal/store/store.go`:
```go
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"

	"github.com/0xFredZhang/Hermes/internal/crypto"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db     *sql.DB
	cipher crypto.Cipher
}

func Open(dbPath string, c crypto.Cipher) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	// SQLite has a single writer; cap the pool at one connection. This also
	// keeps a ":memory:" database on one connection so migrations and queries
	// share the same schema (relied on by tests).
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db, cipher: c}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(
		`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`,
	); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		var exists string
		err := s.db.QueryRow(
			`SELECT version FROM schema_migrations WHERE version = ?`, name,
		).Scan(&exists)
		if err == nil {
			continue // already applied
		}
		if err != sql.ErrNoRows {
			return err
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := s.db.Exec(
			`INSERT INTO schema_migrations (version) VALUES (?)`, name,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DB() *sql.DB { return s.db }
func (s *Store) Close() error { return s.db.Close() }
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestOpen|TestMigrations' -v`
Expected: PASS(2 个测试全过)

- [ ] **Step 7: Commit**

```bash
git add internal/store/ go.mod go.sum
git commit -m "feat: add sqlite store with embedded migration runner"
```

---

### Task 5:CloudAccount 模型与 CRUD(凭证加密存取)

**Files:**
- Create: `internal/store/cloud_account.go`
- Test: `internal/store/cloud_account_test.go`

**Interfaces:**
- Consumes: `Store`(Task 4)、`crypto.Cipher`(Task 3,经 Store 持有)
- Produces:
  - `type CloudAccount struct { ID int64; Name, Provider, DefaultRegion, AccessKeyID, SecretAccessKey, AWSAccountID, ARN string; CreatedAt time.Time }`(`SecretAccessKey` 为**明文**,仅内存中)
  - `func (s *Store) CreateCloudAccount(ctx context.Context, a CloudAccount) (int64, error)` —— 内部加密 `SecretAccessKey`
  - `func (s *Store) GetCloudAccount(ctx context.Context, id int64) (CloudAccount, error)` —— 内部解密,返回明文 secret
  - `func (s *Store) ListCloudAccounts(ctx context.Context) ([]CloudAccount, error)` —— **不含 secret**(`SecretAccessKey` 留空)
  - `func (s *Store) DeleteCloudAccount(ctx context.Context, id int64) error`

- [ ] **Step 1: Write the failing test**

Create `internal/store/cloud_account_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run CloudAccount`
Expected: FAIL(`undefined: CloudAccount` 等)

- [ ] **Step 3: Write minimal implementation**

Create `internal/store/cloud_account.go`:
```go
package store

import (
	"context"
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
	_, err := s.db.ExecContext(ctx, `DELETE FROM cloud_accounts WHERE id = ?`, id)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run CloudAccount -v`
Expected: PASS(4 个测试全过)

- [ ] **Step 5: Commit**

```bash
git add internal/store/cloud_account.go internal/store/cloud_account_test.go
git commit -m "feat: add cloud account CRUD with encrypted secret at rest"
```

---

### Task 6:AWS 凭证验证(cloud 包,STS GetCallerIdentity)

**Files:**
- Create: `internal/cloud/validator.go`
- Test: `internal/cloud/validator_test.go`

**Interfaces:**
- Consumes: 无(接收原始 access key / secret / region)
- Produces:
  - `type Identity struct { AccountID, ARN string }`
  - `type STSClient interface { GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) }`
  - `type Validator struct { NewClient func(accessKey, secret, region string) STSClient }`
  - `func NewValidator() *Validator`(生产实现:真实 STS 客户端)
  - `func (v *Validator) Validate(ctx context.Context, accessKey, secret, region string) (Identity, error)`

- [ ] **Step 1: Add AWS SDK dependencies**

Run:
```bash
go get github.com/aws/aws-sdk-go-v2/aws
go get github.com/aws/aws-sdk-go-v2/credentials
go get github.com/aws/aws-sdk-go-v2/service/sts
```
Expected: `go.mod` 新增上述模块

- [ ] **Step 2: Write the failing test**

Create `internal/cloud/validator_test.go`(用 fake STS client,不打真实 AWS):
```go
package cloud

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type fakeSTS struct {
	out *sts.GetCallerIdentityOutput
	err error
}

func (f fakeSTS) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return f.out, f.err
}

func TestValidate_Success(t *testing.T) {
	v := &Validator{
		NewClient: func(_, _, _ string) STSClient {
			return fakeSTS{out: &sts.GetCallerIdentityOutput{
				Account: aws.String("123456789012"),
				Arn:     aws.String("arn:aws:iam::123456789012:user/deploy"),
			}}
		},
	}
	id, err := v.Validate(context.Background(), "AKIA", "secret", "ap-southeast-1")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if id.AccountID != "123456789012" {
		t.Fatalf("AccountID = %q", id.AccountID)
	}
	if id.ARN != "arn:aws:iam::123456789012:user/deploy" {
		t.Fatalf("ARN = %q", id.ARN)
	}
}

func TestValidate_BadCredentials(t *testing.T) {
	v := &Validator{
		NewClient: func(_, _, _ string) STSClient {
			return fakeSTS{err: errors.New("InvalidClientTokenId")}
		},
	}
	if _, err := v.Validate(context.Background(), "AKIA", "bad", "ap-southeast-1"); err == nil {
		t.Fatal("expected error for bad credentials")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/cloud/`
Expected: FAIL(`undefined: Validator`, `undefined: STSClient`)

- [ ] **Step 4: Write minimal implementation**

Create `internal/cloud/validator.go`:
```go
package cloud

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type Identity struct {
	AccountID string
	ARN       string
}

type STSClient interface {
	GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

type Validator struct {
	// NewClient builds an STS client for the given static credentials.
	// Overridable in tests.
	NewClient func(accessKey, secret, region string) STSClient
}

func NewValidator() *Validator {
	return &Validator{
		NewClient: func(accessKey, secret, region string) STSClient {
			return sts.New(sts.Options{
				Region:      region,
				Credentials: credentials.NewStaticCredentialsProvider(accessKey, secret, ""),
			})
		},
	}
}

func (v *Validator) Validate(ctx context.Context, accessKey, secret, region string) (Identity, error) {
	client := v.NewClient(accessKey, secret, region)
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return Identity{}, err
	}
	if out.Account == nil || out.Arn == nil {
		return Identity{}, errors.New("sts returned empty identity")
	}
	return Identity{AccountID: aws.ToString(out.Account), ARN: aws.ToString(out.Arn)}, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/cloud/ -v`
Expected: PASS(2 个测试全过)

- [ ] **Step 6: Commit**

```bash
git add internal/cloud/ go.mod go.sum
git commit -m "feat: add AWS credential validator via STS GetCallerIdentity"
```

---

### Task 7:基础登录(auth 包:签名 cookie + middleware)

**Files:**
- Create: `internal/auth/auth.go`
- Test: `internal/auth/auth_test.go`

**Interfaces:**
- Consumes: `config.Config.LoginPassword`、`config.Config.MasterKey`(作 HMAC 密钥)
- Produces:
  - `type Authenticator struct { ... }`
  - `func New(password string, hmacKey []byte) *Authenticator`
  - `func (a *Authenticator) CheckPassword(pw string) bool`
  - `func (a *Authenticator) IssueCookie() *http.Cookie` —— 返回带签名的会话 cookie
  - `func (a *Authenticator) Middleware(next http.Handler) http.Handler` —— 未登录跳 `/login`;`/login`、`/healthz`、`/static/` 放行

- [ ] **Step 1: Write the failing test**

Create `internal/auth/auth_test.go`:
```go
package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newAuth() *Authenticator { return New("hunter2", []byte("hmac-key-material")) }

func TestCheckPassword(t *testing.T) {
	a := newAuth()
	if !a.CheckPassword("hunter2") {
		t.Fatal("correct password rejected")
	}
	if a.CheckPassword("wrong") {
		t.Fatal("wrong password accepted")
	}
}

func TestMiddleware_RedirectsWhenUnauthenticated(t *testing.T) {
	a := newAuth()
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/accounts", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 redirect", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("Location = %q, want /login", loc)
	}
}

func TestMiddleware_AllowsWithValidCookie(t *testing.T) {
	a := newAuth()
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	req.AddCookie(a.IssueCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for authenticated request", rec.Code)
	}
}

func TestMiddleware_AllowsLoginAndStatic(t *testing.T) {
	a := newAuth()
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, path := range []string{"/login", "/healthz", "/static/htmx.min.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("path %s: status = %d, want 200 (should bypass auth)", path, rec.Code)
		}
	}
}

func TestForgedCookieRejected(t *testing.T) {
	a := newAuth()
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "forged.value"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("forged cookie accepted: status %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/`
Expected: FAIL(`undefined: New`, `undefined: sessionCookieName`)

- [ ] **Step 3: Write minimal implementation**

Create `internal/auth/auth.go`:
```go
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

const sessionCookieName = "hermes_session"

// sessionPayload is the constant marker we sign. Since this is a single-user
// tool, a valid signature over this marker means "authenticated".
const sessionPayload = "authenticated"

type Authenticator struct {
	password string
	hmacKey  []byte
}

func New(password string, hmacKey []byte) *Authenticator {
	return &Authenticator{password: password, hmacKey: hmacKey}
}

func (a *Authenticator) CheckPassword(pw string) bool {
	return subtle.ConstantTimeCompare([]byte(pw), []byte(a.password)) == 1
}

func (a *Authenticator) sign(msg string) string {
	mac := hmac.New(sha256.New, a.hmacKey)
	mac.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Authenticator) IssueCookie() *http.Cookie {
	value := sessionPayload + "." + a.sign(sessionPayload)
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func (a *Authenticator) valid(cookieValue string) bool {
	parts := strings.SplitN(cookieValue, ".", 2)
	if len(parts) != 2 {
		return false
	}
	expected := a.sign(parts[0])
	return parts[0] == sessionPayload &&
		hmac.Equal([]byte(expected), []byte(parts[1]))
}

func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" || r.URL.Path == "/healthz" ||
			strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(sessionCookieName)
		if err != nil || !a.valid(c.Value) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/auth/ -v`
Expected: PASS(5 个测试全过)

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "feat: add password login with signed session cookie"
```

---

### Task 8:Web UI 与云账号管理页(接线全部组件)

**Files:**
- Create: `internal/web/templates/layout.html`
- Create: `internal/web/templates/login.html`
- Create: `internal/web/templates/accounts.html`
- Create: `internal/web/templates/_account_rows.html`
- Create: `internal/web/static/htmx.min.js`(下载)
- Create: `internal/web/web.go`(embed + 模板渲染)
- Modify: `internal/api/server.go`(新增账号/登录路由与 handlers)
- Modify: `cmd/hermes/main.go`(装配 config/store/cloud/auth,包 middleware)
- Test: `internal/api/accounts_test.go`

**Interfaces:**
- Consumes: `store.Store`(Task 4/5)、`cloud.Validator`(Task 6)、`auth.Authenticator`(Task 7)、`web` 渲染器
- Produces:
  - `web.Renderer`:`func NewRenderer() (*Renderer, error)`;`Render(w, name string, data any)`、`RenderPartial(...)`
  - 更新 `api.NewRouter` 为 `api.NewRouter(deps Deps) http.Handler`,`Deps` 携带 store/validator/auth/renderer
  - 路由:`GET /login`、`POST /login`、`POST /logout`、`GET /accounts`、`POST /accounts`、`DELETE /accounts/{id}`、`GET /static/`、`GET /`(重定向到 `/accounts`)

- [ ] **Step 1: 下载 htmx 并创建模板**

Run:
```bash
mkdir -p internal/web/static internal/web/templates
curl -sL https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js -o internal/web/static/htmx.min.js
test -s internal/web/static/htmx.min.js && echo "htmx downloaded"
```
Expected: 打印 `htmx downloaded`

Create `internal/web/templates/layout.html`:
```html
{{define "layout"}}<!doctype html>
<html lang="zh">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Hermes</title>
  <script src="/static/htmx.min.js"></script>
  <style>
    body{font-family:system-ui,sans-serif;max-width:880px;margin:2rem auto;padding:0 1rem}
    table{border-collapse:collapse;width:100%}th,td{border:1px solid #ddd;padding:.5rem;text-align:left}
    input,button{padding:.4rem;margin:.2rem 0}.err{color:#b00}
  </style>
</head>
<body>
  <h1>Hermes</h1>
  {{template "content" .}}
</body>
</html>{{end}}
```

Create `internal/web/templates/login.html`:
```html
{{define "content"}}
<h2>登录</h2>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<form method="post" action="/login">
  <input type="password" name="password" placeholder="口令" autofocus>
  <button type="submit">登录</button>
</form>
{{end}}
```

Create `internal/web/templates/accounts.html`:
```html
{{define "content"}}
<form method="post" action="/logout" style="float:right"><button>退出</button></form>
<h2>AWS 云账号</h2>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<form hx-post="/accounts" hx-target="#rows" hx-swap="innerHTML">
  <input name="name" placeholder="别名" required>
  <input name="default_region" placeholder="区域 如 ap-southeast-1" required>
  <input name="access_key_id" placeholder="Access Key ID" required>
  <input name="secret_access_key" placeholder="Secret Access Key" required>
  <button type="submit">添加并验证</button>
</form>
<table>
  <thead><tr><th>别名</th><th>区域</th><th>Account ID</th><th>ARN</th><th></th></tr></thead>
  <tbody id="rows">{{template "rows" .Accounts}}</tbody>
</table>
{{end}}
```

Create `internal/web/templates/_account_rows.html`:
```html
{{define "rows"}}
{{range .}}
<tr>
  <td>{{.Name}}</td><td>{{.DefaultRegion}}</td><td>{{.AWSAccountID}}</td><td>{{.ARN}}</td>
  <td><button hx-delete="/accounts/{{.ID}}" hx-target="#rows" hx-swap="innerHTML"
              hx-confirm="删除该账号?">删除</button></td>
</tr>
{{else}}
<tr><td colspan="5">暂无账号,添加一个吧。</td></tr>
{{end}}
{{end}}
```

- [ ] **Step 2: Write the web renderer**

Create `internal/web/web.go`:
```go
package web

import (
	"embed"
	"html/template"
	"io"
	"net/http"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var StaticFS embed.FS

type Renderer struct {
	pages map[string]*template.Template
}

// NewRenderer parses each page template together with the shared layout and rows partial.
func NewRenderer() (*Renderer, error) {
	shared := []string{"templates/layout.html", "templates/_account_rows.html"}
	pages := map[string]string{
		"login":    "templates/login.html",
		"accounts": "templates/accounts.html",
	}
	r := &Renderer{pages: map[string]*template.Template{}}
	for name, file := range pages {
		files := append([]string{file}, shared...)
		t, err := template.ParseFS(templatesFS, files...)
		if err != nil {
			return nil, err
		}
		r.pages[name] = t
	}
	// standalone partial for htmx swaps
	partial, err := template.ParseFS(templatesFS, "templates/_account_rows.html")
	if err != nil {
		return nil, err
	}
	r.pages["rows"] = partial
	return r, nil
}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := r.pages[name].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// RenderRows writes just the <tbody> content for htmx partial swaps.
func (r *Renderer) RenderRows(w io.Writer, accounts any) error {
	return r.pages["rows"].ExecuteTemplate(w, "rows", accounts)
}
```

- [ ] **Step 3: Write the failing handler test**

Create `internal/api/accounts_test.go`:
```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/auth"
	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/crypto"
	"github.com/0xFredZhang/Hermes/internal/store"
	"github.com/0xFredZhang/Hermes/internal/web"
)

func testDeps(t *testing.T) Deps {
	t.Helper()
	c, _ := crypto.NewCipher(make([]byte, 32))
	s, err := store.Open(":memory:", c)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, err := web.NewRenderer()
	if err != nil {
		t.Fatalf("web.NewRenderer: %v", err)
	}
	// validator that always succeeds with a fixed identity
	v := &cloud.Validator{NewClient: nil}
	v.ValidateFunc = func(_ context.Context, _, _, _ string) (cloud.Identity, error) {
		return cloud.Identity{AccountID: "123456789012", ARN: "arn:aws:iam::123456789012:user/x"}, nil
	}
	return Deps{
		Store:     s,
		Validator: v,
		Auth:      auth.New("pw", []byte("k")),
		Renderer:  r,
	}
}

func authedCreate(t *testing.T, deps Deps, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(deps.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, req)
	return rec
}

func TestCreateAccount_ValidatesAndPersists(t *testing.T) {
	deps := testDeps(t)
	form := url.Values{
		"name":              {"prod"},
		"default_region":    {"ap-southeast-1"},
		"access_key_id":     {"AKIA"},
		"secret_access_key": {"secret"},
	}
	rec := authedCreate(t, deps, form)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "123456789012") {
		t.Fatalf("response should show validated account id; got %s", rec.Body.String())
	}
	list, _ := deps.Store.ListCloudAccounts(context.Background())
	if len(list) != 1 || list[0].Name != "prod" {
		t.Fatalf("account not persisted: %+v", list)
	}
}

func TestCreateAccount_RequiresAuth(t *testing.T) {
	deps := testDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader("name=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated create: status = %d, want 303", rec.Code)
	}
}
```

Note: 该测试要求 `cloud.Validator` 有一个可注入的 `ValidateFunc`。为此在 Task 6 的 `Validator` 上增加一个可选字段并让 `Validate` 优先用它 —— 修改 `internal/cloud/validator.go`:

```go
// add field to Validator struct:
//   ValidateFunc func(ctx context.Context, accessKey, secret, region string) (Identity, error)
//
// at the top of Validate():
//   if v.ValidateFunc != nil {
//       return v.ValidateFunc(ctx, accessKey, secret, region)
//   }
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestCreateAccount`
Expected: FAIL(`undefined: Deps`,`NewRouter` 签名不符,`ValidateFunc` 未定义)

- [ ] **Step 5: Add ValidateFunc hook to the validator**

Modify `internal/cloud/validator.go` —— 给 `Validator` 加字段并在 `Validate` 开头短路:
```go
type Validator struct {
	NewClient    func(accessKey, secret, region string) STSClient
	ValidateFunc func(ctx context.Context, accessKey, secret, region string) (Identity, error)
}

func (v *Validator) Validate(ctx context.Context, accessKey, secret, region string) (Identity, error) {
	if v.ValidateFunc != nil {
		return v.ValidateFunc(ctx, accessKey, secret, region)
	}
	client := v.NewClient(accessKey, secret, region)
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return Identity{}, err
	}
	if out.Account == nil || out.Arn == nil {
		return Identity{}, errors.New("sts returned empty identity")
	}
	return Identity{AccountID: aws.ToString(out.Account), ARN: aws.ToString(out.Arn)}, nil
}
```

- [ ] **Step 6: Rewrite the router with Deps and account handlers**

Replace `internal/api/server.go`:
```go
package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/0xFredZhang/Hermes/internal/auth"
	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/store"
	"github.com/0xFredZhang/Hermes/internal/web"
)

type Deps struct {
	Store     *store.Store
	Validator *cloud.Validator
	Auth      *auth.Authenticator
	Renderer  *web.Renderer
}

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /static/", http.FileServerFS(web.StaticFS))

	mux.HandleFunc("GET /login", func(w http.ResponseWriter, _ *http.Request) {
		d.Renderer.Render(w, "login", map[string]any{})
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		if d.Auth.CheckPassword(r.FormValue("password")) {
			http.SetCookie(w, d.Auth.IssueCookie())
			http.Redirect(w, r, "/accounts", http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		d.Renderer.Render(w, "login", map[string]any{"Error": "口令错误"})
	})
	mux.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "hermes_session", Path: "/", MaxAge: -1})
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/accounts", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /accounts", func(w http.ResponseWriter, r *http.Request) {
		list, err := d.Store.ListCloudAccounts(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.Renderer.Render(w, "accounts", map[string]any{"Accounts": list})
	})
	mux.HandleFunc("POST /accounts", func(w http.ResponseWriter, r *http.Request) {
		handleCreateAccount(w, r, d)
	})
	mux.HandleFunc("DELETE /accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err := d.Store.DeleteCloudAccount(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeRows(w, r.Context(), d)
	})

	return d.Auth.Middleware(mux)
}

func handleCreateAccount(w http.ResponseWriter, r *http.Request, d Deps) {
	acc := store.CloudAccount{
		Name:            r.FormValue("name"),
		DefaultRegion:   r.FormValue("default_region"),
		AccessKeyID:     r.FormValue("access_key_id"),
		SecretAccessKey: r.FormValue("secret_access_key"),
	}
	id, err := d.Validator.Validate(r.Context(), acc.AccessKeyID, acc.SecretAccessKey, acc.DefaultRegion)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<tr><td colspan="5" class="err">凭证验证失败:` + err.Error() + `</td></tr>`))
		return
	}
	acc.AWSAccountID = id.AccountID
	acc.ARN = id.ARN
	if _, err := d.Store.CreateCloudAccount(r.Context(), acc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeRows(w, r.Context(), d)
}

func writeRows(w http.ResponseWriter, ctx context.Context, d Deps) {
	list, err := d.Store.ListCloudAccounts(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := d.Renderer.RenderRows(w, list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

> **Note(必做):**Task 1 写的 `TestHealthz` 调用无参 `NewRouter()`,本步已把签名改为 `NewRouter(Deps)`。编辑 `internal/api/server_test.go`,把 `NewRouter().ServeHTTP(...)` 改为 `NewRouter(testDeps(t)).ServeHTTP(...)`(`testDeps` 定义在同包 `accounts_test.go`),否则 `internal/api` 无法编译。

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./internal/api/ -v`
Expected: PASS(`TestHealthz`、`TestCreateAccount_ValidatesAndPersists`、`TestCreateAccount_RequiresAuth`)

- [ ] **Step 8: Wire everything in main.go**

Replace `cmd/hermes/main.go`:
```go
package main

import (
	"log"
	"net/http"

	"github.com/0xFredZhang/Hermes/internal/api"
	"github.com/0xFredZhang/Hermes/internal/auth"
	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/config"
	"github.com/0xFredZhang/Hermes/internal/crypto"
	"github.com/0xFredZhang/Hermes/internal/store"
	"github.com/0xFredZhang/Hermes/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cipher, err := crypto.NewCipher(cfg.MasterKey)
	if err != nil {
		log.Fatalf("cipher: %v", err)
	}
	st, err := store.Open(cfg.DBPath, cipher)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()
	renderer, err := web.NewRenderer()
	if err != nil {
		log.Fatalf("renderer: %v", err)
	}

	deps := api.Deps{
		Store:     st,
		Validator: cloud.NewValidator(),
		Auth:      auth.New(cfg.LoginPassword, cfg.MasterKey),
		Renderer:  renderer,
	}
	log.Printf("hermes listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, api.NewRouter(deps)); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 9: Full build + manual smoke test**

Run:
```bash
go build ./...
export HERMES_MASTER_KEY=$(head -c 32 /dev/urandom | base64)
export HERMES_LOGIN_PASSWORD=hunter2
go run ./cmd/hermes &
sleep 1
curl -s localhost:8080/healthz            # -> ok
curl -s -i localhost:8080/accounts | head -1   # -> 303 redirect to /login
kill %1
```
Expected: `/healthz` 返回 `ok`;未登录访问 `/accounts` 返回 303 跳 `/login`

- [ ] **Step 10: Commit**

```bash
git add internal/web/ internal/api/ internal/cloud/validator.go cmd/hermes/main.go
git commit -m "feat: add login and cloud account management web UI"
```

---

## 完成标准(M1 Done)

- `go build ./...` 通过,`go test ./...` 全绿
- 设置 `HERMES_MASTER_KEY` + `HERMES_LOGIN_PASSWORD` 后 `go run ./cmd/hermes` 可启动
- 浏览器访问 → 跳登录 → 登录后进入 `/accounts`
- 添加一个真实/测试 AWS 账号:验证有效 → 表格出现该账号(含 Account ID / ARN),secret 在 DB 中为密文
- 删除账号即时刷新列表
- **下一里程碑(M2)** 在此基础上加入 Project/Blueprint/Environment/Job、Provisioner 抽象与 Pulumi 引擎

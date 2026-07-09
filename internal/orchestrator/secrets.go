package orchestrator

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
	"github.com/0xFredZhang/Hermes/internal/store"
)

const (
	rdsPasswordLength    = 24
	rdsPasswordChars     = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!#$%&*()-_=+[]{}<>:?"
	redisAuthTokenLength = 32
	redisAuthTokenChars  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!&#$^<>-"
	redisAuthDefaultUser = "default"
)

func (o *Orchestrator) prepareRuntimeSecrets(ctx context.Context, env store.Environment) (provisioner.RuntimeSecrets, error) {
	env.Snapshot.ApplyDefaults()
	var secrets provisioner.RuntimeSecrets
	if env.Snapshot.RDS.Enabled {
		password, err := o.prepareRDSPassword(ctx, env)
		if err != nil {
			return provisioner.RuntimeSecrets{}, err
		}
		secrets.RDSPassword = password
	}
	if env.Snapshot.Redis.Enabled && env.Snapshot.Redis.AuthEnabled {
		token, err := o.prepareRedisAuthToken(ctx, env)
		if err != nil {
			return provisioner.RuntimeSecrets{}, err
		}
		secrets.RedisAuthToken = token
	}
	return secrets, nil
}

func (o *Orchestrator) prepareRDSPassword(ctx context.Context, env store.Environment) (string, error) {
	secret, err := o.store.GetEnvironmentSecret(ctx, env.ID, store.SecretRDSMySQL)
	if err == nil {
		return secret.Password, nil
	}
	if !errors.Is(err, store.ErrEnvironmentSecretNotFound) {
		return "", err
	}

	password, err := generateRDSPassword()
	if err != nil {
		return "", err
	}
	if err := o.store.UpsertEnvironmentSecret(ctx, store.EnvironmentSecret{
		EnvironmentID: env.ID,
		Kind:          store.SecretRDSMySQL,
		Username:      env.Snapshot.RDS.Username,
		Password:      password,
		Metadata:      rdsSecretMetadata(env.Snapshot.RDS),
	}); err != nil {
		return "", err
	}
	return password, nil
}

func (o *Orchestrator) prepareRedisAuthToken(ctx context.Context, env store.Environment) (string, error) {
	secret, err := o.store.GetEnvironmentSecret(ctx, env.ID, store.SecretRedisAuth)
	if err == nil {
		return secret.Password, nil
	}
	if !errors.Is(err, store.ErrEnvironmentSecretNotFound) {
		return "", err
	}

	token, err := generateRedisAuthToken()
	if err != nil {
		return "", err
	}
	if err := o.store.UpsertEnvironmentSecret(ctx, store.EnvironmentSecret{
		EnvironmentID: env.ID,
		Kind:          store.SecretRedisAuth,
		Username:      redisAuthDefaultUser,
		Password:      token,
		Metadata:      redisSecretMetadata(env.Snapshot.Redis),
	}); err != nil {
		return "", err
	}
	return token, nil
}

func (o *Orchestrator) syncRDSSecretMetadata(ctx context.Context, env store.Environment, outputs map[string]any) error {
	env.Snapshot.ApplyDefaults()
	if !env.Snapshot.RDS.Enabled {
		return nil
	}
	secret, err := o.store.GetEnvironmentSecret(ctx, env.ID, store.SecretRDSMySQL)
	if err != nil {
		return err
	}

	metadata := map[string]any{}
	for k, v := range secret.Metadata {
		metadata[k] = v
	}
	for k, v := range rdsSecretMetadata(env.Snapshot.RDS) {
		metadata[k] = v
	}
	if endpoint := outputString(outputs, "rds_endpoint"); endpoint != "" {
		metadata["endpoint"] = endpoint
	}
	if address := outputString(outputs, "rds_address"); address != "" {
		metadata["host"] = address
	}
	if port := outputs["rds_port"]; port != nil {
		metadata["port"] = port
	}

	return o.store.UpsertEnvironmentSecret(ctx, store.EnvironmentSecret{
		EnvironmentID: env.ID,
		Kind:          store.SecretRDSMySQL,
		Username:      env.Snapshot.RDS.Username,
		Password:      secret.Password,
		Metadata:      metadata,
	})
}

func (o *Orchestrator) syncRedisAuthSecretMetadata(ctx context.Context, env store.Environment, outputs map[string]any) error {
	env.Snapshot.ApplyDefaults()
	if !env.Snapshot.Redis.Enabled || !env.Snapshot.Redis.AuthEnabled {
		return nil
	}
	secret, err := o.store.GetEnvironmentSecret(ctx, env.ID, store.SecretRedisAuth)
	if err != nil {
		return err
	}

	metadata := map[string]any{}
	for k, v := range secret.Metadata {
		metadata[k] = v
	}
	for k, v := range redisSecretMetadata(env.Snapshot.Redis) {
		metadata[k] = v
	}
	if endpoint := outputString(outputs, "redis_primary_endpoint"); endpoint != "" {
		metadata["primary_endpoint"] = endpoint
	}
	if endpoint := outputString(outputs, "redis_reader_endpoint"); endpoint != "" {
		metadata["reader_endpoint"] = endpoint
	}
	if port := outputs["redis_port"]; port != nil {
		metadata["port"] = port
	}

	return o.store.UpsertEnvironmentSecret(ctx, store.EnvironmentSecret{
		EnvironmentID: env.ID,
		Kind:          store.SecretRedisAuth,
		Username:      redisAuthDefaultUser,
		Password:      secret.Password,
		Metadata:      metadata,
	})
}

func generateRDSPassword() (string, error) {
	return generateSecret(rdsPasswordLength, rdsPasswordChars)
}

func generateRedisAuthToken() (string, error) {
	return generateSecret(redisAuthTokenLength, redisAuthTokenChars)
}

func generateSecret(length int, chars string) (string, error) {
	buf := make([]byte, length)
	max := big.NewInt(int64(len(chars)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = chars[n.Int64()]
	}
	return string(buf), nil
}

func rdsSecretMetadata(cfg provisioner.RDS) map[string]any {
	return map[string]any{
		"db_name": cfg.DBName,
		"port":    float64(cfg.Port),
	}
}

func redisSecretMetadata(cfg provisioner.Redis) map[string]any {
	return map[string]any{
		"port": float64(cfg.Port),
	}
}

func outputString(outputs map[string]any, key string) string {
	if outputs == nil || outputs[key] == nil {
		return ""
	}
	return fmt.Sprintf("%v", outputs[key])
}

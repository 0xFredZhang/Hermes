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
	rdsPasswordLength = 24
	rdsPasswordChars  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!#$%&*()-_=+[]{}<>:?"
)

func (o *Orchestrator) prepareRuntimeSecrets(ctx context.Context, env store.Environment) (provisioner.RuntimeSecrets, error) {
	env.Snapshot.ApplyDefaults()
	if !env.Snapshot.RDS.Enabled {
		return provisioner.RuntimeSecrets{}, nil
	}

	secret, err := o.store.GetEnvironmentSecret(ctx, env.ID, store.SecretRDSMySQL)
	if err == nil {
		return provisioner.RuntimeSecrets{RDSPassword: secret.Password}, nil
	}
	if !errors.Is(err, store.ErrEnvironmentSecretNotFound) {
		return provisioner.RuntimeSecrets{}, err
	}

	password, err := generateRDSPassword()
	if err != nil {
		return provisioner.RuntimeSecrets{}, err
	}
	if err := o.store.UpsertEnvironmentSecret(ctx, store.EnvironmentSecret{
		EnvironmentID: env.ID,
		Kind:          store.SecretRDSMySQL,
		Username:      env.Snapshot.RDS.Username,
		Password:      password,
		Metadata:      rdsSecretMetadata(env.Snapshot.RDS),
	}); err != nil {
		return provisioner.RuntimeSecrets{}, err
	}
	return provisioner.RuntimeSecrets{RDSPassword: password}, nil
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

func generateRDSPassword() (string, error) {
	buf := make([]byte, rdsPasswordLength)
	max := big.NewInt(int64(len(rdsPasswordChars)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = rdsPasswordChars[n.Int64()]
	}
	return string(buf), nil
}

func rdsSecretMetadata(cfg provisioner.RDS) map[string]any {
	return map[string]any{
		"db_name": cfg.DBName,
		"port":    float64(cfg.Port),
	}
}

func outputString(outputs map[string]any, key string) string {
	if outputs == nil || outputs[key] == nil {
		return ""
	}
	return fmt.Sprintf("%v", outputs[key])
}

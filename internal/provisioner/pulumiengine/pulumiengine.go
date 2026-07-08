package pulumiengine

import (
	"context"
	"fmt"
	"io"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

// Provisioner drives the Pulumi Automation API against a shared backend.
// Per-job AWS credentials are injected via the workspace environment only.
type Provisioner struct {
	project    string
	backendURL string
	passphrase string
}

func New(project, backendURL, passphrase string) *Provisioner {
	return &Provisioner{project: project, backendURL: backendURL, passphrase: passphrase}
}

// envVars builds the workspace environment for one execution. Credentials are
// scoped to this workspace — never the global process environment.
func (p *Provisioner) envVars(spec provisioner.Spec) map[string]string {
	return map[string]string{
		"AWS_ACCESS_KEY_ID":        spec.Creds.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY":    spec.Creds.SecretAccessKey,
		"AWS_REGION":               spec.Region,
		"PULUMI_CONFIG_PASSPHRASE": p.passphrase,
		"PULUMI_BACKEND_URL":       p.backendURL,
	}
}

func (p *Provisioner) stack(ctx context.Context, spec provisioner.Spec) (auto.Stack, error) {
	return auto.UpsertStackInlineSource(ctx, spec.StackName, p.project,
		buildProgram(spec.Params), auto.EnvVars(p.envVars(spec)))
}

func (p *Provisioner) Preview(ctx context.Context, spec provisioner.Spec, logs io.Writer) (provisioner.PreviewResult, error) {
	st, err := p.stack(ctx, spec)
	if err != nil {
		return provisioner.PreviewResult{}, err
	}
	res, err := st.Preview(ctx, optpreview.ProgressStreams(logs))
	if err != nil {
		return provisioner.PreviewResult{}, err
	}
	cs := res.ChangeSummary
	return provisioner.PreviewResult{
		Creates: cs[apitype.OpCreate],
		Updates: cs[apitype.OpUpdate],
		Deletes: cs[apitype.OpDelete],
		Sames:   cs[apitype.OpSame],
		Summary: fmt.Sprintf("%d to create, %d to update, %d to delete",
			cs[apitype.OpCreate], cs[apitype.OpUpdate], cs[apitype.OpDelete]),
	}, nil
}

func (p *Provisioner) Up(ctx context.Context, spec provisioner.Spec, logs io.Writer) (provisioner.UpResult, error) {
	st, err := p.stack(ctx, spec)
	if err != nil {
		return provisioner.UpResult{}, err
	}
	res, err := st.Up(ctx, optup.ProgressStreams(logs))
	if err != nil {
		return provisioner.UpResult{}, err
	}
	outputs := make(map[string]any, len(res.Outputs))
	for k, v := range res.Outputs {
		outputs[k] = v.Value
	}
	return provisioner.UpResult{Outputs: outputs, Summary: res.Summary.Message}, nil
}

func (p *Provisioner) Destroy(ctx context.Context, spec provisioner.Spec, logs io.Writer) error {
	st, err := p.stack(ctx, spec)
	if err != nil {
		return err
	}
	_, err = st.Destroy(ctx, optdestroy.ProgressStreams(logs))
	return err
}

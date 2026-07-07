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

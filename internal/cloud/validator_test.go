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

package emailses

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// sesClient is the slice of the SESv2 API this adapter uses (for testability).
type sesClient interface {
	SendEmail(ctx context.Context, in *sesv2.SendEmailInput, opts ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

// sesRawSender sends a raw MIME message via SESv2 SendEmail. Auth is the task's
// IAM role — no per-account credential.
type sesRawSender struct{ client sesClient }

func (s *sesRawSender) SendRaw(ctx context.Context, from string, to []string, rawMIME []byte) (string, error) {
	out, err := s.client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(from),
		Destination:      &types.Destination{ToAddresses: to},
		Content:          &types.EmailContent{Raw: &types.RawMessage{Data: rawMIME}},
	})
	if err != nil {
		return "", fmt.Errorf("ses send: %w", err)
	}
	return aws.ToString(out.MessageId), nil
}

// NewSES builds an email adapter backed by SESv2 for the given region, using the
// default (IAM-role) credential chain.
func NewSES(ctx context.Context, region string) (*Sender, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return New(&sesRawSender{client: sesv2.NewFromConfig(cfg)}), nil
}

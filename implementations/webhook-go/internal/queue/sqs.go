package queue

import (
	"context"
	"encoding/json"

	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
)

// sqsAPI is the subset of the AWS SQS client this adapter uses. Defining it
// here (rather than depending on the concrete *sqs.Client) keeps the adapter
// unit-testable with a fake and confines the AWS SDK to this file.
type sqsAPI interface {
	SendMessage(context.Context, *sqs.SendMessageInput, ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
	ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
	ChangeMessageVisibility(context.Context, *sqs.ChangeMessageVisibilityInput, ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error)
}

const (
	sqsMaxMessages = 10 // SQS receive batch max
	sqsWaitSeconds = 20 // long-poll wait
)

// SQS is the AWS SQS-backed Queue. Construct with NewSQS, passing a configured
// *sqs.Client (or any sqsAPI) and the queue URL.
type SQS struct {
	client   sqsAPI
	queueURL string
}

// NewSQS builds an SQS-backed queue.
func NewSQS(client sqsAPI, queueURL string) *SQS {
	return &SQS{client: client, queueURL: queueURL}
}

// Enqueue marshals the canonical message to JSON and sends it to the queue.
func (s *SQS) Enqueue(ctx context.Context, msg canonical.Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	bodyStr := string(body)
	_, err = s.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    &s.queueURL,
		MessageBody: &bodyStr,
	})
	return err
}

// Receive long-polls for a batch of messages. Unparseable ("poison") messages
// are skipped so a single bad payload cannot stall the batch; SQS will redrive
// them to the DLQ once their receive count is exceeded.
func (s *SQS) Receive(ctx context.Context) ([]Delivery, error) {
	out, err := s.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            &s.queueURL,
		MaxNumberOfMessages: sqsMaxMessages,
		WaitTimeSeconds:     sqsWaitSeconds,
	})
	if err != nil {
		return nil, err
	}
	ds := make([]Delivery, 0, len(out.Messages))
	for _, m := range out.Messages {
		if m.Body == nil || m.ReceiptHandle == nil {
			continue
		}
		var msg canonical.Message
		if err := json.Unmarshal([]byte(*m.Body), &msg); err != nil {
			continue // poison message — leave it for SQS redrive/DLQ
		}
		ds = append(ds, &sqsDelivery{s: s, receiptHandle: *m.ReceiptHandle, msg: msg})
	}
	return ds, nil
}

type sqsDelivery struct {
	s             *SQS
	receiptHandle string
	msg           canonical.Message
}

func (d *sqsDelivery) Message() canonical.Message { return d.msg }

// Ack deletes the message from the queue (successful processing).
func (d *sqsDelivery) Ack(ctx context.Context) error {
	_, err := d.s.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &d.s.queueURL,
		ReceiptHandle: &d.receiptHandle,
	})
	return err
}

// Nack returns the message for redelivery by zeroing its visibility timeout.
// SQS's redrive policy moves it to the DLQ after the configured retry count.
func (d *sqsDelivery) Nack(ctx context.Context) error {
	_, err := d.s.client.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl:          &d.s.queueURL,
		ReceiptHandle:     &d.receiptHandle,
		VisibilityTimeout: 0,
	})
	return err
}

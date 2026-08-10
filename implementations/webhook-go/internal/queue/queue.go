// Package queue is the durable buffer between inbound acceptance and forwarding
// to comms-channels. The whole app depends only on the Queue interface; the AWS
// SDK is confined to the SQS adapter (sqs.go), so moving to another cloud's
// broker later is a new adapter file plus an infra change — no churn in the
// listeners, parsers, or drain worker.
package queue

import (
	"context"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
)

// Queue is a durable, at-least-once message buffer.
type Queue interface {
	// Enqueue durably stores a canonical message.
	Enqueue(ctx context.Context, msg canonical.Message) error
	// Receive returns a batch of deliveries to process. Each must be Ack'd on
	// success or Nack'd to be redelivered.
	Receive(ctx context.Context) ([]Delivery, error)
}

// Delivery is a single received message plus its acknowledgement handle.
type Delivery interface {
	Message() canonical.Message
	Ack(ctx context.Context) error
	Nack(ctx context.Context) error
}

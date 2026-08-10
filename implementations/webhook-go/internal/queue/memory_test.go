package queue

import (
	"context"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
)

func msg(id string) canonical.Message {
	return canonical.Message{Meta: canonical.Meta{PlatformMessageID: id}}
}

// TestMemory_EnqueueThenReceive confirms an enqueued message is delivered.
func TestMemory_EnqueueThenReceive(t *testing.T) {
	q := NewMemory()
	ctx := context.Background()
	if err := q.Enqueue(ctx, msg("a")); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	ds, err := q.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(ds) != 1 || ds[0].Message().Meta.PlatformMessageID != "a" {
		t.Fatalf("Receive = %+v, want one delivery for 'a'", ds)
	}
}

// TestMemory_AckRemoves confirms an acked message is not redelivered.
func TestMemory_AckRemoves(t *testing.T) {
	q := NewMemory()
	ctx := context.Background()
	_ = q.Enqueue(ctx, msg("a"))
	ds, _ := q.Receive(ctx)
	if err := ds[0].Ack(ctx); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	ds2, _ := q.Receive(ctx)
	if len(ds2) != 0 {
		t.Errorf("after Ack, Receive returned %d, want 0", len(ds2))
	}
}

// TestMemory_NackRedelivers confirms a nacked message returns to the queue.
func TestMemory_NackRedelivers(t *testing.T) {
	q := NewMemory()
	ctx := context.Background()
	_ = q.Enqueue(ctx, msg("a"))
	ds, _ := q.Receive(ctx)
	if err := ds[0].Nack(ctx); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	ds2, _ := q.Receive(ctx)
	if len(ds2) != 1 || ds2[0].Message().Meta.PlatformMessageID != "a" {
		t.Errorf("after Nack, Receive = %+v, want redelivered 'a'", ds2)
	}
}

// TestMemory_SatisfiesQueue is a compile-time guard.
func TestMemory_SatisfiesQueue(t *testing.T) {
	var _ Queue = (*Memory)(nil)
}

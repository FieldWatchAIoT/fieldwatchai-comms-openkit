package drain

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/publisher"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/queue"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func msg(id string) canonical.Message {
	return canonical.Message{Meta: canonical.Meta{PlatformMessageID: id}}
}

// fakePub records published ids and can simulate a failure.
type fakePub struct {
	mu   sync.Mutex
	seen []string
	err  error
}

func (f *fakePub) Publish(_ context.Context, m canonical.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.seen = append(f.seen, m.Meta.PlatformMessageID)
	return nil
}

// TestDrainOnce_AcksOnSuccess: all received messages are published and acked
// (drained from the queue).
func TestDrainOnce_AcksOnSuccess(t *testing.T) {
	ctx := context.Background()
	q := queue.NewMemory()
	_ = q.Enqueue(ctx, msg("a"))
	_ = q.Enqueue(ctx, msg("b"))
	pub := &fakePub{}
	w := New(q, pub, discardLogger(), 4)

	n, err := w.drainOnce(ctx)
	if err != nil || n != 2 {
		t.Fatalf("drainOnce = (%d,%v), want (2,nil)", n, err)
	}
	left, _ := q.Receive(ctx)
	if len(left) != 0 {
		t.Errorf("queue not drained: %d left", len(left))
	}
	sort.Strings(pub.seen)
	if len(pub.seen) != 2 || pub.seen[0] != "a" || pub.seen[1] != "b" {
		t.Errorf("published = %v, want [a b]", pub.seen)
	}
}

// TestDrainOnce_NacksTransientFailure: a transient publish failure returns the
// message to the queue for redelivery (not lost, not acked).
func TestDrainOnce_NacksTransientFailure(t *testing.T) {
	ctx := context.Background()
	q := queue.NewMemory()
	_ = q.Enqueue(ctx, msg("a"))
	w := New(q, &fakePub{err: errors.New("503 transient")}, discardLogger(), 1)

	if _, err := w.drainOnce(ctx); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	left, _ := q.Receive(ctx)
	if len(left) != 1 || left[0].Message().Meta.PlatformMessageID != "a" {
		t.Errorf("transient failure should redeliver; queue = %+v", left)
	}
}

// TestDrainOnce_NacksPermanentFailure: a permanent failure also nacks (so SQS
// redrive sends it to the DLQ) rather than acking and silently losing it.
func TestDrainOnce_NacksPermanentFailure(t *testing.T) {
	ctx := context.Background()
	q := queue.NewMemory()
	_ = q.Enqueue(ctx, msg("a"))
	w := New(q, &fakePub{err: &publisher.PermanentError{Status: 400}}, discardLogger(), 1)

	if _, err := w.drainOnce(ctx); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	left, _ := q.Receive(ctx)
	if len(left) != 1 {
		t.Errorf("permanent failure should nack (not ack-and-lose); queue has %d", len(left))
	}
}

// TestDrainOnce_EmptyQueueNoError: draining an empty queue is a no-op.
func TestDrainOnce_EmptyQueueNoError(t *testing.T) {
	w := New(queue.NewMemory(), &fakePub{}, discardLogger(), 1)
	n, err := w.drainOnce(context.Background())
	if err != nil || n != 0 {
		t.Errorf("drainOnce on empty = (%d,%v), want (0,nil)", n, err)
	}
}

// TestRun_StopsOnContextCancel: Run returns when the context is cancelled.
func TestRun_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := New(queue.NewMemory(), &fakePub{}, discardLogger(), 1)
	done := make(chan struct{})
	go func() { _ = w.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of context cancel")
	}
}

// Package drain is the back half of the accept-and-buffer pipeline: a worker
// that long-polls the queue and forwards each message to comms-channels via the
// publisher. On success the delivery is acked (removed); on any failure it is
// nacked (returned for redelivery, and ultimately the DLQ via the queue's
// redrive policy) so a message is never lost.
package drain

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/publisher"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/queue"
)

const receiveBackoff = time.Second

// Worker drains a queue into a publisher with bounded concurrency.
type Worker struct {
	q           queue.Queue
	pub         publisher.Publisher
	logger      *slog.Logger
	concurrency int
}

// New builds a drain worker. concurrency < 1 is treated as 1.
func New(q queue.Queue, pub publisher.Publisher, logger *slog.Logger, concurrency int) *Worker {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Worker{q: q, pub: pub, logger: logger, concurrency: concurrency}
}

// Run drains until the context is cancelled. A receive error backs off briefly
// (rather than hot-looping) before retrying.
func (w *Worker) Run(ctx context.Context) error {
	w.logger.Info("drain worker started", "event", "drain.start", "concurrency", w.concurrency)
	for {
		if ctx.Err() != nil {
			w.logger.Info("drain worker stopping", "event", "drain.stop")
			return ctx.Err()
		}
		if _, err := w.drainOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			w.logger.Error("receive failed; backing off",
				"event", "drain.receive_failed", "error", err.Error())
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(receiveBackoff):
			}
		}
	}
}

// drainOnce receives one batch and processes it concurrently, returning the
// number of deliveries handled.
func (w *Worker) drainOnce(ctx context.Context) (int, error) {
	ds, err := w.q.Receive(ctx)
	if err != nil {
		return 0, err
	}
	if len(ds) == 0 {
		return 0, nil
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, w.concurrency)
	for _, d := range ds {
		wg.Add(1)
		sem <- struct{}{}
		go func(d queue.Delivery) {
			defer wg.Done()
			defer func() { <-sem }()
			w.handle(ctx, d)
		}(d)
	}
	wg.Wait()
	return len(ds), nil
}

// handle publishes one delivery and acks on success or nacks on failure.
func (w *Worker) handle(ctx context.Context, d queue.Delivery) {
	m := d.Message()
	if err := w.pub.Publish(ctx, m); err != nil {
		w.logger.Warn("publish failed; redelivering",
			"event", "drain.publish_failed",
			"permanent", publisher.IsPermanent(err),
			"platform_message_id", m.Meta.PlatformMessageID,
			"error", err.Error(),
		)
		if nerr := d.Nack(ctx); nerr != nil {
			w.logger.Error("nack failed", "event", "drain.nack_failed", "error", nerr.Error())
		}
		return
	}
	if aerr := d.Ack(ctx); aerr != nil {
		w.logger.Error("ack failed", "event", "drain.ack_failed", "error", aerr.Error())
	}
}

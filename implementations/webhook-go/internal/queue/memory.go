package queue

import (
	"context"
	"sync"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
)

// Memory is an in-process Queue for local development and tests. Receive drains
// the queued messages; Ack discards a delivery and Nack returns it to the
// queue, modelling at-least-once delivery. It is safe for concurrent use.
type Memory struct {
	mu    sync.Mutex
	items []canonical.Message
}

// NewMemory returns an empty in-memory queue.
func NewMemory() *Memory { return &Memory{} }

// Enqueue appends a message to the queue.
func (m *Memory) Enqueue(_ context.Context, msg canonical.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append(m.items, msg)
	return nil
}

// Receive drains and returns all currently queued messages as deliveries.
func (m *Memory) Receive(_ context.Context) ([]Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Delivery, 0, len(m.items))
	for _, msg := range m.items {
		out = append(out, &memDelivery{q: m, msg: msg})
	}
	m.items = nil
	return out, nil
}

type memDelivery struct {
	q   *Memory
	msg canonical.Message
}

func (d *memDelivery) Message() canonical.Message { return d.msg }

// Ack discards the delivery (already removed from the queue at Receive).
func (d *memDelivery) Ack(_ context.Context) error { return nil }

// Nack returns the message to the queue for redelivery.
func (d *memDelivery) Nack(ctx context.Context) error { return d.q.Enqueue(ctx, d.msg) }

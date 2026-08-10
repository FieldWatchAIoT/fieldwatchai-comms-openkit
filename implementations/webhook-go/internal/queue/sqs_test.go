package queue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func sptr(s string) *string { return &s }

// fakeSQS implements the sqsAPI subset the adapter depends on.
type fakeSQS struct {
	sentBodies  []string
	toReceive   []types.Message
	deleted     []string
	visTimeouts map[string]int32
	sendErr     error
	recvErr     error
}

func (f *fakeSQS) SendMessage(_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.sentBodies = append(f.sentBodies, *in.MessageBody)
	return &sqs.SendMessageOutput{}, nil
}

func (f *fakeSQS) ReceiveMessage(_ context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	if f.recvErr != nil {
		return nil, f.recvErr
	}
	return &sqs.ReceiveMessageOutput{Messages: f.toReceive}, nil
}

func (f *fakeSQS) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.deleted = append(f.deleted, *in.ReceiptHandle)
	return &sqs.DeleteMessageOutput{}, nil
}

func (f *fakeSQS) ChangeMessageVisibility(_ context.Context, in *sqs.ChangeMessageVisibilityInput, _ ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error) {
	if f.visTimeouts == nil {
		f.visTimeouts = map[string]int32{}
	}
	f.visTimeouts[*in.ReceiptHandle] = in.VisibilityTimeout
	return &sqs.ChangeMessageVisibilityOutput{}, nil
}

func TestSQS_EnqueueMarshalsMessage(t *testing.T) {
	f := &fakeSQS{}
	q := NewSQS(f, "https://sqs/queue")
	if err := q.Enqueue(context.Background(), msg("a")); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if len(f.sentBodies) != 1 {
		t.Fatalf("sent %d bodies, want 1", len(f.sentBodies))
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(f.sentBodies[0]), &back); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	meta := back["meta"].(map[string]any)
	if meta["platform_message_id"] != "a" {
		t.Errorf("enqueued body platform_message_id = %v, want a", meta["platform_message_id"])
	}
}

func TestSQS_EnqueuePropagatesError(t *testing.T) {
	q := NewSQS(&fakeSQS{sendErr: errors.New("boom")}, "u")
	if err := q.Enqueue(context.Background(), msg("a")); err == nil {
		t.Error("expected enqueue error, got nil")
	}
}

func TestSQS_ReceiveUnmarshalsDeliveries(t *testing.T) {
	body, _ := json.Marshal(msg("b"))
	f := &fakeSQS{toReceive: []types.Message{{Body: sptr(string(body)), ReceiptHandle: sptr("rh1")}}}
	q := NewSQS(f, "u")
	ds, err := q.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(ds) != 1 || ds[0].Message().Meta.PlatformMessageID != "b" {
		t.Fatalf("Receive = %+v, want one delivery for b", ds)
	}
}

func TestSQS_AckDeletesByReceiptHandle(t *testing.T) {
	body, _ := json.Marshal(msg("b"))
	f := &fakeSQS{toReceive: []types.Message{{Body: sptr(string(body)), ReceiptHandle: sptr("rh1")}}}
	q := NewSQS(f, "u")
	ds, _ := q.Receive(context.Background())
	if err := ds[0].Ack(context.Background()); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if len(f.deleted) != 1 || f.deleted[0] != "rh1" {
		t.Errorf("deleted = %v, want [rh1]", f.deleted)
	}
}

func TestSQS_NackZeroesVisibilityForRedelivery(t *testing.T) {
	body, _ := json.Marshal(msg("b"))
	f := &fakeSQS{toReceive: []types.Message{{Body: sptr(string(body)), ReceiptHandle: sptr("rh1")}}}
	q := NewSQS(f, "u")
	ds, _ := q.Receive(context.Background())
	if err := ds[0].Nack(context.Background()); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	if f.visTimeouts["rh1"] != 0 {
		t.Errorf("visibility timeout = %d, want 0 (immediate redelivery)", f.visTimeouts["rh1"])
	}
}

// TestSQS_ReceiveSkipsPoisonMessages confirms an unparseable SQS body does not
// abort the batch — it is skipped (and left for SQS to redrive to the DLQ).
func TestSQS_ReceiveSkipsPoisonMessages(t *testing.T) {
	good, _ := json.Marshal(msg("good"))
	f := &fakeSQS{toReceive: []types.Message{
		{Body: sptr("not json"), ReceiptHandle: sptr("poison")},
		{Body: sptr(string(good)), ReceiptHandle: sptr("ok")},
	}}
	q := NewSQS(f, "u")
	ds, err := q.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(ds) != 1 || ds[0].Message().Meta.PlatformMessageID != "good" {
		t.Errorf("Receive = %+v, want only the good message", ds)
	}
}

func TestSQS_SatisfiesQueue(t *testing.T) {
	var _ Queue = (*SQS)(nil)
}

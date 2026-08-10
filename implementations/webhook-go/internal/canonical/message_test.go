package canonical

import (
	"encoding/json"
	"reflect"
	"testing"
)

func sp(s string) *string { return &s }

// TestMessage_MarshalsFullyPopulated pins the wire contract: exact field order
// and explicit nulls for the fields that are absent. This is the shape
// fieldwatchai-comms-channels consumes, so the bytes must not drift.
func TestMessage_MarshalsFullyPopulated(t *testing.T) {
	msg := Message{
		Sender: Sender{
			Endpoint: "+12425550042",
			Platform: "whatsapp",
			FullName: sp("John Smith"),
		},
		Body: Body{
			Text:        sp("42 STATUS full"),
			Attachments: []Attachment{{Type: "image", URL: "https://x"}},
			Location:    &Location{Lat: 26.5, Lng: -77.0},
		},
		Meta: Meta{
			PlatformMessageID: "false_abc",
			ReceivedAt:        "2026-06-05T19:32:11Z",
			AccountID:         "acc_w7m2f4",
			RawPayload:        json.RawMessage(`{"foo":"bar"}`),
		},
	}

	want := `{"sender":{"endpoint":"+12425550042","platform":"whatsapp","handle":null,"full_name":"John Smith","first_name":null,"last_name":null,"email":null,"avatar_url":null},"body":{"text":"42 STATUS full","attachments":[{"type":"image","url":"https://x","mime":null}],"location":{"lat":26.5,"lng":-77}},"meta":{"platform_message_id":"false_abc","received_at":"2026-06-05T19:32:11Z","in_reply_to_id":null,"account_id":"acc_w7m2f4","raw_payload":{"foo":"bar"}}}`

	got, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != want {
		t.Errorf("marshal mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestMessage_AbsentFieldsAreNullNotOmitted is the core contract rule: a field
// the platform didn't provide serializes as null, never as an empty string and
// never omitted. Optional strings nil, attachments nil, location nil, and an
// unset raw_payload must all render as null.
func TestMessage_AbsentFieldsAreNullNotOmitted(t *testing.T) {
	msg := Message{
		Sender: Sender{Endpoint: "+1", Platform: "sms"},
		Body:   Body{},
		Meta: Meta{
			PlatformMessageID: "id1",
			ReceivedAt:        "2026-06-05T19:32:11Z",
			AccountID:         "acc",
		},
	}

	want := `{"sender":{"endpoint":"+1","platform":"sms","handle":null,"full_name":null,"first_name":null,"last_name":null,"email":null,"avatar_url":null},"body":{"text":null,"attachments":null,"location":null},"meta":{"platform_message_id":"id1","received_at":"2026-06-05T19:32:11Z","in_reply_to_id":null,"account_id":"acc","raw_payload":null}}`

	got, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != want {
		t.Errorf("marshal mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestMessage_RawPayloadPreservedVerbatim confirms the original payload bytes
// survive a round trip unchanged — this is the debugging escape hatch for
// parser bugs, so it must be byte-exact.
func TestMessage_RawPayloadPreservedVerbatim(t *testing.T) {
	raw := `{"nested":{"a":1,"b":[true,null,"x"]},"emoji":"☂"}`
	msg := Message{
		Sender: Sender{Endpoint: "+1", Platform: "whatsapp"},
		Meta:   Meta{PlatformMessageID: "id", ReceivedAt: "t", AccountID: "a", RawPayload: json.RawMessage(raw)},
	}

	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Message
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(back.Meta.RawPayload) != raw {
		t.Errorf("raw_payload not verbatim\n got: %s\nwant: %s", back.Meta.RawPayload, raw)
	}
}

// TestMessage_RoundTrip confirms marshal then unmarshal reproduces the value.
func TestMessage_RoundTrip(t *testing.T) {
	msg := Message{
		Sender: Sender{Endpoint: "+1", Platform: "telegram", Handle: sp("@bob"), FullName: sp("Bob")},
		Body:   Body{Text: sp("hi"), Location: &Location{Lat: 1.5, Lng: 2.5}},
		Meta:   Meta{PlatformMessageID: "m1", ReceivedAt: "2026-06-05T00:00:00Z", AccountID: "acc", RawPayload: json.RawMessage(`{}`)},
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Message
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(msg, back) {
		t.Errorf("round trip mismatch\n got: %+v\nwant: %+v", back, msg)
	}
}

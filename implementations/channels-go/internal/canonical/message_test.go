package canonical

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func sp(s string) *string { return &s }

// fullMessage exercises every field, including a document attachment with a
// filename and an image attachment with a null mime.
func fullMessage() Message {
	return Message{
		Sender: Sender{
			Endpoint:  "+12425550042",
			Platform:  "whatsapp",
			Handle:    sp("marsh_harbour"),
			FullName:  sp("Marsh Harbour Shelter"),
			FirstName: sp("Marsh"),
			LastName:  sp("Harbour"),
			Email:     sp("hmb@example.org"),
			AvatarURL: sp("https://x/a.jpg"),
		},
		Body: Body{
			Text: sp("42 STATUS full"),
			Attachments: []Attachment{
				{Type: "document", URL: "https://x/report.pdf", Mime: sp("application/pdf"), Filename: sp("report.pdf")},
				{Type: "image", URL: "https://x/i.jpg", Mime: nil, Filename: nil},
			},
			Location: &Location{Lat: 26.5412, Lng: -77.0636},
		},
		Meta: Meta{
			PlatformMessageID: "false_abc123",
			ReceivedAt:        "2026-06-06T04:00:00Z",
			InReplyToID:       sp("false_prev"),
			AccountID:         "acc_w7m2f4",
			RawPayload:        json.RawMessage(`{"foo":"bar"}`),
		},
	}
}

func TestMessageRoundTrips(t *testing.T) {
	want := fullMessage()

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Message
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("round-trip mismatch\n want: %+v\n got:  %+v", want, got)
	}
}

func TestAttachmentFilenameRoundTrips(t *testing.T) {
	raw, _ := json.Marshal(fullMessage())
	var got Message
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc := got.Body.Attachments[0]
	if doc.Filename == nil || *doc.Filename != "report.pdf" {
		t.Fatalf("Filename = %v, want report.pdf", doc.Filename)
	}
}

// UltraMSG frequently omits mime; a null mime must decode to nil without error.
func TestNullMimeTolerated(t *testing.T) {
	in := `{"type":"document","url":"https://x/a.pdf","mime":null,"filename":"a.pdf"}`
	var a Attachment
	if err := json.Unmarshal([]byte(in), &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if a.Mime != nil {
		t.Errorf("Mime = %v, want nil", *a.Mime)
	}
	if a.Filename == nil || *a.Filename != "a.pdf" {
		t.Errorf("Filename = %v, want a.pdf", a.Filename)
	}
}

// The contract: every field is emitted (no omitempty); absent values are JSON
// null, never dropped.
func TestNoOmitemptyOnAttachment(t *testing.T) {
	raw, err := json.Marshal(Attachment{Type: "image", URL: "https://x/i.jpg"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	for _, want := range []string{`"mime":null`, `"filename":null`} {
		if !strings.Contains(s, want) {
			t.Errorf("marshaled attachment missing %s; got %s", want, s)
		}
	}
}

package observability

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestEmitter_CountWritesEMF confirms Count emits a CloudWatch Embedded Metric
// Format line: a metric definition under _aws plus the metric value and its
// dimension as top-level fields, so CloudWatch Logs extracts it as a metric.
func TestEmitter_CountWritesEMF(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf, "FieldWatch/CommsWebhook")
	e.Count("WebhookRequests", 1, map[string]string{"Status": "200"})

	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("emitted line is not JSON: %v\n%s", err, buf.String())
	}

	if doc["WebhookRequests"].(float64) != 1 {
		t.Errorf("metric value = %v, want 1", doc["WebhookRequests"])
	}
	if doc["Status"] != "200" {
		t.Errorf("dimension Status = %v, want 200", doc["Status"])
	}

	aws, ok := doc["_aws"].(map[string]any)
	if !ok {
		t.Fatal("_aws block missing")
	}
	if _, ok := aws["Timestamp"].(float64); !ok {
		t.Error("_aws.Timestamp missing or not a number")
	}
	cwm := aws["CloudWatchMetrics"].([]any)
	if len(cwm) != 1 {
		t.Fatalf("CloudWatchMetrics len = %d, want 1", len(cwm))
	}
	def := cwm[0].(map[string]any)
	if def["Namespace"] != "FieldWatch/CommsWebhook" {
		t.Errorf("namespace = %v", def["Namespace"])
	}
	metrics := def["Metrics"].([]any)
	if len(metrics) != 1 || metrics[0].(map[string]any)["Name"] != "WebhookRequests" {
		t.Errorf("metrics def = %v, want WebhookRequests", metrics)
	}
	dims := def["Dimensions"].([]any)
	if len(dims) != 1 {
		t.Fatalf("dimension sets = %d, want 1", len(dims))
	}
	dimSet := dims[0].([]any)
	if len(dimSet) != 1 || dimSet[0] != "Status" {
		t.Errorf("dimension keys = %v, want [Status]", dimSet)
	}
}

// TestEmitter_NoDimensions confirms a metric with no dimensions still emits
// valid EMF (empty dimension set).
func TestEmitter_NoDimensions(t *testing.T) {
	var buf bytes.Buffer
	NewEmitter(&buf, "ns").Count("DLQSends", 3, nil)
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if doc["DLQSends"].(float64) != 3 {
		t.Errorf("value = %v, want 3", doc["DLQSends"])
	}
}

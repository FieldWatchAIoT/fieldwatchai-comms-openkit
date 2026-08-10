// Package observability emits CloudWatch Embedded Metric Format (EMF) lines to
// stdout. CloudWatch Logs parses these into metrics with no separate PutMetric
// API calls. Use it for the operational signals the deployment platform alerts on: webhook request
// counts/latency by status, signature-verification failures, dropped messages,
// and DLQ sends.
package observability

import (
	"encoding/json"
	"io"
	"sort"
	"sync"
	"time"
)

// Emitter writes EMF metric lines to an io.Writer (typically os.Stdout).
type Emitter struct {
	mu        sync.Mutex
	w         io.Writer
	namespace string
	now       func() time.Time
}

// NewEmitter builds an EMF emitter for the given CloudWatch namespace.
func NewEmitter(w io.Writer, namespace string) *Emitter {
	return &Emitter{w: w, namespace: namespace, now: time.Now}
}

// Count emits a count metric with optional dimensions.
func (e *Emitter) Count(name string, value float64, dims map[string]string) {
	e.emit(name, value, "Count", dims)
}

// Milliseconds emits a duration metric (unit Milliseconds).
func (e *Emitter) Milliseconds(name string, value float64, dims map[string]string) {
	e.emit(name, value, "Milliseconds", dims)
}

func (e *Emitter) emit(name string, value float64, unit string, dims map[string]string) {
	keys := make([]string, 0, len(dims))
	for k := range dims {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic output

	doc := map[string]any{
		"_aws": map[string]any{
			"Timestamp": e.now().UnixMilli(),
			"CloudWatchMetrics": []map[string]any{
				{
					"Namespace":  e.namespace,
					"Dimensions": [][]string{keys},
					"Metrics":    []map[string]string{{"Name": name, "Unit": unit}},
				},
			},
		},
		name: value,
	}
	for k, v := range dims {
		doc[k] = v
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	_ = json.NewEncoder(e.w).Encode(doc)
}

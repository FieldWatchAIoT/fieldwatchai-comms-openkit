// Package replay re-delivers consumer forwards that were never acknowledged.
//
// Forward delivery is best-effort and in-process: a few attempts over a couple
// of seconds, then the message is left with workflow_fired = false and nothing
// re-reads it. That is fine for a consumer blip and useless for a consumer
// outage — and a consumer that buffers in memory loses whatever it held the
// moment it restarts, which is every deploy.
//
// This package closes that gap from the hub side. The messages table already
// records exactly which forwards were never acknowledged, so replay rebuilds
// the payload from stored state and fires it again. Delivery is at-least-once:
// consumers are expected to be idempotent on message_id, which the forward has
// always carried.
package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/canonical"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/workflow"
	"github.com/google/uuid"
)

// Sentinel errors mapped to HTTP status by the handler.
var (
	// ErrInvalid is returned for a malformed replay request.
	ErrInvalid = errors.New("invalid replay request")
	// ErrAlreadyRunning is returned when a replay is already in progress for
	// the tenant. Two concurrent runs would select the same unfired rows and
	// deliver every one of them twice.
	ErrAlreadyRunning = errors.New("replay already running for this tenant")
)

// Default and maximum batch sizes. Replay is a recovery tool, not a firehose:
// a bounded batch keeps a large backlog from arriving at a consumer that has
// only just come back up.
const (
	defaultLimit = 100
	maxLimit     = 1000
)

// store is the narrow query API this service needs.
type store interface {
	ListUnfiredForwards(ctx context.Context, arg goqueries.ListUnfiredForwardsParams) ([]goqueries.ListUnfiredForwardsRow, error)
	MarkWorkflowFired(ctx context.Context, id uuid.UUID) error
}

// forwarder delivers a Forward; *workflow.Forwarder satisfies it.
type forwarder interface {
	Forward(ctx context.Context, url string, f workflow.Forward) error
}

// Service re-fires unacknowledged forwards.
type Service struct {
	store  store
	fwd    forwarder
	logger *slog.Logger

	// running single-flights replay per tenant. Selecting candidates and
	// marking them fired are separate steps with network calls in between, so
	// two concurrent runs read the same unfired rows and deliver each one
	// twice. A consumer that is idempotent on message_id would absorb that
	// silently — which is exactly why it must be prevented here rather than
	// left to be someone else's invisible problem.
	mu      sync.Mutex
	running map[uuid.UUID]bool
}

// NewService constructs a replay Service.
func NewService(s store, fwd forwarder, logger *slog.Logger) *Service {
	return &Service{store: s, fwd: fwd, logger: logger, running: map[uuid.UUID]bool{}}
}

// acquire claims the replay slot for a tenant, reporting false if one is held.
func (s *Service) acquire(tenant uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[tenant] {
		return false
	}
	s.running[tenant] = true
	return true
}

func (s *Service) release(tenant uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, tenant)
}

// Request selects which unacknowledged forwards to re-fire.
type Request struct {
	TenantID  uuid.UUID
	ChannelID *uuid.UUID // nil = every channel in the tenant
	Since     time.Time  // zero = no lower bound
	Limit     int32
	DryRun    bool // report what would be sent without sending it
}

// Result reports the outcome of a replay run.
type Result struct {
	Candidates int         `json:"candidates"`
	Replayed   int         `json:"replayed"`
	Failed     int         `json:"failed"`
	DryRun     bool        `json:"dry_run"`
	Failures   []Failure   `json:"failures,omitempty"`
	MessageIDs []uuid.UUID `json:"message_ids,omitempty"`
}

// Failure identifies a message that could not be re-delivered.
type Failure struct {
	MessageID uuid.UUID `json:"message_id"`
	Error     string    `json:"error"`
}

// Run re-fires the unacknowledged forwards matching req, oldest first.
//
// Delivery is sequential on purpose: a consumer recovering from an outage
// should receive its backlog in the order it happened, and one request at a
// time is kinder than a burst. A failure does not abort the run — the whole
// point is to make progress on whatever can be delivered — but every failure is
// reported so the caller knows the backlog is not fully drained.
//
// Only one run per tenant may be in flight. A second concurrent request fails
// with ErrAlreadyRunning rather than double-delivering the same backlog — a
// double-clicked operator button is the realistic way that happens. Dry runs
// are exempt: they send nothing, so they can safely inspect a run in progress.
//
// The guard is per-process. channels runs a single task today; if it is ever
// scaled out, this needs to become a claim on the row (or the durable queue
// that replaces it) rather than a mutex.
func (s *Service) Run(ctx context.Context, req Request) (Result, error) {
	if req.TenantID == uuid.Nil {
		return Result{}, fmt.Errorf("%w: tenant_id is required", ErrInvalid)
	}
	if !req.DryRun {
		if !s.acquire(req.TenantID) {
			return Result{}, ErrAlreadyRunning
		}
		defer s.release(req.TenantID)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	rows, err := s.store.ListUnfiredForwards(ctx, goqueries.ListUnfiredForwardsParams{
		TenantID:   req.TenantID,
		ChannelID:  req.ChannelID,
		ReceivedAt: req.Since.UTC(),
		Limit:      limit,
	})
	if err != nil {
		return Result{}, fmt.Errorf("list unfired forwards: %w", err)
	}

	res := Result{Candidates: len(rows), DryRun: req.DryRun}
	for _, row := range rows {
		res.MessageIDs = append(res.MessageIDs, row.ID)
		if req.DryRun {
			continue
		}
		if err := s.replayOne(ctx, row); err != nil {
			res.Failed++
			res.Failures = append(res.Failures, Failure{MessageID: row.ID, Error: err.Error()})
			s.logger.Error("replay forward failed", "event", "replay_failed",
				"message_id", row.ID, "error", err.Error())
			continue
		}
		res.Replayed++
		s.logger.Info("forward replayed", "event", "replayed", "message_id", row.ID)
	}
	return res, nil
}

// replayOne rebuilds and re-delivers a single forward, marking it fired only
// once the consumer has acknowledged it.
func (s *Service) replayOne(ctx context.Context, row goqueries.ListUnfiredForwardsRow) error {
	if err := s.fwd.Forward(ctx, row.WorkflowUrl.String, buildForward(row)); err != nil {
		return err
	}
	if err := s.store.MarkWorkflowFired(ctx, row.ID); err != nil {
		// Delivered but not recorded: the next replay will send it again. That
		// is the correct failure direction for at-least-once, but say so.
		return fmt.Errorf("delivered but not marked fired (will resend): %w", err)
	}
	return nil
}

// storedParse is the subset of messages.parsed the forward needs. It mirrors
// what ingest wrote; fields absent on a passthrough message stay empty.
type storedParse struct {
	Command   string `json:"command"`
	Payload   string `json:"payload"`
	ContactID string `json:"matched_contact_id"`
}

// geoJSONPoint is the shape ST_AsGeoJSON emits for a Point.
type geoJSONPoint struct {
	Coordinates []float64 `json:"coordinates"` // [lng, lat] — GeoJSON is x,y
}

// locationJSON converts the stored geography (as GeoJSON) back into the
// canonical {lat,lng} a live forward sends. Returns nil when the message has no
// location, so the field is omitted rather than sent as a null island at 0,0.
//
// A replay that silently dropped this would be worse than one that failed:
// a consumer showing a "locate on map" action would just not show it, and an
// operator cannot tell "they sent no location" from "the location was lost".
func locationJSON(geo string) json.RawMessage {
	if geo == "" {
		return nil
	}
	var p geoJSONPoint
	if json.Unmarshal([]byte(geo), &p) != nil || len(p.Coordinates) < 2 {
		return nil
	}
	out, err := json.Marshal(canonical.Location{Lng: p.Coordinates[0], Lat: p.Coordinates[1]})
	if err != nil {
		return nil
	}
	return out
}

// buildForward reconstructs the consumer payload from the stored message.
// Shape-identical to the original forward, location included.
func buildForward(row goqueries.ListUnfiredForwardsRow) workflow.Forward {
	f := workflow.Forward{
		MessageID:   row.ID.String(),
		TenantID:    row.TenantID.String(),
		AccountType: row.AccountType,
		Platform:    outbound.Platform(row.AccountType),
		Text:        row.BodyText,
		ReceivedAt:  row.ReceivedAt.Format(time.RFC3339),
	}
	if row.ChannelID != nil {
		f.ChannelID = row.ChannelID.String()
	}
	if row.AccountID != nil {
		f.AccountID = row.AccountID.String()
	}
	if row.SenderEndpoint.Valid {
		f.SenderEndpoint = row.SenderEndpoint.String
	}
	if len(row.BodyAttachments) > 0 {
		f.Attachments = row.BodyAttachments
	}
	if loc := locationJSON(row.BodyLocation); loc != nil {
		f.Location = loc
	}
	var p storedParse
	if len(row.Parsed) > 0 && json.Unmarshal(row.Parsed, &p) == nil {
		f.Command = p.Command
		f.Payload = p.Payload
		// matched_contact_id serialises as JSON null when unresolved.
		if p.ContactID != "" && p.ContactID != uuid.Nil.String() {
			f.ContactID = p.ContactID
		}
	}
	return f
}

// Package audit provides a small, dependency-light recorder used by every
// domain to write an audit_events row. It takes a *generated.Queries
// rather than owning its own connection so callers can write an audit
// event in the same transaction as the state change it describes when
// that matters (e.g. membership role changes).
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"deeparchive/db/generated"
	"deeparchive/internal/dbtypes"
)

type Recorder struct {
	queries *generated.Queries
	logger  *slog.Logger
}

func NewRecorder(queries *generated.Queries, logger *slog.Logger) *Recorder {
	return &Recorder{queries: queries, logger: logger}
}

type Event struct {
	OrganizationID *uuid.UUID
	ActorUserID    *uuid.UUID
	EventType      string
	TargetType     string
	TargetID       *uuid.UUID
	Metadata       map[string]any
	IPAddress      string
}

// Record writes an audit event. Failure to write an audit event is logged
// but deliberately does not fail the calling request — an audit-log
// outage must not become an availability outage for the underlying
// action. This tradeoff is explicit: it means audit coverage is
// best-effort, not transactionally guaranteed, which is noted in
// docs/implementation-status.md.
func (r *Recorder) Record(ctx context.Context, e Event) {
	metadata := e.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}

	var ip *netip.Addr
	if e.IPAddress != "" {
		if addr, err := netip.ParseAddr(e.IPAddress); err == nil {
			ip = &addr
		}
	}

	var targetType pgtype.Text
	if e.TargetType != "" {
		targetType = dbtypes.Text(e.TargetType)
	}

	_, err := r.queries.CreateAuditEvent(ctx, generated.CreateAuditEventParams{
		OrganizationID: dbtypes.UUIDPtr(e.OrganizationID),
		ActorUserID:    dbtypes.UUIDPtr(e.ActorUserID),
		EventType:      e.EventType,
		TargetType:     targetType,
		TargetID:       dbtypes.UUIDPtr(e.TargetID),
		Metadata:       marshalMetadata(metadata),
		IpAddress:      ip,
	})
	if err != nil {
		r.logger.Error("audit_event_write_failed", "event_type", e.EventType, "error", err)
	}
}

// Recent returns the most recent audit events across every organization
// and actor — the admin panel's global activity feed. Unlike Record,
// read failures are returned rather than swallowed: a handler can decide
// how to report them to the caller.
func (r *Recorder) Recent(ctx context.Context, limit, offset int32) ([]generated.AuditEvent, error) {
	return r.queries.ListAuditEventsRecent(ctx, generated.ListAuditEventsRecentParams{
		Limit: limit, Offset: offset,
	})
}

func marshalMetadata(m map[string]any) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		return []byte("{}")
	}
	return b
}

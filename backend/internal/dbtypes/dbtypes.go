// Package dbtypes converts between domain types (uuid.UUID, time.Time,
// plain strings) and the pgtype wrappers sqlc generates for nullable
// Postgres columns. Keeping these conversions in one place avoids each
// domain package reinventing (and potentially getting wrong) the
// null-handling semantics.
package dbtypes

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func UUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func UUIDPtr(u *uuid.UUID) pgtype.UUID {
	if u == nil {
		return pgtype.UUID{}
	}
	return UUID(*u)
}

func FromUUID(p pgtype.UUID) uuid.UUID {
	if !p.Valid {
		return uuid.Nil
	}
	return uuid.UUID(p.Bytes)
}

// FromUUIDPtr returns nil when p is not set, otherwise a pointer to the
// decoded UUID.
func FromUUIDPtr(p pgtype.UUID) *uuid.UUID {
	if !p.Valid {
		return nil
	}
	u := uuid.UUID(p.Bytes)
	return &u
}

func Timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func TimestamptzPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return Timestamptz(*t)
}

func FromTimestamptz(p pgtype.Timestamptz) time.Time {
	if !p.Valid {
		return time.Time{}
	}
	return p.Time
}

// FromTimestamptzPtr returns nil when p is not set.
func FromTimestamptzPtr(p pgtype.Timestamptz) *time.Time {
	if !p.Valid {
		return nil
	}
	t := p.Time
	return &t
}

func Text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func FromText(p pgtype.Text) string {
	if !p.Valid {
		return ""
	}
	return p.String
}

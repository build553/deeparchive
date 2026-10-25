package organizations

import (
	"context"

	"github.com/google/uuid"
)

func contextWithOrgID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, orgIDCtxKey{}, id)
}

// orgIDFromContext panics if called outside the loadOrgID middleware — a
// handler mounted without that middleware is a programming error, not a
// runtime condition, matching identity.UserIDFromContext's convention.
func orgIDFromContext(ctx context.Context) uuid.UUID {
	id, ok := ctx.Value(orgIDCtxKey{}).(uuid.UUID)
	if !ok {
		panic("organizations.orgIDFromContext: no organization id in context; is loadOrgID middleware mounted?")
	}
	return id
}

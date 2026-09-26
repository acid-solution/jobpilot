package mutationlock

import (
	"context"
	"hash/fnv"

	"github.com/google/uuid"
)

// Locker serializes mutations for one account across API requests and workers.
// Release must be called even when the guarded operation fails.
type Locker interface {
	Lock(context.Context, uuid.UUID) (release func(), err error)
}

func Key(userID uuid.UUID) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("jobpilot-user-mutation:"))
	_, _ = h.Write(userID[:])
	return int64(h.Sum64())
}

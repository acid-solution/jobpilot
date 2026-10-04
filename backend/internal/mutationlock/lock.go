package mutationlock

import (
	"context"
	"hash/fnv"
	"sync/atomic"

	"github.com/google/uuid"
)

// Locker serializes mutations for one account across API requests and workers.
// Release must be called even when the guarded operation fails.
type Locker interface {
	Lock(context.Context, uuid.UUID) (release func(), err error)
}

type heldLockKey struct{}
type heldLock struct {
	user   uuid.UUID
	ctx    context.Context
	active atomic.Bool
}

// WithHeldLock marks a lock that the caller has already acquired. Invalidate
// the scope before releasing that lock, so retained contexts cannot bypass a
// later acquisition. This does not acquire a database lock itself.
func WithHeldLock(ctx context.Context, user uuid.UUID) (context.Context, func()) {
	lock := &heldLock{user: user, ctx: ctx}
	lock.active.Store(true)
	return context.WithValue(ctx, heldLockKey{}, lock), func() { lock.active.Store(false) }
}

func HeldBy(ctx context.Context, user uuid.UUID) bool {
	lock, ok := ctx.Value(heldLockKey{}).(*heldLock)
	return ok && lock.user == user && lock.active.Load() && lock.ctx.Err() == nil && ctx.Err() == nil
}

func Key(userID uuid.UUID) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("jobpilot-user-mutation:"))
	_, _ = h.Write(userID[:])
	return int64(h.Sum64())
}

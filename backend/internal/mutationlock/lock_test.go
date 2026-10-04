package mutationlock

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestHeldLockScopeOnlyCoversItsOwnerAndLifetime(t *testing.T) {
	user := uuid.New()
	ctx, invalidate := WithHeldLock(context.Background(), user)
	if !HeldBy(ctx, user) || HeldBy(ctx, uuid.New()) {
		t.Fatal("lock scope must cover only its owner")
	}
	retainedCtx := context.WithoutCancel(ctx)
	invalidate()
	if HeldBy(ctx, user) || HeldBy(retainedCtx, user) {
		t.Fatal("a retained context must not bypass locking after release")
	}
}

func TestCancelledContextDoesNotReportAHeldLock(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	user := uuid.New()
	ctx, invalidate := WithHeldLock(parent, user)
	defer invalidate()
	cancel()
	if HeldBy(ctx, user) || HeldBy(context.WithoutCancel(ctx), user) {
		t.Fatal("cancellation may have rolled back the lock transaction")
	}
}

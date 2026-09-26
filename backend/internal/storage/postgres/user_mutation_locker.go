package postgres

import (
	"context"
	"database/sql"
	"sync"

	"github.com/LeoninCS/jobpilot-next/backend/internal/mutationlock"
	"github.com/google/uuid"
)

// UserMutationLocker uses a separate pool so waiting advisory locks cannot
// exhaust the connections needed by the business operation holding the lock.
type UserMutationLocker struct {
	database *sql.DB
	local    sync.Map
}

func NewUserMutationLocker(ctx context.Context, databaseURL string) (*UserMutationLocker, error) {
	db, err := Open(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	return &UserMutationLocker{database: db}, nil
}

func (l *UserMutationLocker) Close() error { return l.database.Close() }

func (l *UserMutationLocker) Lock(ctx context.Context, userID uuid.UUID) (func(), error) {
	actual, _ := l.local.LoadOrStore(userID, make(chan struct{}, 1))
	gate := actual.(chan struct{})
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	tx, err := l.database.BeginTx(ctx, nil)
	if err != nil {
		<-gate
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, mutationlock.Key(userID)); err != nil {
		_ = tx.Rollback()
		<-gate
		return nil, err
	}
	return func() {
		_ = tx.Rollback()
		<-gate
	}, nil
}

func lockUserMutationTx(ctx context.Context, tx *sql.Tx, userID uuid.UUID) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, mutationlock.Key(userID))
	return err
}

var _ mutationlock.Locker = (*UserMutationLocker)(nil)

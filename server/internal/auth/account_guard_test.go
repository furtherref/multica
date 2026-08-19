package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeStatusQuerier struct {
	status string
	err    error
}

func (f *fakeStatusQuerier) GetUserAccountStatus(_ context.Context, _ pgtype.UUID) (string, error) {
	return f.status, f.err
}

func TestAccountGuardCheck(t *testing.T) {
	const uid = "3f0e1f6a-8f3f-4a5e-9b6a-2f8f0f1a2b3c"
	ctx := context.Background()

	t.Run("nil guard allows", func(t *testing.T) {
		var g *AccountGuard
		if err := g.Check(ctx, uid); err != nil {
			t.Fatalf("nil guard: %v", err)
		}
	})
	t.Run("active allows", func(t *testing.T) {
		g := &AccountGuard{Queries: &fakeStatusQuerier{status: AccountStatusActive}}
		if err := g.Check(ctx, uid); err != nil {
			t.Fatalf("active: %v", err)
		}
	})
	t.Run("suspended rejects", func(t *testing.T) {
		g := &AccountGuard{Queries: &fakeStatusQuerier{status: AccountStatusSuspended}}
		if !errors.Is(g.Check(ctx, uid), ErrAccountSuspended) {
			t.Fatal("want ErrAccountSuspended")
		}
	})
	t.Run("missing user rejects", func(t *testing.T) {
		g := &AccountGuard{Queries: &fakeStatusQuerier{err: pgx.ErrNoRows}}
		if !errors.Is(g.Check(ctx, uid), ErrAccountSuspended) {
			t.Fatal("want ErrAccountSuspended for deleted user")
		}
	})
	t.Run("invalid uuid rejects", func(t *testing.T) {
		g := &AccountGuard{Queries: &fakeStatusQuerier{status: AccountStatusActive}}
		if !errors.Is(g.Check(ctx, "not-a-uuid"), ErrAccountSuspended) {
			t.Fatal("want ErrAccountSuspended for bad uuid")
		}
	})
	t.Run("transient db error propagates", func(t *testing.T) {
		boom := errors.New("boom")
		g := &AccountGuard{Queries: &fakeStatusQuerier{err: boom}}
		if !errors.Is(g.Check(ctx, uid), boom) {
			t.Fatal("want transient error propagated")
		}
	})
}

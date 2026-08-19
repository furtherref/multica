package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

// Account status values persisted in "user".account_status. Aligned with
// upstream multica-ai/multica#1688 so a future upstream merge is a no-op.
const (
	AccountStatusActive    = "active"
	AccountStatusSuspended = "suspended"
)

// AccountSuspendedMessage is the human-readable error; AccountSuspendedCode
// is the stable machine-readable code clients branch on.
const (
	AccountSuspendedMessage = "account suspended"
	AccountSuspendedCode    = "ACCOUNT_SUSPENDED"
)

var ErrAccountSuspended = errors.New(AccountSuspendedMessage)

// UserMayAuthenticate returns nil only for an explicitly active account.
// Every other value — suspended, empty, unknown — is rejected (fail-closed):
// a status this code does not recognize must never widen access.
func UserMayAuthenticate(status string) error {
	if status == AccountStatusActive {
		return nil
	}
	return ErrAccountSuspended
}

// AccountStatusQuerier is the single sqlc method AccountGuard needs;
// *db.Queries satisfies it.
type AccountStatusQuerier interface {
	GetUserAccountStatus(ctx context.Context, id pgtype.UUID) (string, error)
}

// AccountGuard answers "may this user authenticate right now" from the
// account_status column, fronted by a short-TTL Redis cache. Suspension
// invalidates the cache entry, so revocation is immediate — including on
// the PAT-cache-hit path (the regression that stalled upstream #1689).
type AccountGuard struct {
	Queries AccountStatusQuerier
	Cache   *AccountStatusCache
}

// Check returns nil when userID may authenticate, ErrAccountSuspended when
// it must not (suspended, deleted, or malformed id — fail-closed), and any
// other error for transient lookup failures (caller maps those to 503).
func (g *AccountGuard) Check(ctx context.Context, userID string) error {
	if g == nil || g.Queries == nil {
		return nil
	}
	if status, ok := g.Cache.Get(ctx, userID); ok {
		return UserMayAuthenticate(status)
	}
	uid, err := util.ParseUUID(userID)
	if err != nil {
		return ErrAccountSuspended
	}
	status, err := g.Queries.GetUserAccountStatus(ctx, uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAccountSuspended
		}
		return err
	}
	g.Cache.Set(ctx, userID, status)
	return UserMayAuthenticate(status)
}

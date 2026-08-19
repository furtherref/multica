package auth

import "errors"

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

package auth

import (
	"errors"
	"testing"
)

func TestUserMayAuthenticate(t *testing.T) {
	cases := []struct {
		status string
		wantOK bool
	}{
		{"active", true},
		{"suspended", false},
		{"", false},        // fail-closed: empty status never authenticates
		{"pending", false}, // fail-closed: unknown status never authenticates
		{"ACTIVE", false},  // exact match only; DB CHECK guarantees lowercase
	}
	for _, tc := range cases {
		err := UserMayAuthenticate(tc.status)
		if tc.wantOK && err != nil {
			t.Errorf("UserMayAuthenticate(%q) = %v, want nil", tc.status, err)
		}
		if !tc.wantOK && !errors.Is(err, ErrAccountSuspended) {
			t.Errorf("UserMayAuthenticate(%q) = %v, want ErrAccountSuspended", tc.status, err)
		}
	}
}

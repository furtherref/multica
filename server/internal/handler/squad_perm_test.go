package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func uuidFromBytes(b byte) pgtype.UUID {
	var u pgtype.UUID
	u.Valid = true
	for i := range u.Bytes {
		u.Bytes[i] = b
	}
	return u
}

func TestCanManageSquad(t *testing.T) {
	uA := uuidFromBytes(0xAA)
	uB := uuidFromBytes(0xBB)

	cases := []struct {
		name   string
		member db.Member
		squad  db.Squad
		want   bool
	}{
		{
			name:   "owner who is not the creator",
			member: db.Member{UserID: uA, Role: "owner"},
			squad:  db.Squad{CreatorID: uB},
			want:   true,
		},
		{
			name:   "admin who is not the creator",
			member: db.Member{UserID: uA, Role: "admin"},
			squad:  db.Squad{CreatorID: uB},
			want:   true,
		},
		{
			name:   "plain member who is the creator",
			member: db.Member{UserID: uA, Role: "member"},
			squad:  db.Squad{CreatorID: uA},
			want:   true,
		},
		{
			name:   "plain member who is not the creator",
			member: db.Member{UserID: uA, Role: "member"},
			squad:  db.Squad{CreatorID: uB},
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canManageSquad(tc.member, tc.squad); got != tc.want {
				t.Fatalf("canManageSquad: got %v, want %v", got, tc.want)
			}
		})
	}
}

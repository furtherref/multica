package mention

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// nameResolverMock implements NameResolver for testing. Entries are keyed by
// the canonical UUID string (uuidToString output) of the entity.
type nameResolverMock struct {
	workspaceID pgtype.UUID
	agents      map[string]string // uuid string → agent.Name
	squads      map[string]string // uuid string → squad.Name
	users       map[string]string // uuid string → user.Name (global, like the SQL)
	// memberUserIDs is the set of user UUIDs that are members of
	// workspaceID. Member canonicalization must be gated by this — a
	// globally-valid user who is not a workspace member must NOT leak
	// their name into a comment, and must not survive as a routable
	// member mention.
	memberUserIDs map[string]struct{}
}

func (m *nameResolverMock) GetAgentInWorkspace(_ context.Context, arg db.GetAgentInWorkspaceParams) (db.Agent, error) {
	if uuidToString(arg.WorkspaceID) != uuidToString(m.workspaceID) {
		return db.Agent{}, fmt.Errorf("wrong workspace")
	}
	name, ok := m.agents[uuidToString(arg.ID)]
	if !ok {
		return db.Agent{}, fmt.Errorf("agent not found")
	}
	return db.Agent{ID: arg.ID, WorkspaceID: arg.WorkspaceID, Name: name}, nil
}

func (m *nameResolverMock) GetSquadInWorkspace(_ context.Context, arg db.GetSquadInWorkspaceParams) (db.Squad, error) {
	if uuidToString(arg.WorkspaceID) != uuidToString(m.workspaceID) {
		return db.Squad{}, fmt.Errorf("wrong workspace")
	}
	name, ok := m.squads[uuidToString(arg.ID)]
	if !ok {
		return db.Squad{}, fmt.Errorf("squad not found")
	}
	return db.Squad{ID: arg.ID, WorkspaceID: arg.WorkspaceID, Name: name}, nil
}

func (m *nameResolverMock) GetUser(_ context.Context, id pgtype.UUID) (db.User, error) {
	name, ok := m.users[uuidToString(id)]
	if !ok {
		return db.User{}, fmt.Errorf("user not found")
	}
	return db.User{ID: id, Name: name}, nil
}

func (m *nameResolverMock) GetMemberByUserAndWorkspace(_ context.Context, arg db.GetMemberByUserAndWorkspaceParams) (db.Member, error) {
	if uuidToString(arg.WorkspaceID) != uuidToString(m.workspaceID) {
		return db.Member{}, fmt.Errorf("wrong workspace")
	}
	if _, ok := m.memberUserIDs[uuidToString(arg.UserID)]; !ok {
		return db.Member{}, fmt.Errorf("not a member")
	}
	return db.Member{UserID: arg.UserID, WorkspaceID: arg.WorkspaceID}, nil
}

func TestCanonicalizeMentions(t *testing.T) {
	ctx := context.Background()
	ws := makeUUID("ws1")

	agentRealID := makeUUID("agent-real")
	agentRealUUID := uuidToString(agentRealID)
	agentBracketID := makeUUID("agent-brkts")
	agentBracketUUID := uuidToString(agentBracketID)
	agentIssueBracketID := makeUUID("agent-issue")
	agentIssueBracketUUID := uuidToString(agentIssueBracketID)
	agentUnpairedID := makeUUID("agent-unpar")
	agentUnpairedUUID := uuidToString(agentUnpairedID)
	agentMissingID := makeUUID("agent-gone-")
	agentMissingUUID := uuidToString(agentMissingID)

	squadID := makeUUID("squad-real-")
	squadUUID := uuidToString(squadID)

	userID := makeUUID("user-real--")
	userUUID := uuidToString(userID)

	resolver := &nameResolverMock{
		workspaceID: ws,
		agents: map[string]string{
			agentRealUUID:         "RealAgent",
			agentBracketUUID:      "David[TF]",
			agentIssueBracketUUID: "MUL-117[TF]",
			agentUnpairedUUID:     "Alice [QA",
		},
		squads: map[string]string{
			squadUUID: "RealSquad",
		},
		users: map[string]string{
			userUUID: "Alice",
		},
		memberUserIDs: map[string]struct{}{
			userUUID: {},
		},
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "agent mention with matching label is unchanged",
			input: "[@RealAgent](mention://agent/" + agentRealUUID + ")",
			want:  "[@RealAgent](mention://agent/" + agentRealUUID + ")",
		},
		{
			name:  "agent mention with mismatched label is rewritten to real name",
			input: "[@FakeName](mention://agent/" + agentRealUUID + ")",
			want:  "[@RealAgent](mention://agent/" + agentRealUUID + ")",
		},
		{
			name:  "ordinary markdown link before agent mention is preserved",
			input: "See [docs](https://x) and [@FakeName](mention://agent/" + agentRealUUID + ")",
			want:  "See [docs](https://x) and [@RealAgent](mention://agent/" + agentRealUUID + ")",
		},
		{
			// Regression: a bare bracketed phrase like "[this]" before a real
			// mention used to make the scanner merge both into one fake match
			// (label "this] out [@FakeName"), so canonicalize replaced the
			// whole span and "[this] out " was silently deleted from the
			// persisted comment.
			name:  "plain bracketed text before agent mention is preserved",
			input: "check [this] out [@FakeName](mention://agent/" + agentRealUUID + ") please",
			want:  "check [this] out [@RealAgent](mention://agent/" + agentRealUUID + ") please",
		},
		{
			// Producer-side escape contract: every backend producer of mention
			// markdown (squad_briefing.formatMention, this canonicalize on
			// rewrite) routes the name through util.EscapeMentionLabel. For
			// an agent named "Alice [QA" the well-formed input from those
			// producers is `[@Alice \[QA](mention://...)`; canonicalize
			// compares the captured label against the escaped canonical and
			// leaves it untouched.
			name:  "agent name with escaped unpaired bracket round-trips canonical",
			input: "hi [@Alice \\[QA](mention://agent/" + agentUnpairedUUID + ") there",
			want:  "hi [@Alice \\[QA](mention://agent/" + agentUnpairedUUID + ") there",
		},
		{
			// If an LLM bypasses the producer escape contract and writes a
			// raw `[` inside a mention label, the markdown is ambiguous —
			// the strict scanner finds the inner `[QA](mention://...)` as a
			// valid link (markdown semantics: the outer `[@Alice ` is a
			// dangling unclosed bracket = literal text). Canonicalize then
			// rewrites the inner label to the escaped canonical, leaving
			// the outer `[@Alice ` as visible text. Not pretty, but
			// deterministic and no characters are deleted.
			name:  "unescaped unpaired bracket inside label canonicalizes the inner link",
			input: "hi [@Alice [QA](mention://agent/" + agentUnpairedUUID + ") there",
			want:  "hi [@Alice [@Alice \\[QA](mention://agent/" + agentUnpairedUUID + ") there",
		},
		{
			// Regression for the scanner anchor heuristic: a non-@ bracketed
			// prefix before a real mention must not be swallowed by the
			// span. Without the heuristic, canonicalize would replace the
			// `[draft ` prefix along with the mention and emit
			// `note [@RealAgent](...)`, deleting user text.
			name:  "non-@ bracketed prefix before mention is preserved by canonicalize",
			input: "note [draft [@FakeBob](mention://agent/" + agentRealUUID + ") tail",
			want:  "note [draft [@RealAgent](mention://agent/" + agentRealUUID + ") tail",
		},
		{
			// `[](mention://all/all)` is invisible markdown that the old
			// regex rejected (label was `.+?`). After the scanner rewrite
			// it slipped through and routed as @all. Now stripped — the
			// invisible link is left as plain text.
			name:  "empty-label all mention is stripped",
			input: "hi [](mention://all/all) there",
			want:  "hi [](mention://all/all) there",
		},
		{
			// Agent mentions with an unresolvable UUID are LEFT IN PLACE so
			// downstream signals that depend on author intent — most notably
			// commentMentionsOthersButNotAssignee, which suppresses the
			// on_comment trigger when the comment is aimed at someone other
			// than the assignee — continue to see the mention. The mention
			// link is harmless: enqueueMentionedAgentTasks separately gates
			// on GetAgentInWorkspace and skips it, and the readonly renderer
			// falls back to the markdown label when the UUID isn't cached.
			name:  "agent mention with unresolvable uuid is left unchanged",
			input: "hi [@Ghost](mention://agent/" + agentMissingUUID + ") there",
			want:  "hi [@Ghost](mention://agent/" + agentMissingUUID + ") there",
		},
		{
			name:  "squad mention is canonicalized",
			input: "[@WrongSquadName](mention://squad/" + squadUUID + ")",
			want:  "[@RealSquad](mention://squad/" + squadUUID + ")",
		},
		{
			name:  "member mention is canonicalized",
			input: "[@WrongUser](mention://member/" + userUUID + ")",
			want:  "[@Alice](mention://member/" + userUUID + ")",
		},
		{
			name:  "member mention with malformed uuid is stripped",
			input: "[@BadMember](mention://member/aaaaaaaa)",
			want:  "@BadMember",
		},
		{
			name:  "all mention is untouched",
			input: "[@all](mention://all/all)",
			want:  "[@all](mention://all/all)",
		},
		{
			name:  "issue mention is untouched",
			input: "[MUL-1](mention://issue/" + agentRealUUID + ")",
			want:  "[MUL-1](mention://issue/" + agentRealUUID + ")",
		},
		{
			name:  "mention inside inline code is untouched",
			input: "use `[@Wrong](mention://agent/" + agentRealUUID + ")` to delegate",
			want:  "use `[@Wrong](mention://agent/" + agentRealUUID + ")` to delegate",
		},
		{
			name:  "mention inside fenced code is untouched",
			input: "```\n[@Wrong](mention://agent/" + agentRealUUID + ")\n```",
			want:  "```\n[@Wrong](mention://agent/" + agentRealUUID + ")\n```",
		},
		{
			name: "multiple mentions are all canonicalized",
			input: "[@FakeA](mention://agent/" + agentRealUUID + ") cc [@FakeB](mention://member/" + userUUID +
				") and squad [@FakeC](mention://squad/" + squadUUID + ")",
			want: "[@RealAgent](mention://agent/" + agentRealUUID + ") cc [@Alice](mention://member/" + userUUID +
				") and squad [@RealSquad](mention://squad/" + squadUUID + ")",
		},
		{
			name:  "name containing brackets is escaped on rewrite",
			input: "[@WrongLabel](mention://agent/" + agentBracketUUID + ")",
			want:  "[@David\\[TF\\]](mention://agent/" + agentBracketUUID + ")",
		},
		{
			name:  "matching label containing raw brackets is escaped",
			input: "[@David[TF]](mention://agent/" + agentBracketUUID + ")",
			want:  "[@David\\[TF\\]](mention://agent/" + agentBracketUUID + ")",
		},
		{
			name:  "matching label containing issue key and raw brackets is escaped",
			input: "[@MUL-117[TF]](mention://agent/" + agentIssueBracketUUID + ")",
			want:  "[@MUL-117\\[TF\\]](mention://agent/" + agentIssueBracketUUID + ")",
		},
		{
			name:  "empty content is empty",
			input: "",
			want:  "",
		},
		{
			name:  "no mentions is no-op",
			input: "Just plain text without mentions.",
			want:  "Just plain text without mentions.",
		},
		{
			name: "mix of canonicalized and unresolvable mentions",
			input: "[@FakeReal](mention://agent/" + agentRealUUID + ") and [@FakeGone](mention://agent/" +
				agentMissingUUID + ")",
			want: "[@RealAgent](mention://agent/" + agentRealUUID + ") and [@FakeGone](mention://agent/" +
				agentMissingUUID + ")",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanonicalizeMentions(ctx, resolver, ws, tt.input)
			if got != tt.want {
				t.Errorf("CanonicalizeMentions() =\n  %q\nwant:\n  %q", got, tt.want)
			}
		})
	}
}

// TestCanonicalizeMentions_NonMemberUserStripped pins the membership gate
// for member mentions. A `mention://member/<user-id>` whose UUID belongs to
// a globally-valid user who is NOT a member of this workspace must be
// stripped to plain text — never canonicalized — so non-members' display
// names cannot leak into a workspace's comments and the mention cannot
// continue to act as a routing target for downstream gates that compare
// mention IDs to participants.
func TestCanonicalizeMentions_NonMemberUserStripped(t *testing.T) {
	ctx := context.Background()
	ws := makeUUID("ws-A-------")

	memberID := makeUUID("user-in-A--")
	memberUUID := uuidToString(memberID)

	outsiderID := makeUUID("user-other-")
	outsiderUUID := uuidToString(outsiderID)

	resolver := &nameResolverMock{
		workspaceID: ws,
		users: map[string]string{
			memberUUID:   "AliceInside",
			outsiderUUID: "EveOutside", // globally valid, but not a workspace member
		},
		memberUserIDs: map[string]struct{}{
			memberUUID: {}, // only AliceInside is a member
		},
	}

	in := "hi [@Anything](mention://member/" + outsiderUUID + ") and [@Stale](mention://member/" + memberUUID + ")"
	want := "hi @Anything and [@AliceInside](mention://member/" + memberUUID + ")"

	got := CanonicalizeMentions(ctx, resolver, ws, in)
	if got != want {
		t.Errorf("non-member mention must strip while member mention canonicalizes:\n got:  %q\n want: %q", got, want)
	}
	if strings.Contains(got, "EveOutside") {
		t.Errorf("non-member display name leaked into output: %q", got)
	}
}

// TestCanonicalizeMentions_CrossWorkspaceAgentLeftAsIs pins that an agent
// mention whose UUID does not resolve in this workspace is LEFT IN PLACE
// rather than rewritten or stripped:
//   - There is no name-leak risk for agents the way there is for members:
//     the label here was authored by whoever wrote the comment (commonly an
//     LLM), not derived from a cross-workspace agent.name lookup. The
//     workspace-scoped GetAgentInWorkspace already prevents any DB-sourced
//     rewrite for an outsider agent.
//   - Stripping the link would erase author intent and break
//     commentMentionsOthersButNotAssignee — a fake-UUID mention that the
//     author wrote to direct the comment elsewhere would silently stop
//     suppressing the assignee's on_comment trigger.
//   - enqueueMentionedAgentTasks separately gates on GetAgentInWorkspace
//     and refuses to dispatch, so the link does not actually route anywhere.
func TestCanonicalizeMentions_CrossWorkspaceAgentLeftAsIs(t *testing.T) {
	ctx := context.Background()
	wsA := makeUUID("ws-A-------")
	wsB := makeUUID("ws-B-------")

	agentB := makeUUID("agent-in-B-")
	agentBUUID := uuidToString(agentB)

	// Resolver scoped to wsB only. Lookups against wsA fail by design.
	resolver := &nameResolverMock{
		workspaceID: wsB,
		agents:      map[string]string{agentBUUID: "AgentInB"},
	}

	input := "ping [@AgentInB](mention://agent/" + agentBUUID + ")"
	want := "ping [@AgentInB](mention://agent/" + agentBUUID + ")"

	got := CanonicalizeMentions(ctx, resolver, wsA, input)
	if got != want {
		t.Errorf("cross-workspace agent should be left unchanged:\n got:  %q\n want: %q", got, want)
	}
}


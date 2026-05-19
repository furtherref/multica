package mention

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// NameResolver looks up the canonical display name for an entity referenced
// by a mention link. Implemented by *db.Queries in production.
//
// Note on member lookups: GetUser is workspace-unscoped (the user table has
// no workspace_id). For member mentions, callers must gate the user lookup
// behind GetMemberByUserAndWorkspace — otherwise a `mention://member/<id>`
// for a globally-valid user who is not a workspace member would canonicalize
// to that outsider's name, leaking the name and leaving the link in place
// for downstream routing-style checks.
type NameResolver interface {
	GetAgentInWorkspace(ctx context.Context, arg db.GetAgentInWorkspaceParams) (db.Agent, error)
	GetSquadInWorkspace(ctx context.Context, arg db.GetSquadInWorkspaceParams) (db.Squad, error)
	GetMemberByUserAndWorkspace(ctx context.Context, arg db.GetMemberByUserAndWorkspaceParams) (db.Member, error)
	GetUser(ctx context.Context, id pgtype.UUID) (db.User, error)
}

// lookupOutcome carries the decision lookupCanonicalName reached for a
// single mention. The three states distinguish "rewrite to canonical name"
// from "leave the link alone" from "demote to plain text" — collapsing the
// last two into a single boolean failure breaks the trigger-suppression
// invariant in commentMentionsOthersButNotAssignee, which relies on author
// intent surviving canonicalization for agent/squad mentions.
type lookupOutcome int

const (
	// lookupResolved means the UUID resolves and the label should be rewritten.
	lookupResolved lookupOutcome = iota
	// lookupKeepAsIs means the UUID does not resolve, but the mention link
	// should be preserved verbatim. Used for agent and squad mentions whose
	// UUID is unknown / cross-workspace / deleted — the label was authored
	// by whoever wrote the comment (not derived from a DB lookup), so there
	// is no name-leak risk, and preserving the link keeps the author's
	// routing intent visible to downstream gates.
	lookupKeepAsIs
	// lookupStrip means the link must be demoted to plain `@<label>` text.
	// Reserved for member mentions whose UUID belongs to a user who is NOT
	// a member of this workspace — canonicalizing via the global GetUser
	// would leak the outsider's real display name into a workspace they
	// have no membership in.
	lookupStrip
)

// CanonicalizeMentions rewrites every agent/member/squad mention in `content`
// so the visible label matches the actual entity name stored in the database.
// Defends against the failure mode in which an author (typically an LLM)
// writes `[@A](mention://agent/<B-uuid>)` — the UI renders "@A" but the
// routing layer triggers B. After this pass, the label and the UUID's
// resolved entity always agree, eliminating that silent mismatch.
//
// Behaviour:
//   - agent / member / squad mention whose UUID resolves in this workspace:
//     label replaced with the entity's current `name` (with `[` and `]`
//     escaped to keep the link grammar intact).
//   - agent / squad mention whose UUID does NOT resolve (deleted,
//     cross-workspace, fake): mention link left in place. The label is
//     author-controlled so there is no name-leak risk, and downstream gates
//     (enqueueMentionedAgentTasks, commentMentionsOthersButNotAssignee)
//     correctly interpret the unresolved mention without further help.
//   - member mention whose UUID is NOT a workspace member: mention link is
//     stripped down to plain `@<original-label>` text. GetUser is global,
//     so leaving the link would let a follow-up canonicalize-on-edit leak
//     the outsider's display name; demote it to text so the link cannot be
//     re-resolved and the routing-style mention disappears.
//   - `issue` and `all` mentions: left untouched. Issue mention labels are
//     resolved at render time via IssueMentionLink; `all` is a literal.
//   - Mentions inside inline code or fenced code blocks: left untouched
//     (shared with ExpandIssueIdentifiers via findSkipRegions).
func CanonicalizeMentions(ctx context.Context, resolver NameResolver, workspaceID pgtype.UUID, content string) string {
	if content == "" {
		return content
	}
	matches := util.FindMentionMatches(content)
	if len(matches) == 0 {
		return content
	}
	skipRegions := findSkipRegions(content)

	type replacement struct {
		start, end int
		text       string
	}
	var replacements []replacement

	for _, m := range matches {
		fullStart, fullEnd := m.Start, m.End
		if inSkipRegion(fullStart, skipRegions) {
			continue
		}
		label := m.Label
		mentionType := m.Type
		idStr := m.ID

		if mentionType != "agent" && mentionType != "member" && mentionType != "squad" {
			continue
		}

		canonical, outcome := lookupCanonicalName(ctx, resolver, workspaceID, mentionType, idStr)
		switch outcome {
		case lookupResolved:
			escapedCanonical := util.EscapeMentionLabel(canonical)
			if escapedCanonical == label {
				continue
			}
			replacements = append(replacements, replacement{
				start: fullStart,
				end:   fullEnd,
				text:  fmt.Sprintf("[@%s](mention://%s/%s)", escapedCanonical, mentionType, idStr),
			})
		case lookupStrip:
			replacements = append(replacements, replacement{
				start: fullStart,
				end:   fullEnd,
				text:  "@" + label,
			})
		case lookupKeepAsIs:
			// no-op
		}
	}

	if len(replacements) == 0 {
		return content
	}
	result := content
	for i := len(replacements) - 1; i >= 0; i-- {
		r := replacements[i]
		result = result[:r.start] + r.text + result[r.end:]
	}
	return result
}

func lookupCanonicalName(ctx context.Context, r NameResolver, workspaceID pgtype.UUID, mentionType, idStr string) (string, lookupOutcome) {
	id, err := util.ParseUUID(idStr)
	if err != nil {
		if mentionType == "member" {
			return "", lookupStrip
		}
		// Malformed agent/squad UUID — keep the authored link in place. We
		// never reach the DB for this row, so there is no name to leak;
		// leaving the literal markdown lets downstream gates preserve the
		// author's routing intent.
		return "", lookupKeepAsIs
	}
	switch mentionType {
	case "agent":
		ag, err := r.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: id, WorkspaceID: workspaceID})
		if err != nil {
			return "", lookupKeepAsIs
		}
		return ag.Name, lookupResolved
	case "squad":
		sq, err := r.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{ID: id, WorkspaceID: workspaceID})
		if err != nil {
			return "", lookupKeepAsIs
		}
		return sq.Name, lookupResolved
	case "member":
		// Gate on workspace membership first — GetUser is global. Without
		// this gate a `mention://member/<user-id>` for any valid user
		// would canonicalize to that user's name regardless of which
		// workspace the comment lives in. Non-members are stripped (not
		// kept-as-is) so a subsequent canonicalize-on-edit cannot resolve
		// the link via GetUser and leak the name.
		if _, err := r.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID:      id,
			WorkspaceID: workspaceID,
		}); err != nil {
			return "", lookupStrip
		}
		u, err := r.GetUser(ctx, id)
		if err != nil {
			return "", lookupStrip
		}
		return u.Name, lookupResolved
	}
	return "", lookupKeepAsIs
}


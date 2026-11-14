package execenv

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderSkillBullet verifies the trigger signal (description) is appended
// when present and gracefully omitted when absent, so older skills that carry
// no description still render as a bare name rather than a dangling em dash.
func TestRenderSkillBullet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		skill SkillContextForEnv
		want  string
	}{
		{"with description", SkillContextForEnv{Name: "Foo", Description: "Use for foo"}, "- **Foo** — Use for foo\n"},
		{"no description", SkillContextForEnv{Name: "Foo"}, "- **Foo**\n"},
		{"whitespace description", SkillContextForEnv{Name: "Foo", Description: "  "}, "- **Foo**\n"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := renderSkillBullet(tc.skill); got != tc.want {
				t.Errorf("renderSkillBullet() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSkillsSectionEnforcesActivationProtocol locks the forcing function that
// turns auto-discovered skills from a passive resource into a mandatory
// read-and-apply step. Without it, agents skip standards skills entirely —
// e.g. a unit test landing with zero Javadoc despite the backend skill
// requiring it (TIG-510). Provider-agnostic: must hold for native-discovery
// runtimes (Codex/Claude) and the .agent_context fallback (Gemini/Hermes)
// alike, since the runtime can demote a forcing-function skill to "just
// another auto-discovered file".
func TestSkillsSectionEnforcesActivationProtocol(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		IssueID: "11111111-2222-3333-4444-555555555555",
		AgentSkills: []SkillContextForEnv{
			{Name: "Backend Standards", Description: "Use when writing Java backend code", Content: "rules"},
		},
	}
	for _, provider := range []string{"claude", "codex", "gemini"} {
		out := buildMetaSkillContent(provider, ctx)
		for _, want := range []string{
			"**Discovery is not application.**",
			"you MUST complete this protocol",
			"open its `SKILL.md` and read it in full",
			"ALWAYS includes the applicable coding-standards reference",
			// Compliance wording must stay skill-agnostic, not Java-only:
			// frontend uses Principles/checklists, not `Mandatory:` labels, so
			// keying the protocol to "marked Mandatory" alone would leave a
			// labelless skill with nothing to anchor on.
			"Comply with every required rule the skill states",
			"rules marked Mandatory",
			"must/required language",
			"Principles",
			"checklist items",
			// The index is names-only since MUL-5529, and the name is the
			// on-disk slug so the brief, the directory and the SKILL.md
			// frontmatter all agree. Descriptions now reach the model through
			// each runtime CLI's own listing of that frontmatter.
			"- **backend-standards**",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("[provider=%s] Skills section missing %q", provider, want)
			}
		}
	}
}

// TestSkillsSectionOmittedWithoutSkills keeps the protocol from rendering when
// the agent has no skills — no dangling "## Skills" header or protocol text.
func TestSkillsSectionOmittedWithoutSkills(t *testing.T) {
	t.Parallel()
	out := buildMetaSkillContent("codex", TaskContextForEnv{IssueID: "x"})
	if strings.Contains(out, "## Skills") {
		t.Error("Skills section must be omitted when the agent has no skills")
	}
	if strings.Contains(out, "Discovery is not application") {
		t.Error("Skills protocol must be omitted when the agent has no skills")
	}
}

// TestAssignmentWorkflowPointsToSkillProtocol locks the early, prominent
// pointer from the assignment workflow into the Skills protocol. A bare
// "Follow your Skills" buried among nine steps was the wording that got
// ignored; it must be replaced with an explicit "read and comply before
// touching code" pointer, and only when the agent actually has skills.
func TestAssignmentWorkflowPointsToSkillProtocol(t *testing.T) {
	t.Parallel()
	withSkills := buildMetaSkillContent("codex", TaskContextForEnv{
		IssueID:     "11111111-2222-3333-4444-555555555555",
		AgentSkills: []SkillContextForEnv{{Name: "Backend Standards", Description: "Java", Content: "x"}},
	})
	if !strings.Contains(withSkills, "complete the Skills protocol in the `## Skills` section below") {
		t.Error("assignment workflow must point at the Skills protocol when skills exist")
	}
	if strings.Contains(withSkills, "Follow your Skills and Agent Identity") {
		t.Error("the old buried 'Follow your Skills' wording must be gone")
	}
}

// TestSkillIndexReachesTheBriefNotTheContextFile pins where an installed skill
// is named for the model. MUL-5529 made the runtime brief the single listing
// Multica renders and cut the duplicate out of issue_context.md — descriptions
// now come from each SKILL.md's frontmatter, which every runtime CLI surfaces
// itself. What this fork needs to stay true is the pairing: the brief must
// still name the skill AND carry the forcing-function protocol that turns
// "installed" into "read and applied", while issue_context.md carries neither.
func TestSkillIndexReachesTheBriefNotTheContextFile(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		IssueID: "desc-test",
		AgentSkills: []SkillContextForEnv{
			{Name: "Frontend Standards", Description: "Use when working on Vue3 frontend code", Content: "x"},
		},
	}

	brief := buildMetaSkillContent("codex", ctx)
	if !strings.Contains(brief, "- **frontend-standards**") {
		t.Errorf("runtime brief missing the skill index entry; got:\n%s", brief)
	}
	if !strings.Contains(brief, "Discovery is not application") {
		t.Errorf("runtime brief missing the skill activation protocol; got:\n%s", brief)
	}

	// The per-task sidecar is gone entirely (MUL-6984): the brief is the only
	// copy of the skill index, so writeContextFiles must not bring the file
	// back.
	dir := t.TempDir()
	if err := writeContextFiles(dir, "", ctx, nil); err != nil {
		t.Fatalf("writeContextFiles failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent_context", "issue_context.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("issue_context.md must not be written any more (MUL-6984); stat err = %v", err)
	}
}

// TestCommentTriggeredWorkflowPointsToSkillProtocol locks the Skills-protocol
// pointer onto a comment-triggered brief. This is the path the original
// regression actually travelled: the rework that added the test and the code
// review that missed the missing Javadoc were both comment/mention-triggered.
// The shared ## Skills block covers it, but a buried protocol with no early
// inline pointer is exactly the wording that got ignored — so step 3 carries
// the pointer, and it must call out code review / rework explicitly rather
// than only "writing code". MUL-6417 collapsed the reply/ownership modes into
// one workflow, so this now guards the single step every trigger runs.
func TestCommentTriggeredWorkflowPointsToSkillProtocol(t *testing.T) {
	t.Parallel()
	withSkills := buildMetaSkillContent("codex", TaskContextForEnv{
		IssueID:          "11111111-2222-3333-4444-555555555555",
		TriggerCommentID: "33333333-4444-5555-6666-777777777777",
		AgentSkills:      []SkillContextForEnv{{Name: "Backend Standards", Description: "Java", Content: "x"}},
	})
	if !strings.Contains(withSkills, "complete the Skills protocol in the `## Skills` section below") {
		t.Error("comment-triggered workflow must point at the Skills protocol when skills exist")
	}
	if !strings.Contains(withSkills, "reviewing code") {
		t.Error("comment-triggered pointer must explicitly cover code review / rework")
	}

	// No skills: the pointer must be absent so the brief never dangles a
	// reference to a Skills section that was not rendered.
	noSkills := buildMetaSkillContent("codex", TaskContextForEnv{
		IssueID:          "11111111-2222-3333-4444-555555555555",
		TriggerCommentID: "33333333-4444-5555-6666-777777777777",
	})
	if strings.Contains(noSkills, "complete the Skills protocol") {
		t.Error("comment-triggered brief must not reference the Skills protocol when the agent has no skills")
	}
}

# Local Skill Upload Design

## Context

The Skills page already supports three creation paths in
`packages/views/skills/components/create-skill-dialog.tsx`:

- manual skill creation
- URL import from ClawHub, Skills.sh, or GitHub
- copying an installed skill from an online local runtime

Upstream issue `multica-ai/multica#478` asks for importing a local skill
directory or zip, and `#702` clarifies the same workflow in Chinese: users may
receive or create a skill bundle locally and want to upload it into the
workspace. PR `#913` explores CLI and backend support for local directory
imports, while PR `#460` explores a browser-side folder picker. The product gap
left between those approaches is a first-class Web entry for user-selected local
folders or zip bundles that fits the current dialog architecture.

## Product Goal

Users can import one or more local skill bundles into the current workspace from
the Add Skill dialog without publishing the skill to a remote registry and
without relying on a local runtime scan.

The feature should make the source choices clear:

- `Import from URL` is for ClawHub, Skills.sh, and GitHub links.
- `Copy from runtime` is for skills already installed in a local runtime.
- `Upload folder or zip` is for files selected from the user's computer.

## Entry Point

Add a fourth method card to the Add Skill chooser:

`Upload folder or zip`

The card uses a familiar upload or folder-upload icon and a short description:
`Import a local skill folder or zipped skill bundle.`

Choosing this card opens a wider dialog body, matching the interaction density
of `RuntimeLocalSkillImportPanel` rather than the narrow URL form.

## Upload Panel

The upload panel has three stable regions.

The top region contains the file input surface:

- a dashed drop zone labelled `Drop a skill folder or .zip here`
- `Choose Folder` and `Choose Zip` buttons
- a compact hint that a valid skill must contain `SKILL.md`

The middle region is a preview and validation surface. After selection, the
client scans the selected files and displays either a single-skill preview or a
multi-skill list.

For a single skill, show:

- editable skill name
- editable description
- detected root folder or zip name
- file count
- `SKILL.md` as the main file
- supporting file list
- skipped file list with reasons

For multiple detected skills, show a checklist:

- one row per detected skill root
- checkbox for valid rows
- editable name and description per row
- file count per row
- disabled rows for invalid groups, with the validation reason

The bottom region contains the import summary and action:

- single skill: `Ready to import "<name>" into workspace`
- multiple skills: `Ready to import <n> skills into workspace`
- primary button: `Import` or `Import <n> skills`
- during import: `Importing <done> / <total>`

## Skill Detection

The client normalizes all selected paths to forward-slash relative paths and
groups files by the nearest directory containing `SKILL.md`.

Detection rules:

- If the selected root itself contains `SKILL.md`, treat it as a single skill.
- If child directories contain their own `SKILL.md`, treat each child root as a
  separate skill candidate.
- Ignore directories without `SKILL.md` unless they are supporting files under a
  detected skill root.
- Use `SKILL.md` YAML frontmatter for default `name` and `description`.
- Fall back to the skill root folder name when frontmatter is absent.

The first version should support selecting:

- a folder through `webkitdirectory`
- a zip file unpacked in the browser
- drag-and-drop of files or folders where the browser exposes directory entries

## File Policy

The workspace skill storage model stores supporting file content in text fields.
Until binary asset storage exists, the uploader should avoid pretending binary
assets are fully supported.

Apply the same limits across folder and zip imports:

- require `SKILL.md`
- reject absolute paths and path traversal
- skip hidden files and ignored metadata files
- skip files above 1 MiB
- cap each skill at 128 supporting files
- cap each skill bundle at 8 MiB of accepted text files
- skip binary or non-UTF-8 supporting files
- strip null bytes from accepted text content before submission

Skipped files are visible in the preview before import. If a skill has skipped
files, the user can still import it, but the preview makes the partial import
explicit.

## API Shape

Prefer a dedicated local-import API over calling generic `createSkill` directly
from the upload panel.

Add a workspace-scoped endpoint:

`POST /api/skills/import-local`

Request:

```json
{
  "skills": [
    {
      "name": "Code Review",
      "description": "Reviews pull requests",
      "content": "...SKILL.md content...",
      "files": [
        { "path": "templates/review.md", "content": "..." }
      ],
      "source": {
        "type": "uploaded_bundle",
        "label": "team-skills.zip/code-review"
      }
    }
  ]
}
```

Response:

```json
{
  "created": [
    { "skill": { "...": "..." }, "source_label": "team-skills.zip/code-review" }
  ],
  "skipped": [
    { "name": "Existing Skill", "reason": "already_exists" }
  ],
  "failed": [
    { "name": "Broken Skill", "reason": "missing_skill_md" }
  ]
}
```

The backend performs the authoritative validation even when the client already
validated the preview. This keeps the browser UI helpful without trusting it as
a security boundary.

## Backend Behavior

The backend should create each valid skill in its own transaction so a batch
import can partially succeed. A duplicate skill name should be reported as a
skipped item instead of failing the entire batch.

For each skill:

- validate the workspace and owner/admin permission
- validate name and `SKILL.md` content
- validate every file path with the existing skill-file path rules
- sanitize null bytes from text fields
- enforce file count and accepted-size limits
- create the skill and supporting files through the same create-with-files path
- publish normal skill-created realtime events for created skills

The endpoint should return structured per-item results so the UI can show a
summary without parsing human-readable error strings.

## Frontend Data Flow

Add a new panel beside the existing forms:

- extend `Method` with `upload`
- add the fourth `MethodChooser` card
- create `LocalSkillUploadPanel`
- add a typed API client method for `importLocalSkills`

After successful imports:

- seed each created skill detail cache
- invalidate `workspaceKeys.skills(wsId)`
- invalidate `workspaceKeys.agents(wsId)`
- select the first created skill if the dialog caller provides selection
- close the dialog only when at least one skill is created and there are no
  unresolved blocking failures

For partial success, keep the dialog open and show the result summary so the
user can see what was imported, skipped, or failed.

## Error Handling

Preview-time errors stay local and immediate:

- empty selection
- no `SKILL.md` found
- unreadable zip
- zip path traversal
- all files skipped

Import-time errors come from the API:

- permission failure
- duplicate names
- invalid paths missed by preview
- request size exceeded
- server transaction failure

The UI should prefer structured messages such as `already exists`, `missing
SKILL.md`, `unsupported binary file skipped`, and `bundle is too large`.

## Out of Scope

- Persistent binary asset storage for skill bundles.
- A public skill registry or package-version system.
- Runtime multi-select import. That belongs to `Copy from runtime`.
- Automatic upload from arbitrary filesystem paths without user selection.
- Editing imported files inside the upload preview before creation.

## Testing

Add focused coverage for:

- grouping selected files into one or many skill candidates
- parsing frontmatter defaults from `SKILL.md`
- skipping binary, hidden, oversized, and traversal-path files
- zip import path normalization and traversal rejection
- upload panel states for empty, invalid, single-skill, and multi-skill inputs
- API client response parsing for created, skipped, and failed results
- backend validation and partial-success behavior
- duplicate-name handling as a skipped result
- cache invalidation after successful import

Manual verification should cover:

- folder picker import of a single skill
- zip import of a single skill
- folder or zip import containing multiple skills
- partial import with one duplicate skill
- partial import with binary supporting files

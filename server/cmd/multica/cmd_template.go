package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// ---------------------------------------------------------------------------
// Template commands — workspace-scoped CRUD for issue (task) templates.
//
// The API stores a reusable issue title/content plus an opaque JSON config;
// variable declarations and issue defaults live in that config. `multica
// template` exposes the management surface without adding issue create
// integration.
// ---------------------------------------------------------------------------

type templateSummaryDTO struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Name        string  `json:"name"`
	IssueTitle  string  `json:"issue_title"`
	Config      any     `json:"config"`
	CreatedBy   *string `json:"created_by"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	ArchivedAt  *string `json:"archived_at,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}

type templateDTO struct {
	ID           string  `json:"id"`
	WorkspaceID  string  `json:"workspace_id"`
	Name         string  `json:"name"`
	IssueTitle   string  `json:"issue_title"`
	IssueContent string  `json:"issue_content"`
	Config       any     `json:"config"`
	CreatedBy    *string `json:"created_by"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	ArchivedAt   *string `json:"archived_at,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
}

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Manage workspace issue templates",
	Long: `Manage workspace issue templates.

Issue templates are reusable issue drafts: a name, a title template, an
optional content template, and an opaque JSON config. The config carries the
variable declarations and default-field overrides used by the template
instantiate API. Issue create --template is not implemented.

Templates are addressed by name (case-insensitive) or UUID/id prefix; the CLI
resolves them to the UUIDs the API expects.`,
}

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List issue templates in the workspace",
	Args:  exactArgs(0),
	RunE:  runTemplateList,
}

var templateGetCmd = &cobra.Command{
	Use:   "get <id-or-name>",
	Short: "Show one issue template",
	Args:  exactArgs(1),
	RunE:  runTemplateGet,
}

var templateCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an issue template",
	Args:  exactArgs(0),
	RunE:  runTemplateCreate,
}

var templateUpdateCmd = &cobra.Command{
	Use:   "update <id-or-name>",
	Short: "Update an issue template",
	Args:  exactArgs(1),
	RunE:  runTemplateUpdate,
}

var templateArchiveCmd = &cobra.Command{
	Use:   "archive <id-or-name>",
	Short: "Archive an issue template",
	Args:  exactArgs(1),
	RunE:  makeTemplateArchiveRun(true),
}

var templateUnarchiveCmd = &cobra.Command{
	Use:   "unarchive <id-or-name>",
	Short: "Restore an archived issue template",
	Args:  exactArgs(1),
	RunE:  makeTemplateArchiveRun(false),
}

func init() {
	templateCmd.AddCommand(templateListCmd)
	templateCmd.AddCommand(templateGetCmd)
	templateCmd.AddCommand(templateCreateCmd)
	templateCmd.AddCommand(templateUpdateCmd)
	templateCmd.AddCommand(templateArchiveCmd)
	templateCmd.AddCommand(templateUnarchiveCmd)

	templateListCmd.Flags().String("output", "table", "Output format: table or json")
	templateListCmd.Flags().Bool("full-id", false, "Show full UUIDs in table output")
	templateGetCmd.Flags().String("output", "json", "Output format: table or json")

	templateCreateCmd.Flags().String("name", "", "Template name (required)")
	templateCreateCmd.Flags().String("issue-title", "", "Issue title template (required)")
	templateCreateCmd.Flags().String("issue-content", "", "Issue content template")
	templateCreateCmd.Flags().String("config", "", "JSON object with template config (variables, defaults)")
	templateCreateCmd.Flags().String("output", "json", "Output format: table or json")

	templateUpdateCmd.Flags().String("name", "", "New template name")
	templateUpdateCmd.Flags().String("issue-title", "", "New issue title template")
	templateUpdateCmd.Flags().String("issue-content", "", "New issue content template")
	templateUpdateCmd.Flags().String("config", "", "New JSON config object")
	templateUpdateCmd.Flags().String("output", "json", "Output format: table or json")

	templateArchiveCmd.Flags().String("output", "json", "Output format: table or json")
	templateUnarchiveCmd.Flags().String("output", "json", "Output format: table or json")
}

func fetchTemplateSummaries(ctx context.Context, client *cli.APIClient) ([]templateSummaryDTO, error) {
	if client.WorkspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required to list issue templates; use --workspace-id or set MULTICA_WORKSPACE_ID")
	}
	params := url.Values{
		"workspace_id":     {client.WorkspaceID},
		"include_archived": {"true"},
	}
	var summaries []templateSummaryDTO
	if err := client.GetJSON(ctx, "/api/issue-templates?"+params.Encode(), &summaries); err != nil {
		return nil, fmt.Errorf("list issue templates: %w", err)
	}
	return summaries, nil
}

// resolveTemplateRef matches a CLI ref by full UUID, unambiguous UUID prefix,
// then case-insensitive name.
func resolveTemplateRef(ctx context.Context, client *cli.APIClient, ref string) (resolvedID, error) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return resolvedID{}, fmt.Errorf("template id or name is required")
	}
	if uuidRegexp.MatchString(trimmed) {
		return resolvedID{ID: trimmed, Display: trimmed}, nil
	}

	summaries, err := fetchTemplateSummaries(ctx, client)
	if err != nil {
		return resolvedID{}, err
	}

	prefix, prefixErr := normalizeUUIDPrefix(trimmed)
	if prefixErr == nil {
		matches := make([]idCandidate, 0, 1)
		for _, s := range summaries {
			if s.ID == "" {
				continue
			}
			if strings.HasPrefix(compactUUID(s.ID), prefix) {
				matches = append(matches, idCandidate{ID: s.ID, Display: s.Name})
			}
		}
		switch len(matches) {
		case 1:
			return resolvedID{ID: matches[0].ID, Display: matches[0].Display}, nil
		case 0:
			// Fall through to name matching.
		default:
			return resolvedID{}, ambiguousIDPrefixError("template", trimmed, matches)
		}
	}

	lower := strings.ToLower(trimmed)
	names := make([]string, 0, len(summaries))
	for _, s := range summaries {
		if strings.ToLower(s.Name) == lower {
			return resolvedID{ID: s.ID, Display: s.Name}, nil
		}
		names = append(names, s.Name)
	}
	if len(names) > 0 {
		return resolvedID{}, fmt.Errorf("template %q not found; available: %s", ref, strings.Join(names, ", "))
	}
	return resolvedID{}, fmt.Errorf("template %q not found; run 'multica template list' to see available templates", ref)
}

func newTemplateAPIClient(cmd *cobra.Command) (*cli.APIClient, error) {
	if _, err := requireWorkspaceID(cmd); err != nil {
		return nil, err
	}
	return newAPIClient(cmd)
}

func runTemplateList(cmd *cobra.Command, _ []string) error {
	client, err := newTemplateAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	summaries, err := fetchTemplateSummaries(ctx, client)
	if err != nil {
		return err
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, summaries)
	}

	fullID, _ := cmd.Flags().GetBool("full-id")
	headers := []string{"ID", "NAME", "TITLE", "ARCHIVED", "UPDATED"}
	rows := make([][]string, 0, len(summaries))
	for _, s := range summaries {
		updated := s.UpdatedAt
		if len(updated) >= 10 {
			updated = updated[:10]
		}
		rows = append(rows, []string{
			displayID(s.ID, fullID),
			s.Name,
			s.IssueTitle,
			templateArchivedLabel(s.ArchivedAt, s.Enabled),
			updated,
		})
	}
	cli.PrintTable(os.Stdout, headers, rows)
	return nil
}

func runTemplateGet(cmd *cobra.Command, args []string) error {
	client, err := newTemplateAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	templateRef, err := resolveTemplateRef(ctx, client, args[0])
	if err != nil {
		return err
	}

	var template templateDTO
	if err := client.GetJSON(ctx, "/api/issue-templates/"+templateRef.ID, &template); err != nil {
		return fmt.Errorf("get issue template: %w", err)
	}

	return printTemplateResult(cmd, &template)
}

func parseTemplateConfigFlag(flag, raw string) (json.RawMessage, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object: %w", flag, err)
	}
	if obj == nil {
		return nil, fmt.Errorf("%s must be a JSON object", flag)
	}
	return json.RawMessage(raw), nil
}

func runTemplateCreate(cmd *cobra.Command, _ []string) error {
	name, _ := cmd.Flags().GetString("name")
	issueTitle, _ := cmd.Flags().GetString("issue-title")
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("--name is required")
	}
	if strings.TrimSpace(issueTitle) == "" {
		return fmt.Errorf("--issue-title is required")
	}

	configRaw, err := parseTemplateConfigFlag("--config", mustString(cmd, "config"))
	if err != nil {
		return err
	}

	client, err := newTemplateAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	body := map[string]any{
		"name":        strings.TrimSpace(name),
		"issue_title": strings.TrimSpace(issueTitle),
	}
	if v, _ := cmd.Flags().GetString("issue-content"); v != "" {
		body["issue_content"] = v
	}
	if configRaw != nil {
		body["config"] = json.RawMessage(configRaw)
	}

	var template templateDTO
	if err := client.PostJSON(ctx, "/api/issue-templates", body, &template); err != nil {
		return fmt.Errorf("create issue template: %w", err)
	}

	return printTemplateResult(cmd, &template)
}

func runTemplateUpdate(cmd *cobra.Command, args []string) error {
	body := map[string]any{}
	if cmd.Flags().Changed("name") {
		body["name"] = mustString(cmd, "name")
	}
	if cmd.Flags().Changed("issue-title") {
		body["issue_title"] = mustString(cmd, "issue-title")
	}
	if cmd.Flags().Changed("issue-content") {
		body["issue_content"] = mustString(cmd, "issue-content")
	}
	if cmd.Flags().Changed("config") {
		configRaw, err := parseTemplateConfigFlag("--config", mustString(cmd, "config"))
		if err != nil {
			return err
		}
		if configRaw == nil {
			body["config"] = json.RawMessage(`{}`)
		} else {
			body["config"] = json.RawMessage(configRaw)
		}
	}
	if len(body) == 0 {
		return fmt.Errorf("nothing to update; pass at least one of --name, --issue-title, --issue-content, --config")
	}

	client, err := newTemplateAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	templateRef, err := resolveTemplateRef(ctx, client, args[0])
	if err != nil {
		return err
	}

	var template templateDTO
	if err := client.PutJSON(ctx, "/api/issue-templates/"+templateRef.ID, body, &template); err != nil {
		return fmt.Errorf("update issue template: %w", err)
	}

	return printTemplateResult(cmd, &template)
}

func makeTemplateArchiveRun(archive bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		client, err := newTemplateAPIClient(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := cli.APIContext(context.Background())
		defer cancel()

		templateRef, err := resolveTemplateRef(ctx, client, args[0])
		if err != nil {
			return err
		}

		path := "/api/issue-templates/" + templateRef.ID
		if archive {
			path += "/archive"
		} else {
			path += "/unarchive"
		}
		var template templateDTO
		if err := client.PostJSON(ctx, path, nil, &template); err != nil {
			if archive {
				return fmt.Errorf("archive issue template: %w", err)
			}
			return fmt.Errorf("unarchive issue template: %w", err)
		}

		return printTemplateResult(cmd, &template)
	}
}

func templateArchivedLabel(archivedAt *string, enabled *bool) string {
	if archivedAt != nil && *archivedAt != "" {
		return "yes"
	}
	if enabled != nil && !*enabled {
		return "yes"
	}
	return ""
}

func printTemplateResult(cmd *cobra.Command, template *templateDTO) error {
	if output, _ := cmd.Flags().GetString("output"); output == "table" {
		headers := []string{"ID", "NAME", "TITLE", "ARCHIVED", "UPDATED"}
		updated := template.UpdatedAt
		if len(updated) >= 10 {
			updated = updated[:10]
		}
		rows := [][]string{{
			template.ID,
			template.Name,
			template.IssueTitle,
			templateArchivedLabel(template.ArchivedAt, template.Enabled),
			updated,
		}}
		cli.PrintTable(os.Stdout, headers, rows)
		return nil
	}
	return cli.PrintJSON(os.Stdout, template)
}

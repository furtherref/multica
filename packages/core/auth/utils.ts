/**
 * Machine-readable error code the server attaches to a 403 response body
 * when a request was rejected because the acting account is suspended.
 * See server/internal/handler write path — `{"error":"account suspended","code":"ACCOUNT_SUSPENDED"}`.
 */
export const ACCOUNT_SUSPENDED_CODE = "ACCOUNT_SUSPENDED";

/**
 * StorageAdapter key used to hand off "why did my session end" from the
 * ApiClient (which detects the rejection) to the login page (which reads it
 * once, at boot, to render a "your account was suspended" message) without
 * either module importing the other. Cleared by the login page after read.
 */
export const SESSION_ENDED_REASON_KEY = "multica_session_ended_reason";

/**
 * Validate a post-login redirect URL and return it only if safe to follow.
 *
 * Only single-slash relative paths (e.g. `/invite/abc`) are accepted. Returns
 * `null` for unsafe or empty input — call sites decide the fallback so this
 * helper never overloads a specific path with "user did not pass next".
 *
 * Rejects:
 *   - `null` / empty string
 *   - absolute URLs (`https://evil.com`, `javascript:alert(1)`, …)
 *   - protocol-relative URLs (`//evil.com`)
 *   - paths containing backslashes (Windows-style or `/\\host`)
 *   - paths containing ASCII control characters (`\x00`–`\x1f`)
 */
export function sanitizeNextUrl(raw: string | null): string | null {
  if (!raw) return null;
  if (!raw.startsWith("/") || raw.startsWith("//")) return null;
  // eslint-disable-next-line no-control-regex -- intentional: rejecting control chars is the whole point
  if (/[\x00-\x1f\\]/.test(raw)) return null;
  return raw;
}

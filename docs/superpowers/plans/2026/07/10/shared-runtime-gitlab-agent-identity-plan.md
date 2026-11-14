# Shared Runtime GitLab Agent Identity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enforce per-Agent GitLab commit identity and HTTPS credentials for every real Copilot task on `furtherref-agent1`, failing before Copilot starts when `GITLAB_TOKEN` is missing or invalid.

**Architecture:** Install a root-owned Bash wrapper selected through `MULTICA_COPILOT_PATH`. Probe invocations pass directly to the pinned Copilot executable; real tasks validate the Agent token with GitLab `/api/v4/user`, export process-scoped author/committer identity, append a host-locked in-memory Git credential helper, and then `exec` Copilot.

**Tech Stack:** Bash 4.4, curl 7.61, Python 3.6 standard library, Git 2.43, Multica CLI 0.3.42, GitHub Copilot CLI 1.0.70, SSH/SCP.

**Design spec:** `docs/superpowers/specs/2026/07/10/shared-runtime-gitlab-agent-identity-design.md`

---

## Scope and Baseline

- Target: `root@47.116.49.173`, SSH port `2222`, hostname
  `furtherref-agent1`.
- SSH key:
  `/Users/zhangqiang/Documents/furtherref/服务器/ssh证书/id_rsa`.
- Real Copilot path:
  `/usr/local/lib/nodejs/node-v22.23.1-linux-x64/bin/copilot`.
- Live acceptance workspace:
  `c5d82d69-84dd-499f-ad51-78019e5b96c9`.
- Local valid-token source:
  `/Users/zhangqiang/Downloads/gitlab_token`, mode `0600`.
- `jq` is absent. Use the existing Python 3.6 runtime for strict JSON parsing;
  do not install another system package.
- This is a server-only deployment. Product code, API, database, frontend,
  release artifacts, root Git config, cached-repository config, and remote
  URLs remain unchanged.
- Build/test files live under `/tmp` locally and `/root/.cache` remotely. They
  are not committed. The plan and design documents are the only repository
  changes.

## Exit Codes

| Code | Meaning |
|---:|---|
| `70` | Required Agent token missing |
| `71` | GitLab identity endpoint unavailable |
| `72` | Token rejected or unsafe for an HTTP header |
| `73` | Other non-200 GitLab response |
| `74` | Invalid, incomplete, inactive, or unsafe profile |
| `75` | Invalid inherited `GIT_CONFIG_COUNT` |
| `76` | Invalid wrapper config, dependency, or real Copilot path |

## Files

**Permanent server files**

- Create: `/opt/multica-gitlab-identity/bin/copilot-wrapper`
- Create: `/opt/multica-gitlab-identity/bin/git-credential-multica-gitlab`
- Create: `/opt/multica-gitlab-identity/etc/runtime.conf`
- Create: `/etc/profile.d/multica-gitlab-identity.sh`

**Temporary test files**

- Create: `/tmp/multica-gitlab-identity-build/tests/fake-copilot`
- Create: `/tmp/multica-gitlab-identity-build/tests/fake-gitlab.py`
- Create: `/tmp/multica-gitlab-identity-build/tests/run-tests.sh`
- Create: `/root/multica-gitlab-identity-backups/$DEPLOY_ID/` for non-secret
  before/after evidence

---

### Task 1: Capture the baseline and open a zero-task maintenance window

**Files:**
- Create: `/root/multica-gitlab-identity-backups/$DEPLOY_ID/daemon-before.json`
- Create: `/root/multica-gitlab-identity-backups/$DEPLOY_ID/protected-before.sha256`
- Create: `/root/multica-gitlab-identity-backups/$DEPLOY_ID/cache-config-before.sha256`

- [ ] **Step 1: Define local execution variables**

```bash
export TARGET=root@47.116.49.173
export SSH_PORT=2222
export SSH_KEY='/Users/zhangqiang/Documents/furtherref/服务器/ssh证书/id_rsa'
export DEPLOY_ID="$(date -u +%Y%m%dT%H%M%SZ)"
export LOCAL_STAGE=/tmp/multica-gitlab-identity-build
export REMOTE_STAGE="/root/.cache/multica-gitlab-identity-${DEPLOY_ID}"
export WORKSPACE_ID=c5d82d69-84dd-499f-ad51-78019e5b96c9
```

Expected: all variables are non-empty and `DEPLOY_ID` contains only UTC date
characters.

- [ ] **Step 2: Reconfirm versions and the real executable**

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  'hostname; multica --version; command -v copilot; copilot --version; bash --version | head -1; curl --version | head -1; python3 --version; git --version'
```

Expected: `furtherref-agent1`, Multica `0.3.42`, Copilot `1.0.70`, Bash `4.4`,
curl `7.61.1`, Python `3.6.8`, and Git `2.43.7`. Stop and revise this plan if
the real Copilot path has changed.

- [ ] **Step 3: Require zero active tasks**

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  'multica daemon status --output json' \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["active_task_count"]); raise SystemExit(0 if d["status"]=="running" and d["active_task_count"]==0 else 1)'
```

Expected: prints `0` and exits `0`. If not, repeat every ten seconds. Never
stop an active task to deploy this change.

- [ ] **Step 4: Record non-secret baseline evidence**

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "DEPLOY_ID='$DEPLOY_ID' /usr/bin/bash -s" <<'REMOTE'
set -euo pipefail
evidence="/root/multica-gitlab-identity-backups/${DEPLOY_ID}"
install -d -m 0700 -o root -g root "$evidence"
multica daemon status --output json > "$evidence/daemon-before.json"

for path in /root/.gitconfig /root/.git-credentials; do
  if [[ -e "$path" ]]; then
    sha256sum "$path"
  else
    printf 'MISSING  %s\n' "$path"
  fi
done > "$evidence/protected-before.sha256"

cache_config='/root/multica_workspaces/.repos/c5d82d69-84dd-499f-ad51-78019e5b96c9/gitlab.tigermed.net+iTigermed-cloud+iTigermed-cloud-application+project-operation-domain+cpms+cpms-frontend.git/config'
sha256sum "$cache_config" > "$evidence/cache-config-before.sha256"
for path in /root/.gitconfig /root/.git-credentials "$cache_config"; do
  if [[ -e "$path" ]]; then
    stat -c '%a %U:%G %n' "$path"
  else
    printf 'MISSING %s\n' "$path"
  fi
done > "$evidence/protected-before.stat"
command -v copilot > "$evidence/copilot-path-before.txt"
readlink -f "$(command -v copilot)" > "$evidence/copilot-target-before.txt"
copilot --version > "$evidence/copilot-version-before.txt"
REMOTE
```

Expected: evidence directory is `0700`; no command reads or prints credential
contents.

---

### Task 2: Write isolated wrapper/helper tests first

**Files:**
- Create: `/tmp/multica-gitlab-identity-build/tests/fake-copilot`
- Create: `/tmp/multica-gitlab-identity-build/tests/fake-gitlab.py`
- Create: `/tmp/multica-gitlab-identity-build/tests/run-tests.sh`

- [ ] **Step 1: Create the temporary tree**

```bash
install -d -m 0700 "$LOCAL_STAGE/bin" "$LOCAL_STAGE/etc" "$LOCAL_STAGE/tests"
```

- [ ] **Step 2: Create the fake Copilot executable**

Use `apply_patch` to create `$LOCAL_STAGE/tests/fake-copilot`:

```bash
#!/usr/bin/bash
set -euo pipefail

capture_dir=${FAKE_COPILOT_CAPTURE_DIR:?capture directory required}
task_id=${MULTICA_TASK_ID:-probe}
safe_id=$(printf '%s' "$task_id" | tr -c 'A-Za-z0-9_.-' '_')
prefix="$capture_dir/$safe_id"
install -d -m 0700 "$capture_dir"

printf '%s\n' "$@" > "${prefix}.args"
{
  for key in GIT_AUTHOR_NAME GIT_AUTHOR_EMAIL GIT_COMMITTER_NAME \
    GIT_COMMITTER_EMAIL GIT_TERMINAL_PROMPT GIT_ASKPASS; do
    printf '%s=%s\n' "$key" "${!key-}"
  done
  env | LC_ALL=C sort | grep '^GIT_CONFIG_' || true
} > "${prefix}.env"

if [[ ${1-} == '--credential-check' ]]; then
  filled=$(printf 'protocol=https\nhost=gitlab.tigermed.net\n\n' | git credential fill)
  username=$(printf '%s\n' "$filled" | sed -n 's/^username=//p')
  password=$(printf '%s\n' "$filled" | sed -n 's/^password=//p')
  [[ "$username" == oauth2 ]]
  [[ -n ${GITLAB_TOKEN-} && "$password" == "$GITLAB_TOKEN" ]]
  printf 'ok\n' > "${prefix}.credential"
fi

if [[ ${1-} == '--no-fallback-check' ]]; then
  set +e
  filled=$(printf 'protocol=https\nhost=gitlab.tigermed.net\n\n' \
    | env -u GITLAB_TOKEN git credential fill 2>/dev/null)
  rc=$?
  set -e
  [[ "$rc" -ne 0 ]]
  [[ "$filled" != *root-sentinel* ]]
  printf 'ok\n' > "${prefix}.no-fallback"
fi

if [[ ${1-} == '--version' ]]; then
  printf 'Fake Copilot 1.0\n'
fi
```

- [ ] **Step 3: Create the loopback GitLab user endpoint**

Use `apply_patch` to create `$LOCAL_STAGE/tests/fake-gitlab.py`:

```python
#!/usr/bin/python3
from __future__ import print_function

import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

ready_file = sys.argv[1]
requests = {}


def profile(user_id, username, name, email, state="active"):
    return {
        "id": user_id,
        "username": username,
        "name": name,
        "email": email,
        "state": state,
    }


class Handler(BaseHTTPRequestHandler):
    def log_message(self, _format, *_args):
        return

    def reply(self, status, body):
        payload = body if isinstance(body, bytes) else body.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def do_GET(self):
        if self.path != "/api/v4/user":
            self.reply(404, b"{}")
            return
        token = self.headers.get("PRIVATE-TOKEN", "")
        if token == "always-500":
            self.reply(500, b"{}")
            return
        if token == "retry-once":
            requests[token] = requests.get(token, 0) + 1
            if requests[token] == 1:
                self.reply(500, b"{}")
                return
            data = profile(3, "retry", "Retry User", "retry@example.test")
        elif token == "valid-a":
            data = profile(1, "alice", "Alice Example", "alice@example.test")
        elif token == "valid-b":
            data = profile(2, "zhangqiang", "张强", "zhangqiang@example.test")
        elif token == "inactive":
            data = profile(4, "blocked", "Blocked User", "blocked@example.test", "blocked")
        elif token == "incomplete":
            data = profile(5, "missing", "Missing Email", "")
        elif token == "newline":
            data = profile(6, "newline", "Bad\nName", "bad@example.test")
        elif token == "invalid-json":
            self.reply(200, b"{not-json")
            return
        else:
            self.reply(401, b"{}")
            return
        self.reply(200, json.dumps(data, ensure_ascii=False).encode("utf-8"))


server = HTTPServer(("127.0.0.1", 0), Handler)
with open(ready_file, "w") as stream:
    stream.write(str(server.server_port))
server.serve_forever()
```

- [ ] **Step 4: Create the complete isolated test runner**

Use `apply_patch` to create `$LOCAL_STAGE/tests/run-tests.sh`:

```bash
#!/usr/bin/bash
set -euo pipefail

source_root=${1:?source root required}
for script in copilot-wrapper git-credential-multica-gitlab; do
  [[ -f "$source_root/bin/$script" ]] || {
    printf 'FAIL: source script missing: %s\n' "$source_root/bin/$script" >&2
    exit 1
  }
done

test_root=$(mktemp -d /tmp/multica-gitlab-identity-test.XXXXXX)
server_pid=''
cleanup() {
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid"
    wait "$server_pid" 2>/dev/null || true
  fi
  [[ "$test_root" == /tmp/multica-gitlab-identity-test.* ]] && rm -rf -- "$test_root"
}
trap cleanup EXIT

fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }
pass() { printf 'PASS: %s\n' "$1"; }
expect_exit() {
  expected=$1
  diagnostic=$2
  shift 2
  set +e
  output=$("$@" 2>&1)
  rc=$?
  set -e
  [[ "$rc" -eq "$expected" ]] || fail "expected $expected, got $rc: $output"
  [[ "$output" == *"$diagnostic"* ]] || fail "missing diagnostic: $diagnostic"
}
fingerprint() {
  for path in /root/.gitconfig /root/.git-credentials; do
    [[ -e "$path" ]] && sha256sum "$path" || printf 'MISSING  %s\n' "$path"
  done
}

install -d -m 0700 "$test_root/bin" "$test_root/etc" "$test_root/tests" "$test_root/capture"
install -m 0755 "$source_root/bin/copilot-wrapper" "$test_root/bin/copilot-wrapper"
install -m 0755 "$source_root/bin/git-credential-multica-gitlab" "$test_root/bin/git-credential-multica-gitlab"
install -m 0755 "$source_root/tests/fake-copilot" "$test_root/tests/fake-copilot"
install -m 0755 "$source_root/tests/fake-gitlab.py" "$test_root/tests/fake-gitlab.py"

ready="$test_root/port"
python3 "$test_root/tests/fake-gitlab.py" "$ready" &
server_pid=$!
for _ in $(seq 1 50); do [[ -s "$ready" ]] && break; sleep 0.1; done
[[ -s "$ready" ]] || fail 'fake GitLab did not start'
port=$(<"$ready")
printf '%s\n' \
  "GITLAB_BASE_URL=http://127.0.0.1:${port}" \
  'GITLAB_HOST=gitlab.tigermed.net' \
  "REAL_COPILOT_PATH=$test_root/tests/fake-copilot" \
  > "$test_root/etc/runtime.conf"

wrapper="$test_root/bin/copilot-wrapper"
helper="$test_root/bin/git-credential-multica-gitlab"
capture="$test_root/capture"
marker="credential-$RANDOM-$$"
before=$(fingerprint)
request=$'protocol=https\nhost=gitlab.tigermed.net\n\n'

actual=$(printf '%s' "$request" | env GITLAB_TOKEN="$marker" "$helper" get)
expected=$(printf 'username=oauth2\npassword=%s' "$marker")
[[ "$actual" == "$expected" ]] || fail 'exact-host helper result differs'
wrong=$(printf 'protocol=https\nhost=example.com\n\n' | env GITLAB_TOKEN="$marker" "$helper" get)
[[ -z "$wrong" ]] || fail 'helper served another host'
wrong_protocol=$(printf 'protocol=http\nhost=gitlab.tigermed.net\n\n' | env GITLAB_TOKEN="$marker" "$helper" get)
[[ -z "$wrong_protocol" ]] || fail 'helper served a non-HTTPS request'
[[ -z $(printf '%s' "$request" | env GITLAB_TOKEN="$marker" "$helper" store) ]]
[[ -z $(printf '%s' "$request" | env GITLAB_TOKEN="$marker" "$helper" erase) ]]
[[ -z $(printf '%s' "$request" | env GITLAB_TOKEN="$marker" "$helper" unknown) ]]
set +e
missing=$(printf '%s' "$request" | env -u GITLAB_TOKEN "$helper" get 2>&1)
missing_rc=$?
set -e
[[ "$missing_rc" -eq 1 && -z "$missing" ]] || fail 'missing helper token did not fail silently'
pass 'credential helper is exact-host, read-only, and fail-closed'

probe=$(env -u MULTICA_TASK_ID -u GITLAB_TOKEN \
  FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" --version)
[[ "$probe" == 'Fake Copilot 1.0' ]] || fail 'probe did not pass through'
pass 'probe bypasses task validation'

expect_exit 70 'required Agent environment variable GITLAB_TOKEN is missing' \
  env -u GITLAB_TOKEN MULTICA_TASK_ID=missing FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" run
[[ ! -e "$capture/missing.args" ]] || fail 'Copilot ran without a token'
expect_exit 72 'GitLab token validation failed' \
  env MULTICA_TASK_ID=rejected GITLAB_TOKEN=rejected FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" run
expect_exit 72 'GitLab token validation failed' \
  env MULTICA_TASK_ID=unsafe GITLAB_TOKEN=$'bad\ntoken' FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" run
pass 'missing and rejected tokens stop before Copilot'

for token in inactive incomplete newline invalid-json; do
  expect_exit 74 'GitLab identity response is incomplete or invalid' \
    env MULTICA_TASK_ID="$token" GITLAB_TOKEN="$token" FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" run
done
expect_exit 73 'GitLab identity validation returned HTTP 500' \
  env MULTICA_TASK_ID=http500 GITLAB_TOKEN=always-500 FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" run
pass 'invalid profiles and non-200 responses fail closed'

env MULTICA_TASK_ID=retry GITLAB_TOKEN=retry-once \
  FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" run
[[ -e "$capture/retry.args" ]] || fail '5xx retry did not recover'
pass 'one 5xx retry recovers'

env MULTICA_TASK_ID=valid GITLAB_TOKEN=valid-a \
  FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" --credential-check 'arg with space'
grep -Fx 'GIT_AUTHOR_NAME=Alice Example' "$capture/valid.env" >/dev/null
grep -Fx 'GIT_AUTHOR_EMAIL=alice@example.test' "$capture/valid.env" >/dev/null
grep -Fx 'GIT_COMMITTER_NAME=Alice Example' "$capture/valid.env" >/dev/null
grep -Fx 'GIT_COMMITTER_EMAIL=alice@example.test' "$capture/valid.env" >/dev/null
grep -Fx 'GIT_CONFIG_COUNT=2' "$capture/valid.env" >/dev/null
grep -Fx 'GIT_CONFIG_KEY_0=credential.https://gitlab.tigermed.net.helper' "$capture/valid.env" >/dev/null
grep -Fx 'GIT_CONFIG_VALUE_0=' "$capture/valid.env" >/dev/null
grep -Fx 'GIT_CONFIG_KEY_1=credential.https://gitlab.tigermed.net.helper' "$capture/valid.env" >/dev/null
grep -Fx "GIT_CONFIG_VALUE_1=$helper" "$capture/valid.env" >/dev/null
grep -Fx 'GIT_TERMINAL_PROMPT=0' "$capture/valid.env" >/dev/null
grep -Fx 'GIT_ASKPASS=/bin/false' "$capture/valid.env" >/dev/null
[[ $(sed -n '1p' "$capture/valid.args") == '--credential-check' ]]
[[ $(sed -n '2p' "$capture/valid.args") == 'arg with space' ]]
[[ $(<"$capture/valid.credential") == ok ]]
pass 'valid task receives identity, helper, prompt guards, and original argv'

env MULTICA_TASK_ID=no-fallback GITLAB_TOKEN=valid-a \
  GIT_CONFIG_COUNT=1 \
  GIT_CONFIG_KEY_0=credential.helper \
  "GIT_CONFIG_VALUE_0=!f() { printf 'username=root-sentinel\\npassword=root-sentinel\\n'; }; f" \
  FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" --no-fallback-check
[[ $(<"$capture/no-fallback.no-fallback") == ok ]]
pass 'empty host reset prevents fallback to an inherited generic helper'

env MULTICA_TASK_ID=existing GITLAB_TOKEN=valid-a \
  GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.editor GIT_CONFIG_VALUE_0=: \
  FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" run
grep -Fx 'GIT_CONFIG_COUNT=3' "$capture/existing.env" >/dev/null
grep -Fx 'GIT_CONFIG_KEY_0=core.editor' "$capture/existing.env" >/dev/null
grep -Fx 'GIT_CONFIG_VALUE_0=:' "$capture/existing.env" >/dev/null
grep -Fx 'GIT_CONFIG_KEY_1=credential.https://gitlab.tigermed.net.helper' "$capture/existing.env" >/dev/null
grep -Fx 'GIT_CONFIG_VALUE_1=' "$capture/existing.env" >/dev/null
grep -Fx 'GIT_CONFIG_KEY_2=credential.https://gitlab.tigermed.net.helper' "$capture/existing.env" >/dev/null
grep -Fx "GIT_CONFIG_VALUE_2=$helper" "$capture/existing.env" >/dev/null
expect_exit 75 'task Git credential configuration is invalid' \
  env MULTICA_TASK_ID=badcount GITLAB_TOKEN=valid-a GIT_CONFIG_COUNT=bad \
  FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" run
pass 'existing Git config is preserved and invalid count is rejected'

cp "$test_root/etc/runtime.conf" "$test_root/etc/runtime.conf.good"
printf '%s\n' \
  "GITLAB_BASE_URL=http://127.0.0.1:${port}" \
  'GITLAB_HOST=gitlab.tigermed.net' \
  "REAL_COPILOT_PATH=$wrapper" > "$test_root/etc/runtime.conf"
expect_exit 76 'real Copilot executable is unavailable' \
  env -u MULTICA_TASK_ID -u GITLAB_TOKEN FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" --version
mv "$test_root/etc/runtime.conf.good" "$test_root/etc/runtime.conf"
pass 'recursive Copilot path is rejected'

env MULTICA_TASK_ID=parallel-a GITLAB_TOKEN=valid-a FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" run &
pid_a=$!
env MULTICA_TASK_ID=parallel-b GITLAB_TOKEN=valid-b FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" run &
pid_b=$!
wait "$pid_a"
wait "$pid_b"
grep -Fx 'GIT_AUTHOR_NAME=Alice Example' "$capture/parallel-a.env" >/dev/null
grep -Fx 'GIT_AUTHOR_NAME=张强' "$capture/parallel-b.env" >/dev/null
! grep -F '张强' "$capture/parallel-a.env" >/dev/null
! grep -F 'Alice Example' "$capture/parallel-b.env" >/dev/null
pass 'concurrent identities remain process-local'

kill "$server_pid"
wait "$server_pid" 2>/dev/null || true
server_pid=''
expect_exit 71 'GitLab identity validation unavailable' \
  env MULTICA_TASK_ID=unavailable GITLAB_TOKEN=valid-a FAKE_COPILOT_CAPTURE_DIR="$capture" "$wrapper" run
pass 'unavailable GitLab stops before Copilot'

after=$(fingerprint)
[[ "$before" == "$after" ]] || fail 'global Git files changed'
! grep -R -F "$marker" "$capture" >/dev/null
pass 'global Git files and token capture remain unchanged'
printf 'PASS: all isolated tests completed\n'
```

- [ ] **Step 5: Prove the RED state before implementation**

```bash
chmod 0755 "$LOCAL_STAGE/tests/fake-copilot" \
  "$LOCAL_STAGE/tests/fake-gitlab.py" \
  "$LOCAL_STAGE/tests/run-tests.sh"
bash "$LOCAL_STAGE/tests/run-tests.sh" "$LOCAL_STAGE"
```

Expected: FAIL with `source script missing` for `bin/copilot-wrapper`.

---

### Task 3: Implement the helper and Copilot wrapper

**Files:**
- Create: `/tmp/multica-gitlab-identity-build/etc/runtime.conf`
- Create: `/tmp/multica-gitlab-identity-build/bin/git-credential-multica-gitlab`
- Create: `/tmp/multica-gitlab-identity-build/bin/copilot-wrapper`
- Create: `/tmp/multica-gitlab-identity-build/profile.sh`

- [ ] **Step 1: Create the production config candidate**

Use `apply_patch` to create `$LOCAL_STAGE/etc/runtime.conf`:

```bash
GITLAB_BASE_URL=https://gitlab.tigermed.net
GITLAB_HOST=gitlab.tigermed.net
REAL_COPILOT_PATH=/usr/local/lib/nodejs/node-v22.23.1-linux-x64/bin/copilot
```

- [ ] **Step 2: Implement the credential helper**

Use `apply_patch` to create
`$LOCAL_STAGE/bin/git-credential-multica-gitlab`:

```bash
#!/usr/bin/bash
set -euo pipefail

operation=${1-}
case "$operation" in
  get) ;;
  store|erase|*) exit 0 ;;
esac

deploy_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
config="$deploy_root/etc/runtime.conf"
[[ -r "$config" ]] || exit 1
. "$config"
[[ -n ${GITLAB_HOST-} ]] || exit 1

protocol=''
host=''
while IFS= read -r line; do
  [[ -z "$line" ]] && break
  [[ "$line" == *=* ]] || continue
  key=${line%%=*}
  value=${line#*=}
  case "$key" in
    protocol) protocol=$value ;;
    host) host=$value ;;
  esac
done

[[ "$protocol" == https && "$host" == "$GITLAB_HOST" ]] || exit 0
[[ -n ${GITLAB_TOKEN-} ]] || exit 1
[[ "$GITLAB_TOKEN" != *$'\r'* && "$GITLAB_TOKEN" != *$'\n'* ]] || exit 1
printf 'username=oauth2\n'
printf 'password=%s\n' "$GITLAB_TOKEN"
```

- [ ] **Step 3: Implement the Copilot wrapper**

Use `apply_patch` to create `$LOCAL_STAGE/bin/copilot-wrapper`:

```bash
#!/usr/bin/bash
set -euo pipefail

readonly EXIT_TOKEN_MISSING=70
readonly EXIT_GITLAB_UNAVAILABLE=71
readonly EXIT_TOKEN_REJECTED=72
readonly EXIT_HTTP_STATUS=73
readonly EXIT_PROFILE_INVALID=74
readonly EXIT_GIT_CONFIG_INVALID=75
readonly EXIT_WRAPPER_CONFIG=76

fail() {
  local code=$1
  shift
  printf '[multica-gitlab-identity] %s\n' "$*" >&2
  exit "$code"
}

deploy_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
config="$deploy_root/etc/runtime.conf"
[[ -r "$config" ]] \
  || fail "$EXIT_WRAPPER_CONFIG" 'real Copilot executable is unavailable'
. "$config"

for value in "${GITLAB_BASE_URL-}" "${GITLAB_HOST-}" "${REAL_COPILOT_PATH-}"; do
  [[ -n "$value" ]] \
    || fail "$EXIT_WRAPPER_CONFIG" 'real Copilot executable is unavailable'
done

wrapper_path=$(readlink -f -- "$0" 2>/dev/null || true)
real_copilot=$(readlink -f -- "$REAL_COPILOT_PATH" 2>/dev/null || true)
[[ -n "$real_copilot" && -x "$real_copilot" && "$real_copilot" != "$wrapper_path" ]] \
  || fail "$EXIT_WRAPPER_CONFIG" 'real Copilot executable is unavailable'

if [[ -z ${MULTICA_TASK_ID+x} ]]; then
  exec "$real_copilot" "$@"
fi

command -v curl >/dev/null 2>&1 \
  || fail "$EXIT_WRAPPER_CONFIG" 'identity validator dependency is unavailable'
command -v python3 >/dev/null 2>&1 \
  || fail "$EXIT_WRAPPER_CONFIG" 'identity validator dependency is unavailable'
[[ -n ${GITLAB_TOKEN-} ]] \
  || fail "$EXIT_TOKEN_MISSING" 'required Agent environment variable GITLAB_TOKEN is missing'
[[ "$GITLAB_TOKEN" != *$'\r'* && "$GITLAB_TOKEN" != *$'\n'* ]] \
  || fail "$EXIT_TOKEN_REJECTED" 'GitLab token validation failed'

PROFILE_BODY=''
PROFILE_STATUS=''
request_profile() {
  local attempt response curl_rc status body
  for attempt in 1 2; do
    if response=$(curl \
      --silent \
      --connect-timeout 3 \
      --max-time 10 \
      --header @<(printf 'PRIVATE-TOKEN: %s\n' "$GITLAB_TOKEN") \
      --write-out $'\n%{http_code}' \
      "${GITLAB_BASE_URL%/}/api/v4/user" 2>/dev/null); then
      curl_rc=0
    else
      curl_rc=$?
    fi

    if [[ "$curl_rc" -ne 0 ]]; then
      [[ "$attempt" -eq 1 ]] && continue
      return 1
    fi
    [[ "$response" == *$'\n'* ]] || return 1
    status=${response##*$'\n'}
    body=${response%$'\n'*}
    [[ "$status" =~ ^[0-9]{3}$ ]] || return 1
    if [[ "$status" =~ ^5[0-9]{2}$ && "$attempt" -eq 1 ]]; then
      continue
    fi
    PROFILE_STATUS=$status
    PROFILE_BODY=$body
    return 0
  done
  return 1
}

request_profile \
  || fail "$EXIT_GITLAB_UNAVAILABLE" 'GitLab identity validation unavailable'
case "$PROFILE_STATUS" in
  200) ;;
  401|403) fail "$EXIT_TOKEN_REJECTED" 'GitLab token validation failed' ;;
  *) fail "$EXIT_HTTP_STATUS" "GitLab identity validation returned HTTP $PROFILE_STATUS" ;;
esac

if ! parsed_identity=$(printf '%s' "$PROFILE_BODY" | PYTHONIOENCODING=UTF-8 python3 -c '
from __future__ import print_function
import json
import sys

def clean(value):
    if not isinstance(value, str):
        raise ValueError()
    if any(char in value for char in ("\x00", "\r", "\n")):
        raise ValueError()
    value = value.strip()
    if not value:
        raise ValueError()
    return value

try:
    data = json.load(sys.stdin)
    user_id = data.get("id")
    if isinstance(user_id, bool) or not isinstance(user_id, int) or user_id <= 0:
        raise ValueError()
    clean(data.get("username"))
    if data.get("state") != "active":
        raise ValueError()
    name = clean(data.get("name"))
    email = clean(data.get("email"))
except Exception:
    raise SystemExit(1)

sys.stdout.write(name + "\n" + email)
'); then
  fail "$EXIT_PROFILE_INVALID" 'GitLab identity response is incomplete or invalid'
fi

readarray -t identity <<< "$parsed_identity"
[[ ${#identity[@]} -eq 2 ]] \
  || fail "$EXIT_PROFILE_INVALID" 'GitLab identity response is incomplete or invalid'
export GIT_AUTHOR_NAME=${identity[0]}
export GIT_AUTHOR_EMAIL=${identity[1]}
export GIT_COMMITTER_NAME=${identity[0]}
export GIT_COMMITTER_EMAIL=${identity[1]}

count_input=${GIT_CONFIG_COUNT-0}
if ! indexes=$(GIT_CONFIG_COUNT_INPUT="$count_input" python3 -c '
import os
import re
s = os.environ.get("GIT_CONFIG_COUNT_INPUT", "")
if re.fullmatch(r"[0-9]+", s) is None:
    raise SystemExit(1)
n = int(s, 10)
print(n)
print(n + 1)
print(n + 2)
'); then
  fail "$EXIT_GIT_CONFIG_INVALID" 'task Git credential configuration is invalid'
fi
readarray -t config_indexes <<< "$indexes"
[[ ${#config_indexes[@]} -eq 3 ]] \
  || fail "$EXIT_GIT_CONFIG_INVALID" 'task Git credential configuration is invalid'

reset_index=${config_indexes[0]}
helper_index=${config_indexes[1]}
final_count=${config_indexes[2]}
credential_key="credential.https://${GITLAB_HOST}.helper"
helper_path="$deploy_root/bin/git-credential-multica-gitlab"
export "GIT_CONFIG_KEY_${reset_index}=$credential_key"
export "GIT_CONFIG_VALUE_${reset_index}="
export "GIT_CONFIG_KEY_${helper_index}=$credential_key"
export "GIT_CONFIG_VALUE_${helper_index}=$helper_path"
export GIT_CONFIG_COUNT=$final_count
export GIT_TERMINAL_PROMPT=0
export GIT_ASKPASS=/bin/false

unset PROFILE_BODY parsed_identity indexes identity
exec "$real_copilot" "$@"
```

- [ ] **Step 4: Create the activation profile**

Use `apply_patch` to create `$LOCAL_STAGE/profile.sh`:

```bash
export MULTICA_COPILOT_PATH=/opt/multica-gitlab-identity/bin/copilot-wrapper
```

- [ ] **Step 5: Run local syntax checks**

```bash
chmod 0755 "$LOCAL_STAGE/bin/copilot-wrapper" \
  "$LOCAL_STAGE/bin/git-credential-multica-gitlab"
bash -n "$LOCAL_STAGE/bin/copilot-wrapper"
bash -n "$LOCAL_STAGE/bin/git-credential-multica-gitlab"
bash -n "$LOCAL_STAGE/tests/run-tests.sh"
bash -n "$LOCAL_STAGE/tests/fake-copilot"
bash -n "$LOCAL_STAGE/profile.sh"
python3 -m py_compile "$LOCAL_STAGE/tests/fake-gitlab.py"
```

Expected: all commands exit `0`.

---

### Task 4: Run GREEN tests and install atomically

**Files:**
- Create: `/opt/multica-gitlab-identity/bin/copilot-wrapper`
- Create: `/opt/multica-gitlab-identity/bin/git-credential-multica-gitlab`
- Create: `/opt/multica-gitlab-identity/etc/runtime.conf`
- Create: `/etc/profile.d/multica-gitlab-identity.sh`

- [ ] **Step 1: Upload the candidate tree**

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "install -d -m 0700 '$REMOTE_STAGE'"
scp -P "$SSH_PORT" -i "$SSH_KEY" -r "$LOCAL_STAGE/." \
  "$TARGET:$REMOTE_STAGE/"
```

- [ ] **Step 2: Run the Linux test suite**

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "chmod 0755 '$REMOTE_STAGE'/bin/* '$REMOTE_STAGE'/tests/* && '$REMOTE_STAGE/tests/run-tests.sh' '$REMOTE_STAGE'"
```

Expected: all lines are `PASS:` and the final line is
`PASS: all isolated tests completed`.

- [ ] **Step 3: Recheck zero active tasks**

Repeat Task 1 Step 3 immediately before installation. Expected: `0`.

- [ ] **Step 4: Install the tested tree by same-filesystem rename**

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "DEPLOY_ID='$DEPLOY_ID' REMOTE_STAGE='$REMOTE_STAGE' /usr/bin/bash -s" <<'REMOTE'
set -euo pipefail
target=/opt/multica-gitlab-identity
candidate="/opt/.multica-gitlab-identity.new.${DEPLOY_ID}"
rollback="/opt/.multica-gitlab-identity.rollback.${DEPLOY_ID}"
evidence="/root/multica-gitlab-identity-backups/${DEPLOY_ID}"
[[ ! -e "$candidate" && ! -e "$rollback" ]]

install -d -m 0755 -o root -g root "$candidate/bin" "$candidate/etc"
install -m 0755 -o root -g root "$REMOTE_STAGE/bin/copilot-wrapper" \
  "$candidate/bin/copilot-wrapper"
install -m 0755 -o root -g root "$REMOTE_STAGE/bin/git-credential-multica-gitlab" \
  "$candidate/bin/git-credential-multica-gitlab"
install -m 0644 -o root -g root "$REMOTE_STAGE/etc/runtime.conf" \
  "$candidate/etc/runtime.conf"
bash -n "$candidate/bin/copilot-wrapper"
bash -n "$candidate/bin/git-credential-multica-gitlab"

if [[ -e "$target" ]]; then
  mv "$target" "$rollback"
  printf '%s\n' "$rollback" > "$evidence/rollback-install-path.txt"
fi
mv "$candidate" "$target"
find "$target" -maxdepth 2 -type f -exec stat -c '%a %U:%G %n' {} \; \
  | LC_ALL=C sort > "$evidence/install.stat"
find "$target" -maxdepth 2 -type f -exec sha256sum {} \; \
  | LC_ALL=C sort > "$evidence/install.sha256"
REMOTE
```

Expected: binaries `0755 root:root`, config `0644 root:root`, and any previous
deployment preserved under `/opt`.

- [ ] **Step 5: Install the profile atomically**

```bash
scp -P "$SSH_PORT" -i "$SSH_KEY" "$LOCAL_STAGE/profile.sh" \
  "$TARGET:/etc/profile.d/.multica-gitlab-identity.${DEPLOY_ID}.new"
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "DEPLOY_ID='$DEPLOY_ID' /usr/bin/bash -s" <<'REMOTE'
set -euo pipefail
source_file="/etc/profile.d/.multica-gitlab-identity.${DEPLOY_ID}.new"
target=/etc/profile.d/multica-gitlab-identity.sh
evidence="/root/multica-gitlab-identity-backups/${DEPLOY_ID}"
bash -n "$source_file"
chown root:root "$source_file"
chmod 0644 "$source_file"
[[ ! -e "$target" ]] || cp -a "$target" "$evidence/profile-before.sh"
mv -f "$source_file" "$target"
stat -c '%a %U:%G %n' "$target" > "$evidence/profile-after.stat"
REMOTE
```

- [ ] **Step 6: Test the installed probe before daemon restart**

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  'env -u MULTICA_TASK_ID -u GITLAB_TOKEN /opt/multica-gitlab-identity/bin/copilot-wrapper --version'
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  'test "$(find /opt/multica-gitlab-identity -type f | wc -l)" -eq 3; ! grep -E "^(GITLAB_TOKEN|PRIVATE-TOKEN|password)=" /opt/multica-gitlab-identity/etc/runtime.conf'
```

Expected: real Copilot version `1.0.70`; no token required.

---

### Task 5: Activate and verify daemon/runtime health

**Files:**
- Modify process state: restart Multica daemon
- Create: `/root/multica-gitlab-identity-backups/$DEPLOY_ID/daemon-after.json`

- [ ] **Step 1: Record old PID with a zero-task gate**

```bash
export OLD_PID="$(
  ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    'multica daemon status --output json' \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["active_task_count"]==0; print(d["pid"])'
)"
```

- [ ] **Step 2: Restart from a login shell**

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "/bin/bash -lc 'multica daemon restart'"
```

Expected: restart succeeds. On failure, execute Task 7 rollback before
starting any acceptance task.

- [ ] **Step 3: Verify new PID, status, and process environment**

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "DEPLOY_ID='$DEPLOY_ID' OLD_PID='$OLD_PID' /usr/bin/bash -s" <<'REMOTE'
set -euo pipefail
for _ in $(seq 1 30); do
  status=$(multica daemon status --output json)
  state=$(printf '%s' "$status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')
  [[ "$state" == running ]] && break
  sleep 1
done
printf '%s\n' "$status" > "/root/multica-gitlab-identity-backups/${DEPLOY_ID}/daemon-after.json"
pid=$(printf '%s' "$status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["pid"])')
active=$(printf '%s' "$status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["active_task_count"])')
[[ "$pid" != "$OLD_PID" && "$active" -eq 0 ]]
tr '\0' '\n' < "/proc/${pid}/environ" \
  | grep -Fx 'MULTICA_COPILOT_PATH=/opt/multica-gitlab-identity/bin/copilot-wrapper'
REMOTE
```

- [ ] **Step 4: Verify the public Copilot runtime is online**

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "multica --workspace-id '$WORKSPACE_ID' runtime list --output json" \
  | python3 -c 'import json,sys; xs=[x for x in json.load(sys.stdin) if x.get("provider")=="copilot" and x.get("device_info","").startswith("furtherref-agent1")]; assert len(xs)==1; x=xs[0]; print(x["id"],x["status"],x["metadata"]["version"]); raise SystemExit(0 if x["status"]=="online" and "1.0.70" in x["metadata"]["version"] else 1)'
```

Expected: one runtime, `online`, version containing `1.0.70`.

- [ ] **Step 5: Prove cache credentials and protected files remain separate**

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "DEPLOY_ID='$DEPLOY_ID' /usr/bin/bash -s" <<'REMOTE'
set -euo pipefail
evidence="/root/multica-gitlab-identity-backups/${DEPLOY_ID}"
cache='/root/multica_workspaces/.repos/c5d82d69-84dd-499f-ad51-78019e5b96c9/gitlab.tigermed.net+iTigermed-cloud+iTigermed-cloud-application+project-operation-domain+cpms+cpms-frontend.git'
git --git-dir="$cache" fetch --dry-run origin
for path in /root/.gitconfig /root/.git-credentials; do
  [[ -e "$path" ]] && sha256sum "$path" || printf 'MISSING  %s\n' "$path"
done > "$evidence/protected-after-activation.sha256"
sha256sum "$cache/config" > "$evidence/cache-config-after-activation.sha256"
diff -u "$evidence/protected-before.sha256" "$evidence/protected-after-activation.sha256"
diff -u "$evidence/cache-config-before.sha256" "$evidence/cache-config-after-activation.sha256"
REMOTE
```

Expected: dry-run fetch succeeds and both diffs are empty.

---

### Task 6: Run live missing, invalid, and valid Agent acceptance

**Files/data:**
- Temporary private Agent in workspace
  `c5d82d69-84dd-499f-ad51-78019e5b96c9`
- Three temporary acceptance issues
- One temporary `cpms-frontend` branch, removed before cleanup

Run every Task 6 step in the same local Bash session. The exit trap defined in
Step 1 is the fail-safe that clears the temporary Agent's `custom_env` and
archives it if any later command aborts.

- [ ] **Step 1: Discover the current runtime and create an empty-env Agent**

```bash
set -euo pipefail
export RUNTIME_ID="$(
  ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' runtime list --output json" \
  | python3 -c 'import json,sys; xs=[x for x in json.load(sys.stdin) if x.get("provider")=="copilot" and x.get("device_info","").startswith("furtherref-agent1") and x.get("status")=="online"]; assert len(xs)==1; print(xs[0]["id"])'
)"
export AGENT_JSON="$(
  ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' agent create --name 'ops-gitlab-identity-$DEPLOY_ID' --description 'Temporary identity acceptance' --runtime-id '$RUNTIME_ID' --model gpt-5.5 --visibility private --custom-env '{}' --output json"
)"
export AGENT_ID="$(printf '%s' "$AGENT_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
unset AGENT_JSON
test -n "$RUNTIME_ID"
test -n "$AGENT_ID"

cleanup_acceptance() {
  local original_status=$?
  local variable issue_id
  set +e
  if [[ -n ${AGENT_ID-} ]]; then
    printf '%s' '{}' \
      | ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
        "multica --workspace-id '$WORKSPACE_ID' agent env set '$AGENT_ID' --custom-env-stdin --output json" \
      >/dev/null
    for variable in ISSUE_MISSING_ID ISSUE_INVALID_ID ISSUE_VALID_ID ISSUE_CLEANUP_ID; do
      issue_id=${!variable-}
      [[ -z "$issue_id" ]] || ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
        "multica --workspace-id '$WORKSPACE_ID' issue status '$issue_id' cancelled --output json" \
        >/dev/null
    done
    ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
      "multica --workspace-id '$WORKSPACE_ID' agent archive '$AGENT_ID' --output json" \
      >/dev/null
  fi
  return "$original_status"
}
trap cleanup_acceptance EXIT
trap 'exit 130' INT TERM
```

- [ ] **Step 2: Define a reusable task poller**

```bash
poll_issue_task() {
  local issue_id=$1
  local expected_status=$2
  local expected_text=$3
  local tasks selected status text
  for _ in $(seq 1 60); do
    tasks=$(ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
      "multica --workspace-id '$WORKSPACE_ID' agent tasks '$AGENT_ID' --output json")
    selected=$(printf '%s' "$tasks" | ISSUE_ID="$issue_id" python3 -c '
import json,os,sys
xs=[x for x in json.load(sys.stdin) if x.get("issue_id")==os.environ["ISSUE_ID"]]
print(json.dumps(sorted(xs,key=lambda x:x["created_at"])[-1]) if xs else "")
')
    if [[ -n "$selected" ]]; then
      status=$(printf '%s' "$selected" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')
      if [[ "$status" == failed || "$status" == completed || "$status" == cancelled ]]; then
        text=$(printf '%s' "$selected" | python3 -c 'import json,sys; x=json.load(sys.stdin); print((x.get("error") or "")+"\n"+((x.get("result") or {}).get("output") or ""))')
        if [[ "$status" == "$expected_status" && "$text" == *"$expected_text"* ]]; then
          printf '%s\n' "$selected"
          return 0
        fi
        printf 'unexpected terminal task result: status=%s expected=%s\n' \
          "$status" "$expected_status" >&2
        return 1
      fi
    fi
    sleep 2
  done
  printf 'task did not reach a terminal state\n' >&2
  return 1
}
```

- [ ] **Step 3: Verify missing-token failure before Copilot**

```bash
export ISSUE_MISSING_JSON="$(
  ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' issue create --title '[OPS] Missing GitLab token $DEPLOY_ID' --description 'This task must fail before Copilot starts.' --assignee-id '$AGENT_ID' --output json"
)"
export ISSUE_MISSING_ID="$(printf '%s' "$ISSUE_MISSING_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
unset ISSUE_MISSING_JSON
poll_issue_task "$ISSUE_MISSING_ID" failed \
  'required Agent environment variable GITLAB_TOKEN is missing' >/tmp/multica-missing-task.json
```

Expected: status `failed` with the stable missing-token diagnostic.

- [ ] **Step 4: Verify invalid-token failure before Copilot**

```bash
printf '%s' '{"GITLAB_TOKEN":"invalid-acceptance-token"}' \
  | ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' agent env set '$AGENT_ID' --custom-env-stdin --output json" \
  >/dev/null
export ISSUE_INVALID_JSON="$(
  ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' issue create --title '[OPS] Invalid GitLab token $DEPLOY_ID' --description 'This task must fail before Copilot starts.' --assignee-id '$AGENT_ID' --output json"
)"
export ISSUE_INVALID_ID="$(printf '%s' "$ISSUE_INVALID_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
unset ISSUE_INVALID_JSON
poll_issue_task "$ISSUE_INVALID_ID" failed \
  'GitLab token validation failed' >/tmp/multica-invalid-task.json
```

- [ ] **Step 5: Set the valid token through stdin, never argv**

```bash
export TOKEN_FILE=/Users/zhangqiang/Downloads/gitlab_token
test "$(stat -f '%Lp' "$TOKEN_FILE")" = 600
TOKEN_FILE="$TOKEN_FILE" python3 -c 'import json,os; token=open(os.environ["TOKEN_FILE"]).read().strip(); assert token; print(json.dumps({"GITLAB_TOKEN":token}))' \
  | ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' agent env set '$AGENT_ID' --custom-env-stdin --output json" \
  >/dev/null
```

Expected: command exits `0`; token never appears in terminal output or process
arguments.

- [ ] **Step 6: Enqueue an identity, commit, credential, and push check**

Use `apply_patch` to create `/tmp/multica-gitlab-valid-acceptance.txt`:

```text
This is an operations acceptance task. Execute only the steps below. Never print GITLAB_TOKEN, an environment dump, credential output, or an authorization header.

1. Run `git var GIT_AUTHOR_IDENT` and `git var GIT_COMMITTER_IDENT`.
2. Run `wrong=$(printf 'protocol=https\nhost=example.com\n\n' | /opt/multica-gitlab-identity/bin/git-credential-multica-gitlab get)` and require `wrong` to be empty. Record `WRONG_HOST_HELPER_OK` without printing any credential.
3. Run `repo=$(multica repo checkout https://gitlab.tigermed.net/iTigermed-cloud/iTigermed-cloud-application/project-operation-domain/cpms/cpms-frontend.git)` and `cd "$repo"`.
4. Run `git commit --allow-empty -m "test: verify shared runtime Git identity"`.
5. Set `branch="ops/multica-git-identity-acceptance-${MULTICA_TASK_ID:0:8}"` and `sha=$(git rev-parse HEAD)`.
6. Run `git show -s --format="AUTHOR=%an <%ae>%nCOMMITTER=%cn <%ce>%nSHA=%H" "$sha"`.
7. Run `git push -o ci.skip origin "HEAD:refs/heads/$branch"`.
8. Verify `git ls-remote origin "refs/heads/$branch"` returns the same SHA.
9. Return only the two `git var` lines, AUTHOR, COMMITTER, `SHA=$sha`, `BRANCH=$branch`, `WRONG_HOST_HELPER_OK`, and `IDENTITY_ACCEPTANCE_OK`. Do not delete the branch yet.
```

```bash
export ISSUE_VALID_JSON="$(
  ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' issue create --title '[OPS] Valid GitLab identity $DEPLOY_ID' --description-stdin --assignee-id '$AGENT_ID' --output json" \
    < /tmp/multica-gitlab-valid-acceptance.txt
)"
export ISSUE_VALID_ID="$(printf '%s' "$ISSUE_VALID_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
unset ISSUE_VALID_JSON
poll_issue_task "$ISSUE_VALID_ID" completed \
  'IDENTITY_ACCEPTANCE_OK' >/tmp/multica-valid-task.json
export ACCEPTANCE_OUTPUT="$(python3 -c 'import json; x=json.load(open("/tmp/multica-valid-task.json")); print((x.get("result") or {}).get("output") or "")')"
export ACCEPTANCE_SHA="$(printf '%s\n' "$ACCEPTANCE_OUTPUT" | sed -n 's/^SHA=//p' | tail -1)"
export ACCEPTANCE_BRANCH="$(printf '%s\n' "$ACCEPTANCE_OUTPUT" | sed -n 's/^BRANCH=//p' | tail -1)"
test -n "$ACCEPTANCE_SHA"
test -n "$ACCEPTANCE_BRANCH"
[[ "$ACCEPTANCE_OUTPUT" == *WRONG_HOST_HELPER_OK* ]]
```

Expected: author equals committer and matches the token's `/api/v4/user`
profile; credential-backed push and `ls-remote` succeed.

- [ ] **Step 7: Inspect GitLab attribution**

Open:

```text
https://gitlab.tigermed.net/iTigermed-cloud/iTigermed-cloud-application/project-operation-domain/cpms/cpms-frontend/-/commit/$ACCEPTANCE_SHA
```

Expected: GitLab links the commit author to the same authenticated account and
no pipeline was started because the push used `ci.skip`.

- [ ] **Step 8: Remove the test branch, cancel test issues, and archive the Agent**

```bash
printf '%s\n' \
  'This is cleanup only. Never print GITLAB_TOKEN or credential output.' \
  "Run \`repo=\$(multica repo checkout https://gitlab.tigermed.net/iTigermed-cloud/iTigermed-cloud-application/project-operation-domain/cpms/cpms-frontend.git)\`, \`cd \"\$repo\"\`, then \`git push origin \":refs/heads/$ACCEPTANCE_BRANCH\"\`." \
  "Verify \`git ls-remote origin \"refs/heads/$ACCEPTANCE_BRANCH\"\` returns no line and return IDENTITY_ACCEPTANCE_BRANCH_REMOVED." \
  > /tmp/multica-gitlab-cleanup-acceptance.txt

export ISSUE_CLEANUP_JSON="$(
  ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' issue create --title '[OPS] Remove identity acceptance branch $DEPLOY_ID' --description-stdin --assignee-id '$AGENT_ID' --output json" \
    < /tmp/multica-gitlab-cleanup-acceptance.txt
)"
export ISSUE_CLEANUP_ID="$(printf '%s' "$ISSUE_CLEANUP_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
unset ISSUE_CLEANUP_JSON
poll_issue_task "$ISSUE_CLEANUP_ID" completed \
  'IDENTITY_ACCEPTANCE_BRANCH_REMOVED' >/tmp/multica-cleanup-task.json

for issue_id in "$ISSUE_MISSING_ID" "$ISSUE_INVALID_ID" "$ISSUE_VALID_ID" "$ISSUE_CLEANUP_ID"; do
  ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' issue status '$issue_id' cancelled --output json" \
    >/dev/null
done
printf '%s' '{}' \
  | ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' agent env set '$AGENT_ID' --custom-env-stdin --output json" \
  >/dev/null
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "multica --workspace-id '$WORKSPACE_ID' agent archive '$AGENT_ID' --output json" \
  >/dev/null
unset AGENT_ID
trap - EXIT INT TERM
```

Expected: branch absent, issues cancelled, temporary Agent archived.

---

### Task 7: Collect final evidence and retain a fail-closed rollback

**Files:**
- Create: `/root/multica-gitlab-identity-backups/$DEPLOY_ID/daemon-final.json`
- Create: `/root/multica-gitlab-identity-backups/$DEPLOY_ID/protected-final.sha256`
- Optional: `/etc/profile.d/multica-gitlab-identity.sh.disabled.$DEPLOY_ID`

- [ ] **Step 1: Scan logs without placing the token in argv**

```bash
export LOCAL_LOG=/tmp/multica-gitlab-identity-daemon.log
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  'multica daemon logs -n 500' > "$LOCAL_LOG"
TOKEN_FILE="$TOKEN_FILE" LOCAL_LOG="$LOCAL_LOG" python3 -c '
import os
token=open(os.environ["TOKEN_FILE"]).read().strip()
logs=open(os.environ["LOCAL_LOG"],errors="replace").read()
assert token and token not in logs
assert "PRIVATE-TOKEN:" not in logs
'
```

Expected: exits `0`; stable diagnostics may appear, but no token, header, or
GitLab response body.

- [ ] **Step 2: Prove final health and unchanged shared Git files**

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "DEPLOY_ID='$DEPLOY_ID' WORKSPACE_ID='$WORKSPACE_ID' /usr/bin/bash -s" <<'REMOTE'
set -euo pipefail
evidence="/root/multica-gitlab-identity-backups/${DEPLOY_ID}"
multica daemon status --output json > "$evidence/daemon-final.json"
for path in /root/.gitconfig /root/.git-credentials; do
  [[ -e "$path" ]] && sha256sum "$path" || printf 'MISSING  %s\n' "$path"
done > "$evidence/protected-final.sha256"
cache_config='/root/multica_workspaces/.repos/c5d82d69-84dd-499f-ad51-78019e5b96c9/gitlab.tigermed.net+iTigermed-cloud+iTigermed-cloud-application+project-operation-domain+cpms+cpms-frontend.git/config'
sha256sum "$cache_config" > "$evidence/cache-config-final.sha256"
diff -u "$evidence/protected-before.sha256" "$evidence/protected-final.sha256"
diff -u "$evidence/cache-config-before.sha256" "$evidence/cache-config-final.sha256"
python3 - "$evidence/daemon-final.json" <<'PY'
import json
import sys
with open(sys.argv[1]) as stream:
    status = json.load(stream)
assert status["status"] == "running"
assert status["active_task_count"] == 0
assert "copilot" in status["agents"]
PY
REMOTE
```

Expected: both diffs empty and daemon healthy.

- [ ] **Step 3: Remove temporary build material**

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "case '$REMOTE_STAGE' in /root/.cache/multica-gitlab-identity-*) rm -rf -- '$REMOTE_STAGE' ;; *) exit 1 ;; esac"
case "$LOCAL_STAGE" in
  /tmp/multica-gitlab-identity-build) rm -rf -- "$LOCAL_STAGE" ;;
  *) exit 1 ;;
esac
rm -f -- /tmp/multica-gitlab-valid-acceptance.txt \
  /tmp/multica-gitlab-cleanup-acceptance.txt \
  /tmp/multica-missing-task.json \
  /tmp/multica-invalid-task.json \
  /tmp/multica-valid-task.json \
  /tmp/multica-cleanup-task.json \
  "$LOCAL_LOG"
```

Keep the root-only evidence and any `/opt` rollback directory through the
agreed observation window.

- [ ] **Step 4: Roll back if activation or live acceptance fails because of the wrapper**

First repeat Task 1 Step 3 to require zero active tasks, then run:

```bash
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "DEPLOY_ID='$DEPLOY_ID' /usr/bin/bash -s" <<'REMOTE'
set -euo pipefail
profile=/etc/profile.d/multica-gitlab-identity.sh
disabled="/etc/profile.d/multica-gitlab-identity.sh.disabled.${DEPLOY_ID}"
[[ ! -e "$profile" ]] || mv "$profile" "$disabled"
/bin/bash -lc 'unset MULTICA_COPILOT_PATH; multica daemon restart'

status=$(multica daemon status --output json)
printf '%s' "$status" | python3 -c '
import json,sys
d=json.load(sys.stdin)
assert d["status"] == "running"
assert d["active_task_count"] == 0
'
pid=$(printf '%s' "$status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["pid"])')
if tr '\0' '\n' < "/proc/${pid}/environ" | grep -q '^MULTICA_COPILOT_PATH='; then
  printf 'rollback failed: daemon still has MULTICA_COPILOT_PATH\n' >&2
  exit 1
fi

multica --workspace-id "$WORKSPACE_ID" runtime list --output json \
  | python3 -c '
import json,sys
xs=[x for x in json.load(sys.stdin) if x.get("provider")=="copilot" and x.get("device_info","").startswith("furtherref-agent1")]
assert len(xs)==1
assert xs[0]["status"]=="online"
'

rollback="/opt/.multica-gitlab-identity.rollback.${DEPLOY_ID}"
if [[ -d "$rollback" ]]; then
  mv /opt/multica-gitlab-identity \
    "/opt/.multica-gitlab-identity.failed.${DEPLOY_ID}"
  mv "$rollback" /opt/multica-gitlab-identity
fi
REMOTE
```

- [ ] **Step 5: Verify a real Copilot task launches after rollback**

Run in one local Bash shell:

```bash
export ROLLBACK_RUNTIME_ID="$(
  ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' runtime list --output json" \
  | python3 -c 'import json,sys; xs=[x for x in json.load(sys.stdin) if x.get("provider")=="copilot" and x.get("device_info","").startswith("furtherref-agent1") and x.get("status")=="online"]; assert len(xs)==1; print(xs[0]["id"])'
)"
export ROLLBACK_AGENT_JSON="$(
  ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' agent create --name 'ops-wrapper-rollback-$DEPLOY_ID' --description 'Temporary rollback launch check' --runtime-id '$ROLLBACK_RUNTIME_ID' --model gpt-5.5 --visibility private --custom-env '{}' --output json"
)"
export ROLLBACK_AGENT_ID="$(printf '%s' "$ROLLBACK_AGENT_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
unset ROLLBACK_AGENT_JSON
export ROLLBACK_ISSUE_JSON="$(
  ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' issue create --title '[OPS] Wrapper rollback launch $DEPLOY_ID' --description 'Return only ROLLBACK_TASK_OK.' --assignee-id '$ROLLBACK_AGENT_ID' --output json"
)"
export ROLLBACK_ISSUE_ID="$(printf '%s' "$ROLLBACK_ISSUE_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
unset ROLLBACK_ISSUE_JSON

for _ in $(seq 1 60); do
  result=$(ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
    "multica --workspace-id '$WORKSPACE_ID' agent tasks '$ROLLBACK_AGENT_ID' --output json")
  selected=$(printf '%s' "$result" | ISSUE_ID="$ROLLBACK_ISSUE_ID" python3 -c '
import json,os,sys
xs=[x for x in json.load(sys.stdin) if x.get("issue_id")==os.environ["ISSUE_ID"]]
print(json.dumps(sorted(xs,key=lambda x:x["created_at"])[-1]) if xs else "")
')
  if [[ -n "$selected" ]]; then
    status=$(printf '%s' "$selected" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')
    output=$(printf '%s' "$selected" | python3 -c 'import json,sys; print(((json.load(sys.stdin).get("result") or {}).get("output") or ""))')
    [[ "$status" != completed ]] || { [[ "$output" == *ROLLBACK_TASK_OK* ]]; break; }
    [[ "$status" != failed ]] || exit 1
  fi
  sleep 2
done
[[ ${output-} == *ROLLBACK_TASK_OK* ]]
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "multica --workspace-id '$WORKSPACE_ID' issue status '$ROLLBACK_ISSUE_ID' cancelled --output json" \
  >/dev/null
ssh -p "$SSH_PORT" -i "$SSH_KEY" -o BatchMode=yes "$TARGET" \
  "multica --workspace-id '$WORKSPACE_ID' agent archive '$ROLLBACK_AGENT_ID' --output json" \
  >/dev/null
```

Expected: normal Copilot discovery is online, daemon environment has no
`MULTICA_COPILOT_PATH`, a real Copilot task returns `ROLLBACK_TASK_OK`, and
protected-file checksums still match baseline. Diagnose before activating
again.

---

## Final Acceptance

- [ ] Target-host isolated tests pass, including two-token concurrency.
- [ ] Daemon restarts only after active task count reaches zero.
- [ ] Daemon environment selects the exact wrapper path.
- [ ] Copilot probe/runtime remains online at version `1.0.70`.
- [ ] Missing and invalid Agent tasks fail before Copilot starts.
- [ ] Valid Agent task receives author and committer from `/api/v4/user`.
- [ ] GitLab HTTPS uses the Agent token through the process-scoped helper.
- [ ] The deployed helper returns no credential for another host.
- [ ] Controlled commit is pushed, inspected in GitLab, and its branch removed.
- [ ] Daemon cache fetch still uses the existing shared credential.
- [ ] Root Git files, cache config, and remote URLs remain unchanged.
- [ ] No real token appears in argv, logs, repository files, captures, or
  permanent config.
- [ ] Temporary Agent, issues, branches, and build files are cleaned up.

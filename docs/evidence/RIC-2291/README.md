# RIC-2291 — autopilot 每日重复建单

## 缺陷

autopilot `9224cdf7-6b02-4131-950c-b3b63e74c0ac`（跨仓库 PR 定时合并与按机器部署）
的 trigger `b6da88d6` 是 `0 */4 * * *`（每天 6 次），而 `issue_title_template` 只有
`{{date}}`（天粒度）。两者粒度不匹配，同一天每次触发都建一条同名 issue。

v0.8.0 里唯一的去重是 `dispatchCreateIssue` 的
`LockAndFindRecentAutopilotDuplicate`（`server/internal/service/autopilot.go`，
窗口 `autopilotRecentDuplicateWindow = 60s`）。60 秒跨不过 4 小时的触发间隔，
所以每次触发都真的建单、真的派 run。

## 本次两项改动

### 1. 线上配置（已生效，立即止住）

trigger 降频 + 更新 label：

```
cron        0 */4 * * *  ->  0 8 * * *
label       每4小时合并+部署  ->  每日08:00合并+部署
next_run_at 2026-11-04T20:00:00Z -> 2026-11-05T00:00:00Z   (00:00Z = 08:00 Asia/Shanghai)
```

改动前后快照：`autopilot-before.json` / `trigger-before.json` / `trigger-after.json`。

回滚：

```bash
multica autopilot trigger-update 9224cdf7-6b02-4131-950c-b3b63e74c0ac \
  b6da88d6-6ade-4e2d-bd6d-51720fc02449 \
  --cron "0 */4 * * *" --label "每4小时合并+部署"
```

注意：仅降频到 2 次/天仍会建 2 个重复单，**不能单独作为修复**。

### 2. 代码修复（方案 A：同日去重）

新增 `autopilot.issue_dedup_policy`：

| 取值 | 行为 |
|---|---|
| `off`（默认，迁移回填） | 保持原 60 秒 title 窗口，SQL/Golang 行为逐字节不变 |
| `per_day` | 按 trigger 时区的自然日最多一条 issue；同日后续触发记 `skipped` + `reason_code=duplicate_same_day` |

关键实现点：

- 查询 `FindSameDayAutopilotIssue` **不过滤终态**。当天 issue 已 done/cancelled
  仍算「这天已经做过」—— PR 合并+部署本身不可并发，这正是需求里的正确性要求。
- 窗口按 **run 的 occurrence（`planned_at`，无则 `triggered_at`）** 归日，
  不是按 `issue.created_at`，也不是按 dispatch 的墙钟时间。调度器补跑/迟到
  一次 08:00 的槽位时，仍归到该槽位所属的那一天，不会因为落到了下一个自然日
  而重复建单。
- 窗口边界用 `time.Date(...).AddDate(0,0,1)` 算，不是 +24h，DST 日不重不漏。
- advisory lock 按 `(workspace, autopilot, project)`，不按 title —— per_day 的
  去重键是「天」，标题里带时间粒度也必须 collapse。
- `per_day` 取代 60 秒守卫而不是叠加（日窗口严格更宽，且两者对终态 issue 的
  处理相反）。

迁移编号用 **907**：`548` 在现网 `schema_migrations` 里已被
`548_task_supplement_comment_task_index` 占用，下游 `furtherref/main` 已到 906。

## 线上效果实测（2026-11-11 复核）

trigger 降频自 2026-11-05 00:00 UTC（= 08:00 Asia/Shanghai）起生效，`autopilot runs`
按日统计：

| 日期 | run 数 | 状态 |
|---|---|---|
| 2026-11-11 | 1 | completed |
| 2026-11-10 | 1 | completed |
| 2026-11-09 | 1 | completed |
| 2026-11-08 | 1 | completed |
| 2026-11-07 | 1 | completed |
| 2026-11-06 | 1 | completed |
| 2026-11-05 | 1 | completed |
| 2026-11-04（改前） | 5 | 3 completed / 2 failed |
| 2026-11-03（改前） | 6 | 6 completed |

连续 7 天每天 1 个 run、1 条 issue，改前为每天 5~6 个。验收标准 1/2/3/4/6 由此
实测满足。`next_run_at` 复核为 `2026-11-12T00:00:00Z`（= 次日 08:00 +08），
与 `0 8 * * *` + `Asia/Shanghai` 一致。

**验收 #5（真实 `autopilot run now` 冒烟）仍未做**，理由不变：该 autopilot 会合并
PR 并部署三台机器，拿它做验证副作用过大。同日去重由 DB 集成测试覆盖。

## 仍未闭环（2026-11-11 更新）

现网已于 2026-11-10 升级到 **v0.8.2**（commit `b7f8efca`，在 `origin/main` 上）。
复核该版本 `autopilot` 表仍**无** `issue_dedup_policy` 列，`FindRecentAutopilotDuplicateIssue`
仍是 60 秒窗口 + 按 title + `NOT (status = ANY(terminal))`。也就是说：

- 上游/下游都还没有同日去重，本单代码修复没有被别人做掉，仍然有效；
- 它依然需要「提 PR → 发版」才生效，线上目前唯一在起作用的仍是 trigger 降频。

把本分支合并到 `origin/main`（`d44d6866a9`）实测：只有 `server/internal/handler/admission.go`
一处一行别名冲突（两边各加一个 `ReasonXxx = dispatch.ReasonXxx`），合并后
`go build ./...` 与 `go test ./internal/issueguard ./internal/service` 全部通过。
即修复可以直接前移到当前主干，不需要重写。


## 验证

全部在隔离 Postgres（`ric2291-test-pg`，127.0.0.1:55432，pgvector/pgvector:pg17）
上跑，未触碰生产库。

```
migrate up                                   -> 907_autopilot_issue_dedup_policy OK
go build ./...                               -> OK
go test ./internal/service ./internal/issueguard ./internal/handler \
        ./cmd/server                          -> 全部 ok
  TestDispatchAutopilotPerDayDedupCreatesOneIssuePerDay
    - 同日 12:00 + 20:00 两次触发 -> 1 个 issue
    - 第二次 run: status=skipped, reason_code=duplicate_same_day
    - 次日 12:00 -> 新 issue（共 2 个）
  TestDispatchAutopilotSuppressesRecentDuplicateIssue   (原 60s 守卫) -> ok
  TestAutopilotLocalDayWindow / TestValidateIssueDedupPolicy -> ok
pnpm typecheck                               -> 10/10 tasks successful
pnpm lint                                    -> 7/7 tasks successful
packages/views test                          -> 446 files / 5511 tests passed
sqlc generate + sqlc vet                     -> clean
```

`packages/core` test 有 10 个失败、`apps/desktop` 有 1 个失败，均**不是本次引入**：

- `packages/core`：`TypeError: Cannot read properties of undefined (reading 'setItem')`。
  在基线 commit `94749bb8d1`（未改动的独立 worktree）复跑，失败数同为 10，
  用例集合一致 → pre-existing。
- `apps/desktop`：`Electron failed to install correctly` → 本机环境问题，未执行到断言
  （相同环境未安装 electron 二进制）。


未做：真实 `autopilot run now` 冒烟。该 autopilot 会合并 PR 并部署三台机器，
拿它做验证副作用过大；同日去重行为由上述 DB 集成测试覆盖。

## 尚未闭环

代码修复要生效必须把 backend 升级到含 907 迁移的构建。现网
`ghcr.io/multica-ai/multica-backend:v0.8.0` 不含本改动，且 907 需要按下游
`furtherref/main`（迁移已到 906）出 PR 后再发版。在发版前，线上靠第 1 项
配置降频兜底。

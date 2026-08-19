# 账号禁用（Account Suspension）设计方案

日期：2026-08-19
状态：待复核
分支：`feat/account-suspension`

## 背景与目标

需要支持"人员禁用"：账号被禁用后不可登录 Multica；已登录的会话在下一次请求时失效，前端自动返回登录页。

上游 `multica-ai/multica` 已有对口设计但未合并：

- **Issue #1688**（open）：v1 方案——`"user"` 表加 `account_status` 列（`active`/`suspended`），`suspended` 时所有认证路径统一返回 **403 + 稳定错误码 `ACCOUNT_SUSPENDED`**；会话失效靠中间件每次请求重新校验用户状态实现（本项目为无状态 JWT，无 sessions 表、无吊销机制）。软删除与邮箱唯一性调整推迟到 v2。
- **PR #1689**（open，已停滞）：完整实现了 #1688，reviewer 已 "approve with clarifications"，但 main 合入 Redis PAT 缓存（10 分钟 TTL）后产生 must-fix 回归——**PAT 缓存命中路径绕过禁用检查**，禁用后 PAT 仍可用最多 10 分钟；作者未再跟进。reviewer 另指出：已建立的 WebSocket 连接不会被踢，只拒新连接。

本方案在形状上完全对齐上游（列名、状态值、错误码、集中式 helper），将来上游合并其后代版本时同步成本接近零，并额外修复上游 PR 停滞的两个缺口（PAT 缓存绕过、WS 不踢线）。

## 现状要点（代码调研结论）

- 认证：无状态 JWT（HS256，cookie `multica_auth`，默认 30 天 TTL），登录方式为邮箱验证码 + Google OAuth，无密码。`POST /auth/logout` 仅清 cookie，无服务端吊销。
- 会话校验：`middleware.Auth`（`server/internal/middleware/auth.go`）与 `middleware.DaemonAuth`；凭证类型含 JWT、`mul_` PAT、`mat_` task token、`mdt_` daemon token、`mcn_` cloud PAT。JWT 路径目前零 DB 读。
- `"user"` 表无任何 status/disabled 列。但已存在硬编码应急黑名单 `server/internal/auth/temporary_disabled_users.go`，其注释明确说明"等账号禁用持久化到 user 模型后删除此文件"。它已在以下位置埋好检查点，全部返回 403 `"account disabled"`：
  - `middleware/auth.go` 全部 5 条认证路径（task_token / cloud_pat / pat_cache / pat / jwt）
  - `middleware/daemon_auth.go` 4 处
  - `realtime/hub.go` `authenticateToken` 2 处
  - `handler/auth.go`：`issueJWT`、`findOrCreateUser`、`SendCode`、`VerifyCode`、Google 登录、`IssueCliToken`
- 权限模型：只有 workspace 级 `member.role`（owner/admin/member），**无全局管理员概念**。
- 前端 401/403 处理：`packages/core/api/client.ts` 仅在 401 时调 `handleUnauthorized`；web/desktop 的 `onUnauthorized`（`packages/core/platform/core-provider.tsx`）只清 token 不导航，真正跳登录页依赖路由守卫看到 `user === null`（仅启动时 `getMe()` 401 触发）。**现有 403 不会触发任何登出/跳转**。mobile（`apps/mobile/app/_layout.tsx`）有完整的登出+跳转实现，可作参照。
- WebSocket：仅连接时校验；Hub 无按用户断连 API；客户端 `ws-client.ts` 不识别 `auth_error`，连接被拒会无限重连。
- 缓存：`auth.AuthCacheTTL = 10min`，供 `PATCache` / `DaemonTokenCache`；另有 `MembershipCache`。
- 成员移除的收敛模式：`revokeAndRemoveMember`（`server/internal/handler/workspace_revoke.go`）——取消进行中任务、force-offline runtime、删 daemon token、失效缓存。

## 已确认的设计决策

| 决策点 | 结论 |
| --- | --- |
| 权限模型 | 环境变量 `ADMIN_EMAILS` 配置系统管理员（逗号分隔邮箱），仅系统管理员可禁用/恢复；管理员不能禁用自己 |
| 现存 WS 连接 | 禁用时立即踢掉（新增 `Hub.DisconnectUser`） |
| 管理 UI | 本期一起做，新增全局 `/admin` 页（pre-workspace 单词路由，符合路由规则） |
| 与上游对齐 | 列名 `account_status`、状态值 `active`/`suspended`、错误码 `ACCOUNT_SUSPENDED`、集中式 `auth.UserMayAuthenticate` |

## 设计

### 数据库

- 迁移：`ALTER TABLE "user" ADD COLUMN account_status TEXT NOT NULL DEFAULT 'active'` + `CHECK (account_status IN ('active','suspended'))`。
- 不加索引、不加外键、不做级联，符合仓库迁移规则（加列本身无需 CONCURRENTLY）。
- `server/pkg/db/queries/user.sql` 增加按 id 读状态、按 id 更新状态的查询；`make sqlc` 重新生成（本机需 sqlc v1.31.1）。

### 后端强制逻辑

1. **集中检查**：新建 `server/internal/auth/account.go`：
   - `UserMayAuthenticate(status string) error`：仅 `active` 放行；`suspended`、空值、未知值一律拒绝（fail-closed）。
   - `WriteAccountSuspendedResponse(w)`：统一写出 `403` + `{"error":"account suspended","code":"ACCOUNT_SUSPENDED"}`。
2. **替换应急黑名单**：删除 `temporary_disabled_users.go`，其全部现有调用点原位替换为基于 DB 状态的检查（调用点清单见"现状要点"）。
3. **每请求生效与缓存**：
   - JWT 路径新增按 userID 的账号状态查询，配 Redis 缓存（复用 `auth.AuthCacheTTL` 10 分钟），命名如 `AccountStatusCache`。
   - **禁用/恢复操作必须主动按 userID 失效**：`AccountStatusCache` + `PATCache` + `DaemonTokenCache`，保证"下一次请求即失效"。这是对上游 PR #1689 停滞回归（PAT 缓存命中绕过检查）的修复：缓存命中路径同样执行状态检查，且写路径主动失效。
4. **管理 API**（新路由组，系统管理员中间件保护）：
   - `GET /api/admin/users`：列全部用户及 `account_status`。
   - `PATCH /api/admin/users/{id}/status`：body `{"status":"active"|"suspended"}`；路径 UUID 用 `parseUUIDOrBadRequest`；禁止操作自己。
   - 系统管理员判定：请求用户 email ∈ `ADMIN_EMAILS`（server config 读取，大小写不敏感比对）。
   - `GET /api/me` 响应增加 `is_system_admin: boolean`，供前端显隐入口。
5. **禁用时的收敛**（单事务/顺序执行，参照 `revokeAndRemoveMember` 模式）：
   - 更新 `account_status = 'suspended'`；
   - 取消该用户进行中的任务、force-offline 其 runtimes；
   - 失效上述三类缓存；
   - 调用 `Hub.DisconnectUser(userID)` 断开其全部 WS 连接（发送 `auth_error` 帧后 close）。
   - 恢复（`active`）只更新状态并失效缓存，无收敛动作。

### WebSocket

- `Hub` 维护 userID → connections 索引（或遍历现有连接表），新增 `DisconnectUser(userID)`。
- 连接时校验沿用现有 `authenticateToken` 检查点（替换为 DB 状态检查后自动生效）。
- 客户端 `packages/core/api/ws-client.ts`：识别 `auth_error` 帧与升级被拒（403），停止重连并触发会话终止流程（当前行为是无限重连、无用户可见信号）。

### 前端（web / desktop 共享 + mobile）

1. **会话终止流程**：
   - `packages/core/api/client.ts`：在现有 401 处理旁增加 403 + `code === "ACCOUNT_SUSPENDED"` 分支，同样走 `handleUnauthorized`（携带禁用原因标记）。
   - 补全 web/desktop 的 `onUnauthorized`（`core-provider.tsx`）：清 auth store 用户 + 清 token + 导航到登录页（参照 mobile `_layout.tsx` 的完整实现，含防循环的幂等保护）。
   - 登录页根据标记显示"账号已被禁用，请联系管理员"提示。
   - mobile 的独立 client（`apps/mobile/data/api.ts`）同步增加 `ACCOUNT_SUSPENDED` 分支。
2. **登录被拒**：验证码 / Google 登录接口返回 `ACCOUNT_SUSPENDED` 时，登录表单原地显示禁用提示（不进入通用错误文案）。
3. **管理页 `/admin`**：
   - 页面与组件放 `packages/views/admin/`；web（`apps/web/app/admin/`）与 desktop 路由各自接线。
   - 仅 `is_system_admin === true` 显示入口并允许访问；服务端仍是最终权限校验。
   - 功能：用户列表（姓名、邮箱、状态、加入时间），禁用/恢复操作带确认弹窗；被禁用用户有明显状态标识；自己那一行不显示禁用操作。
   - API 响应经 zod schema + `parseWithFallback` 解析；UI 对字段做防御性可选链；文案（中英）遵循 `apps/docs/content/docs/developers/conventions*.mdx`。

### 兼容性与安全细节

- 403 响应体带稳定 `code` 字段，旧客户端（不识别 code）表现为普通 403 报错，不会误登出——符合 API 兼容规则（不 pin 单一布尔、显式判断）。
- 已知残留窗口（与上游一致，文档化即可）：已签发的 CDN 签名 cookie / 预签名 URL 在 TTL 内仍有效。
- `ADMIN_EMAILS` 未配置时不存在系统管理员，admin API 全部 403，`/admin` 入口不可见——默认关闭。

## 测试计划

按仓库测试分层规则，行为性改动先写失败测试（TDD）：

- **Go（`server/`）**：
  - `UserMayAuthenticate` 单测：active 放行；suspended / 空 / 未知值拒绝（fail-closed）。
  - 集成测试：禁用后——验证码登录被拒、已有 JWT 下一请求 403 + `ACCOUNT_SUSPENDED`、PAT（含缓存命中路径）下一请求 403、daemon token 路径 403、`IssueCliToken` 被拒。
  - 缓存失效即时性：禁用后立即请求（缓存未过期）仍被拒。
  - Admin API：非管理员 403、管理员可禁用/恢复、不能禁用自己、`parseUUIDOrBadRequest` 边界。
  - `Hub.DisconnectUser`：禁用后连接被断开、新连接被拒。
- **TS**：
  - `packages/core`：admin API schema 解析 + malformed-response 测试；client 403 + `ACCOUNT_SUSPENDED` 触发会话终止；ws-client 收到 `auth_error` 停止重连。
  - `packages/views`：admin 页组件测试（列表渲染、确认弹窗、自己不可禁用、禁用态标识）。
  - `apps/web` / desktop：登出跳转接线测试（如有必要）。
- 验证命令：`make test`、`pnpm typecheck`、`pnpm --filter @multica/core test`、`pnpm --filter @multica/views test`。

## 明确不做（本期）

- 软删除（`deleted_at`）、邮箱唯一性调整（上游 v2 范围）。
- 禁用原因字段、审计日志。
- 已签发 CDN 签名凭证的即时吊销。
- workspace 级"仅移出成员"之外的新语义（现有 `revokeAndRemoveMember` 不变）。

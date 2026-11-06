# RIC-2321 — Multica daemon 单实例 / 锁 / 反复杀任务 取证记录

取证时间：2026-11-06（本机 macdeMac-Studio.local，uid 501）
原则：全程只读取证；**未重启 daemon**（重启会杀掉当时在跑的 6 个 run）。

---

## 一、工单前提复核：launchd KeepAlive + 孤儿占锁 → 反复杀任务

**结论：不成立。** 三件事被混成了一件。分开看：

| 现象 | 真实成因 | 证据 |
|---|---|---|
| 39199 行 `daemon profile "" is already running or starting` | auto-reload 自重启丢了启动参数 → 参数不全的孤儿占住锁 → launchd `KeepAlive=true` 每次被锁挡住 exit 1 → 立即重拉 | 见 §3 |
| 任务被批量判失败 | 服务端删除某 runtime 后，daemon 的 `runtime_gone` 恢复路径对其**全部** runtime 调 `RecoverOrphans`，连带杀掉健康的 Claude runtime 上的在跑任务 | 见 §4 |
| daemon 进程数异常 | 同上，auto-reload re-exec 而非 launchd 反复拉起 | 见 §3 |

---

## 二、工单点名的四项取证（原样输出）

### 1) 有几份 plist 在管同一个 daemon

```
$ launchctl list | grep -i multica
-	0	ai.openclaw.multica-project-guard
-	0	ai.openclaw.multica-feedback-bridge
-	0	ai.openclaw.multica-log-rotate
-	0	ai.openclaw.multica-daemon-watchdog
```

`com.multica.daemon` **不在 launchd 里**（2026-11-05 12:19 被 `bootout` 后未恢复）：

```
$ launchctl print gui/501/com.multica.daemon
Bad request.
Could not find service "com.multica.daemon" in domain for user gui: 501
```

但注册项仍 enabled，下次登录会自动加载：

```
$ launchctl print-disabled gui/501 | grep com.multica.daemon
		"com.multica.daemon" => enabled
```

磁盘上只有 1 份有效 plist（其余是历史备份）：

```
$ ls -la ~/Library/LaunchAgents/ | grep -i multica
ai.openclaw.multica-daemon-watchdog.plist
ai.openclaw.multica-feedback-bridge.plist
ai.openclaw.multica-log-rotate.plist
com.multica.daemon.plist
com.multica.daemon.plist.bak-activate-20261022-025145
com.multica.daemon.plist.bak-concurrency-20261031-161148
com.multica.daemon.plist.bak-restart-20261022-043530
com.multica.daemon.plist.bak-ric1813-20261021-153155
```

→ **只有 1 份 plist 管这个 daemon**，但**当前不受 launchd 管理**（孤儿进程）。

### 2) daemon 进程数

```
$ pgrep -fl 'multica daemon'
（无输出，exit=1）

$ ps -eo pid,ppid,args | grep 'multica daemon' | grep -v grep
87090     1 /Users/macbook/.local/bin/multica daemon start --foreground

$ ps -eo args | grep 'multica daemon' | grep -v grep | wc -l
       1
```

→ **有且仅有 1 个**，PPID=1（孤儿，非 launchd 托管）。

⚠️ **`pgrep -f` 在本机对某些进程是假阴性**，不能用于单实例校验。实测：

```
$ pgrep -f 'multica daemon'   → 空（退出码 1），但 ps 明明看得到 87090
$ pgrep -f . | grep -cx 87090 → 0
$ ps -A -o pid= | wc -l       → 1006
$ pgrep -f . | wc -l          → 999
```

缺失的固定是同一族进程（`/sbin/launchd` pid 1、87090 及其子进程 93176）。
**加不加 sandbox 结果一致**，所以不是本 agent 的沙箱造成的，是本机 `pgrep` 对这批进程的系统性盲区。
→ 结论：单实例校验一律用 `ps`（或 `multica daemon status --output json` 的 `pid` 字段），不要用 `pgrep -f`。

### 3) 锁的持有者

```
$ /usr/sbin/lsof ~/.multica/daemon.lock
COMMAND   PID    USER   FD   TYPE DEVICE SIZE/OFF      NODE NAME
multica 87090 macbook    5u   REG   1,16        0 516986107 /Users/macbook/.multica/daemon.lock
```

三方一致：

```
$ cat ~/.multica/daemon.pid
87090
$ multica daemon status --output json | jq .pid
87090
```

→ 锁持有者 = 87090，**没有第二个候选**。

### 4) `restarted while task was in flight` 的复现频率

```
$ grep -c 'restarted while task was in flight' ~/.multica/daemon.log
0

$ for f in ~/.multica/daemon-*.log.gz; do gzcat "$f" | grep -c 'restarted while task was in flight'; done
daemon-2026-10-18T05-00-33.241.log.gz -> 0
daemon-2026-10-21T05-03-46.996.log.gz -> 0
daemon-2026-11-01T04-47-55.722.log.gz -> 0
daemon-2026-11-03T07-02-09.253.log.gz -> 0
daemon-2026-11-05T13-39-20.107.log.gz -> 0
```

→ **全部日志 0 条**。这句话只存在于 DB 的 `error` 字段，是服务端 `runtime_recovery` 的固定文案
（`server/pkg/db/generated/agent.sql.go` 里 `RecoverOrphanedTasksForRuntime` 的 SET 子句）。
**用日志判断「daemon 是否重启」从方向上是错的。**

DB 里的真实计数：

```sql
select failure_reason, count(*) from agent_task_queue
 where failure_reason='runtime_recovery' group by 1;
 runtime_recovery | 18
```

按分钟分布，只有两个簇：

```
2026-11-05 02:58:00+00 | 6
2026-11-05 03:20:00+00 | 6
```

其余 6 条散落在 8~9 月。

---

## 三、KeepAlive 配置原文 + auto-reload 丢参（孤儿与 39199 行的真因）

修改前 plist（备份 `com.multica.daemon.plist.before-20261106-123312`）关键字段：

```xml
<key>KeepAlive</key>
<true/>
```

`launchd.err.log` 修改前：`39199 行 / 1920751 字节`，`sort | uniq -c` 全是同一句
`daemon profile "" is already running or starting`，mtime 停在 `2026-11-05 12:19:14`（bootout 那刻）。

**孤儿的产生链路**（02:09 UTC 即 03:09 本地）：

```
03:09:55.603 INF auto-reload: restarting into the binary on disk
             reason="multica binary on disk reports 0.8.0, running 0.7.1"
             binary=/Users/macbook/.local/bin/multica
03:09:55.614 INF restarting daemon with updated binary
03:09:55.644 INF starting daemon component=daemon version=0.8.0
03:09:55.647 DBG daemon config resolved ... max_concurrent_tasks=20 ...
```

re-exec 后的进程 argv 只剩裸参数：

```
87090  1  /Users/macbook/.local/bin/multica daemon start --foreground
```

plist 里的 `--no-auto-update --max-concurrent-tasks 10` 全部丢失 → 落到二进制默认 **20**。
binary 路径也从 `/usr/local/bin/multica` 漂到 `~/.local/bin/multica`（两份 sha256 相同：
`4a3510930068312bff3487dbb9e35d6cdf99b5d738f126c84a31768cdf32f77f`）。

之后 launchd 想按 plist 拉 `/usr/local/bin/multica ... --max-concurrent-tasks 10` 时锁已被占 →
`already running or starting` → exit 1 → `KeepAlive true` 立即重拉 → 39199 次。
**这是孤儿产生后的放大器，不是任务被杀的原因。**

### 生效并发长期漂移（本轮新发现）

把全部归档 + 当前日志里的 `daemon config resolved` 汇总：

```
daemon-2026-10-21T05-03-46.996.log.gz -> max_concurrent_tasks=4
daemon-2026-11-01T04-47-55.722.log.gz -> max_concurrent_tasks=20
daemon-2026-11-05T13-39-20.107.log.gz -> max_concurrent_tasks=20
```

| 来源 | 值 |
|---|---|
| plist `--max-concurrent-tasks` | 10 |
| 当前运行进程（argv 裸参数，二进制默认） | **20** |

→ 从 11-01 起 daemon 实际按 20 跑，而 plist 写的是 10。这解释了告警规则
`multica_runs_active > 10` 会把「正常跑 11~20 个」误判成饱和。

---

## 四、任务被批量判死的真因（与工单、也与上一轮结论都不同）

`RecoverOrphans` 会把指定 runtime 上所有 `dispatched/running/waiting_local_directory` 任务判失败，
文案固定为 `daemon restarted while task was in flight` + `failure_reason=runtime_recovery`。

两次事故的时序（daemon 日志原始行）：

```
# 事件 A
10:58:02.075 INF runtime deleted server-side; pruned from local state
             runtime_id=19757968-79c9-403b-95d2-f50ecfc13152   ← antigravity
10:58:02.659 DBG register response ... runtimes=6
10:58:02.659 INF re-registered runtime after server-side deletion runtime_id=723f0520... ← claude
10:58:02.663 DB 里 6 个任务被判 failed

# 事件 B
11:20:58.452 INF runtime deleted server-side; pruned from local state
             runtime_id=912004b6-1a96-4f80-adad-fc39a0599197   ← antigravity(新 id)
11:20:59.166 DBG register response ... runtimes=5
11:20:59.167 INF re-registered runtime after server-side deletion runtime_id=723f0520... ← claude
11:20:59.170 DB 里另 6 个任务被判 failed
```

关键点：**被服务端删掉的是 antigravity，被连带杀任务的是 claude。**
12 条被判死的任务全部挂在 `723f0520`：

```sql
select left(t.runtime_id::text,8), r.provider, t.completed_at
  from agent_task_queue t left join agent_runtime r on r.id=t.runtime_id
 where t.failure_reason='runtime_recovery' and t.completed_at > '2026-11-05';
 723f0520 | claude | 2026-11-05 02:58:02.663237+00   (×6)
 723f0520 | claude | 2026-11-05 03:20:59.170803+00   (×6)
```

代码路径：

```
server/internal/daemon/daemon.go:1933  reregisterWorkspaceAfterRuntimeGone
  ├─ applyRegisterResponseInPlace → newIDs = 响应里的【全部】runtime
  ├─ deregisterDroppedRuntimes    → 只处理真正掉的
  └─ for _, rid := range newIDs { d.client.RecoverOrphans(ctx, rid) }   ← 对幸存者也调了
```

注释自己写明了本意是「recover the previous (now-deleted) runtime」，
但循环变量用的是 `newIDs`（本次注册返回的全集），不是「被删的那个 id」。
于是任何一个 runtime 被服务端删除，都会把**同一 workspace 下所有 runtime 的在跑任务**判死。
这正是「任务反复被杀」的真因——与 launchd、与重复 daemon_id 注册都无关。

**为什么 antigravity 会被删**：`~/.local/bin/agy` 已不存在，
daemon 每次探测失败 `skip registering runtime`，服务端侧该行随后被回收：

```
$ ls -la /Users/macbook/.local/bin/agy
ls: no such file or directory
$ multica daemon status --output json | jq .skipped_agents
{"antigravity": "version detection failed: detect version for /Users/macbook/.local/bin/agy: ..."}
```

---

## 五、改了什么（改前值 → 改后值）

| 文件 | 项 | 改前 | 改后 | 理由 |
|---|---|---|---|---|
| `~/.multica/config.json` | `max_concurrent_tasks` | 不存在 | `10` | 钉住生效并发，使 re-exec 丢 flag 时仍读得到（优先级：flag > env > 此字段 > 默认20） |
| `~/.multica/config.json` | `disable_auto_reload` | 不存在 | `true` | 从 CLI 配置侧再堵一次 auto-reload 丢参，与 plist 的 `--no-auto-reload` 双保险 |
| `~/Library/LaunchAgents/com.multica.daemon.plist` | `ProgramArguments` | 无 `--no-auto-reload` | 已含（上一轮改） | 堵 orphan 源头 |
| 同上 | `MULTICA_DAEMON_MAX_CONCURRENT_TASKS` | `4` | 已删（上一轮改） | 与 flag 双写冲突 |
| 同上 | `ThrottleInterval` | 不存在 | `60`（上一轮改） | 把重试风暴压到 60s/次 |
| 同上 | `KeepAlive` | `<true/>` | **保持不变** | 它本身是对的 |

备份：`~/.multica/config.json.bak-ric2321-20261106-170227`

选择改 config.json 而不是改 plist：**它不需要重启 daemon 就能生效**（下次任何启动/重载都会读），
而 plist 需 bootout/bootstrap，会杀掉在跑的 6 个 run —— 违反本轮「不要重启 daemon」的约束。

告警规则 `config/prometheus/rules/multica.rules.yml:57` `expr: multica_runs_active > 10`
**不需要改**：它对齐的正是 plist 的 10，现在 config.json 也钉成 10，三者一致。
漂移的根源是运行中进程用了 20，改的是 daemon 不是规则。

---

## 六、改后复测（同一组命令）

| 检查项 | 改前 | 改后 |
|---|---|---|
| daemon 进程数（`ps`） | 1（87090） | **1（87090，未变）** |
| 锁持有者（`lsof`） | `multica 87090` | **`multica 87090`（未变）** |
| `com.multica.daemon` in launchd | 不在 | **仍不在（有意保持）** |
| `pgrep -fl 'multica daemon'` | 空（假阴性） | 空（**仍假阴性**） |
| `grep -c 'restarted while task was in flight'` | 0 | 0 |
| `launchd.err.log` | 0 B | **0 B**，60 秒增量 **0 B** |
| `plutil -lint` | OK | **OK** |
| daemon pid / uptime | 87090 | **87090 / 连续增长，未重启** |

改后 `~/.multica/config.json`：

```json
{
  "server_url": "http://localhost:8080",
  "workspace_id": "03de4570-c1ae-4530-bb55-0f35ef4d1689",
  "token": "<redacted>",
  "max_concurrent_tasks": 10,
  "disable_auto_reload": true
}
```

---

## 七、没做 / 待决策

1. **`com.multica.daemon` 仍是 bootout 状态**（自 11-05 12:19）。`RunAtLoad` + `enabled` 会在下次登录
   自动加载**已修好的** plist。要现在恢复必须停掉 87090，会杀掉在跑的 run，需另行择机。
2. **`RecoverOrphans` 的越界循环（§四）是代码缺陷，未改。**
   本 worktree 基线是 `v0.5.3`，而这台机器跑的是 0.8.0（commit `9b1ab4f9…`，本仓库没有该对象），
   在这个分支上改 `daemon.go` 无法用实际运行的二进制验证。建议单开一单在 0.8.x 上修：
   `newIDs` → 只传被删的那个 runtime id。
3. **`pgrep -f` 盲区**建议同步给所有看门狗作者：`pgrep -f 'multica daemon'` 恒空，
   写成「为空就重启」会变成无限重启循环。当前 `multica_daemon_watchdog.py` 用的是
   `multica daemon status` + `launchctl print`，没有踩这个坑。

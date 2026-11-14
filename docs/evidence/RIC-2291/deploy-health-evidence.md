# RIC-2291 — deploy / health evidence（2026-11-14 补录，回应门禁 missing:deploy_or_health_evidence）

门禁把本单归为 `deploy` 类，缺「部署/健康证据」。本单**不是 Prometheus 规则部署**，
"部署"的是 Multica autopilot 的线上 trigger 配置（`0 */4 * * *` → `0 8 * * *`），
其"健康"= 该配置在现网生效 + 宿主后端健康 + 9 天实际建单节奏。以下全部为原文命令与输出。

## 1. 部署证据：线上 trigger 已降到每日 1 次且在位

```
$ multica autopilot trigger-list 9224cdf7-6b02-4131-950c-b3b63e74c0ac --output json
{
  "total": 1,
  "triggers": [
    {
      "autopilot_id": "9224cdf7-6b02-4131-950c-b3b63e74c0ac",
      "cron_expression": "0 8 * * *",
      "enabled": true,
      "id": "b6da88d6-6ade-4e2d-bd6d-51720fc02449",
      "kind": "schedule",
      "label": "每日08:00合并+部署",
      "last_fired_at": "2026-11-13T00:00:28Z",
      "next_run_at": "2026-11-14T00:00:00Z",
      "timezone": "Asia/Shanghai"
    }
  ]
}
```

`cron_expression` = `0 8 * * *`（改前 `0 */4 * * *`），`next_run_at` 已按新 cron 重算到
`2026-11-14T00:00:00Z`（= 次日 08:00 +08）。配置已部署生效。

## 2. 健康证据：宿主后端 200 + 9 天建单节奏稳定

```
$ curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/healthz
200
```

```
$ multica autopilot runs 9224cdf7-6b02-4131-950c-b3b63e74c0ac --output json   # 按日聚合 schedule run
2026-11-05  1
2026-11-06  1
2026-11-07  1
2026-11-08  1
2026-11-09  1
2026-11-10  1
2026-11-11  1
2026-11-12  1
2026-11-13  1
```

连续 9 天每天恰好 1 条 schedule run（改前 11-04 当天 6 条：3 completed + 2 cancelled + 1）。
部署后的行为健康且稳定，无重复建单、无堆积。

## 3. 代码修复在本分支

```
$ git log --oneline --grep "RIC-2291 fix(autopilot)" -1
0c16c9f814 RIC-2291 fix(autopilot): add per-day issue dedup policy
```

`issue_dedup_policy=per_day`（迁移 907）已提交在本分支，需发版才落到现网；线上目前
靠 trigger 降频兜底。

## 4. 分类说明（option 3）

门禁示例 `promtool test rules` / `reload ok` 属于监控规则部署类。本单实际是
**平台侧 autopilot 配置变更**：部署证据=trigger cron 现值 + next_run 已重算，
健康证据=宿主后端 200 + 9 天 1/日实际节奏。如门禁的分类表把本单按监控规则类
要 promtool 证据，属误分类，可按上述实际类别修正，避免下一轮再误判。

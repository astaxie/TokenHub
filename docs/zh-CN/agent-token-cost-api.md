# Agent Token 成本 API

Language: [English](../agent-token-cost-api.md) | 简体中文 | [日本語](../ja/agent-token-cost-api.md)

版本化的 Agent Token 成本 API 让本地报表或监控 Agent 无需管理员会话、模型调用 API Key、Provider 凭证或人工导出，即可只读访问 TokenHub 用量。接口与管理员用量页面使用相同的请求、Token、错误数和预估客户成本口径。

## 接口

| 接口 | 认证 | 用途 |
| --- | --- | --- |
| `GET /api/v1/analytics/token-costs` | 分析凭证 | 以 JSON 或 CSV 查询请求级或聚合后的 Token 成本 |
| `GET /api/admin/analytics/credentials` | 平台管理员 | 列出分析凭证元数据 |
| `POST /api/admin/analytics/credentials` | 平台管理员 | 创建分析凭证，并仅显示一次 Token |
| `DELETE /api/admin/analytics/credentials/{id}` | 平台管理员 | 立即吊销分析凭证 |

分析凭证以 `tha_` 开头，不能认证 `/v1/models`、模型推理接口或管理员接口。

## 创建最小权限凭证

使用管理员会话或配置的管理员 Token 创建项目级凭证：

```bash
curl -sS https://tokenhub.example.com/api/admin/analytics/credentials \
  -H "Authorization: Bearer $TOKENHUB_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "payments-cost-agent",
    "scope_type": "project",
    "project_id": "prj_payments",
    "expires_at": "2026-12-31T00:00:00Z"
  }'
```

响应包含 `credential` 元数据和 `token`。请立即复制 Token；以后列表只显示前缀和后缀。只有在 Agent 确实需要读取整个 TokenHub 实例时，才将 `scope_type` 设为 `organization` 并省略 `project_id`。过期时间可选，但建议设置。

将 Token 保存到本地 Agent 的密钥存储：

```bash
export TOKENHUB_ANALYTICS_TOKEN='tha_REPLACE_ME'
```

Agent 停用或 Token 可能泄露时立即吊销：

```bash
curl -sS -X DELETE \
  https://tokenhub.example.com/api/admin/analytics/credentials/acred_REPLACE_ME \
  -H "Authorization: Bearer $TOKENHUB_ADMIN_TOKEN"
```

## 查询请求级成本

`from` 包含边界，`to` 不包含边界，均使用 RFC 3339。省略时查询从执行开始时刻向前 24 小时的数据。

```bash
curl -sS -G https://tokenhub.example.com/api/v1/analytics/token-costs \
  -H "Authorization: Bearer $TOKENHUB_ANALYTICS_TOKEN" \
  --data-urlencode 'from=2026-08-01T00:00:00Z' \
  --data-urlencode 'to=2026-08-02T00:00:00Z' \
  --data-urlencode 'provider_id=prv_openai' \
  --data-urlencode 'model=gpt-4.1-mini' \
  --data-urlencode 'status=success' \
  --data-urlencode 'limit=100'
```

默认 `granularity=request`，每个网关请求返回一条脱敏记录。记录包含稳定 ID 和指标，但绝不包含提交的分析 Token、API Key Secret、Provider 凭证、Provider 成本、客户端 IP、请求/响应 Payload 或 User-Agent。

## 过滤与聚合

以下示例按项目、Provider、模型和成功/错误状态生成每日汇总：

```bash
curl -sS -G https://tokenhub.example.com/api/v1/analytics/token-costs \
  -H "Authorization: Bearer $TOKENHUB_ANALYTICS_TOKEN" \
  --data-urlencode 'from=2026-07-01T00:00:00Z' \
  --data-urlencode 'to=2026-08-01T00:00:00Z' \
  --data-urlencode 'granularity=day' \
  --data-urlencode 'group_by=project,provider,model,status'
```

时间粒度可用 `hour`、`day`、`month`；`granularity=none` 表示不按时间分桶。只传 `group_by` 时也会选择 `none`。`request` 不能和 `group_by` 同时使用。

| 参数 | 可选值和行为 |
| --- | --- |
| `from`、`to` | RFC 3339 的 `[from, to)` 区间；默认最近 24 小时 |
| `project_id` | 精确项目 ID；项目级凭证始终被限制到自己的项目，请求其他项目返回 `403` |
| `user_id` | 精确的归因用户 ID |
| `api_key_id` | 已保存的 API Key ID，不是 API Key Secret |
| `provider_id` | 精确 Provider ID |
| `model` | 精确外部模型名 |
| `status` | HTTP 状态小于 400 时为 `success`，400 及以上为 `error` |
| `granularity` | `request`（默认）、`none`、`hour`、`day` 或 `month` |
| `group_by` | 逗号分隔或重复传入 `project`、`user`、`api_key`、`provider`、`model`、`status` |
| `limit` | 1–1000 行，默认 100 |
| `cursor` | 上一页返回的不透明 `next_cursor` |
| `after` | 用于按提交序号增量拉取的已提交不透明 `watermark`；不能与 `from` 或 `cursor` 同时使用 |
| `format` | `json`（默认）或 `csv`；`Accept: text/csv` 也会选择 CSV |

初始请求级快照的时间范围最多 31 天，初始聚合快照最多 366 天。增量变更拉取由提交序号限定，因此可以保留原始 `from` 并持续推进 `to`，且不会重新扫描原始历史。

## JSON Schema

每个 JSON 响应都声明 `schema_version: "1.0"`，结构如下：

```json
{
  "schema_version": "1.0",
  "object": "token_cost.list",
  "generated_at": "2026-08-02T00:00:01Z",
  "query": {
    "from": "2026-08-01T00:00:00Z",
    "to": "2026-08-02T00:00:00Z",
    "granularity": "day",
    "group_by": ["project", "model"],
    "filters": {"project_id": "prj_payments"},
    "format": "json",
    "limit": 100,
    "dedupe_by": "dedupe_key",
    "checkpoint_by": "commit_sequence",
    "incremental_mode": "snapshot"
  },
  "data": [
    {
      "dedupe_key": "aggregate_f6d6...",
      "bucket": "2026-08-01",
      "project_id": "prj_payments",
      "model": "gpt-4.1-mini",
      "metrics": {
        "request_count": 42,
        "error_count": 2,
        "input_tokens": 120000,
        "cached_input_tokens": 35000,
        "cache_write_input_tokens": 4000,
        "output_tokens": 18000,
        "reasoning_output_tokens": 2500,
        "total_tokens": 138000,
        "estimated_cost_usd": 1.73
      }
    }
  ],
  "has_more": false,
  "watermark": "OPAQUE_WATERMARK"
}
```

`request_count` 和 `error_count` 来自网关请求日志，因此包含没有用量记录的失败请求。Token 与成本来自用量记录。缓存和推理 Token 已包含在输入/输出总量中，不要把所有明细字段再次加到 `total_tokens`。`estimated_cost_usd` 是管理员用量页面使用的外部客户计费估算，不是 TokenHub 保密的 Provider 成本。

## 分页与增量拉取

当 `has_more` 为 true 时，用 `cursor=next_cursor` 再次调用。Cursor 会保存原始过滤条件、`granularity`、`group_by` 和快照区间，因此这些参数都可以省略；如果再次传入，则必须与 Cursor 一致。快照上界保持固定，因此分页期间新到达的请求不会挤动后续页面。

响应中的 `watermark` 标识已完成的数据库快照。在 PostgreSQL 上，新请求日志使用事务 ID 与持久化偏移量的组合值；升级时则会为冻结的历史记录分配按事件时间排列且互不相同的检查点值。历史重排使用 MVCC，不会获取阻止新请求日志插入的表锁。watermark 不会超过当前可见请求日志的最大序号；执行 `pg_restore` 后，启动迁移会把持久化偏移量推进到恢复后的最大值之上，因此 Agent 已保存的 watermark 在新集群中仍然有效。检查点还会停在快照中最早的活跃事务之前，所以后续事务提交不需要等待共享的分析行锁。SQLite 则在数据库原有的单写者模型内使用事务序号。检查点会停在第一条 `occurred_at` 大于等于 `to` 的已提交匹配请求之前，因此后续推进 `to` 时不会跳过已经提交的未来事件。即使快照没有匹配行也会返回 watermark。Agent 必须先处理所有页面，成功后再把 watermark 持久化。下一轮使用 `after=<已提交 watermark>`：

```bash
curl -sS -G https://tokenhub.example.com/api/v1/analytics/token-costs \
  -H "Authorization: Bearer $TOKENHUB_ANALYTICS_TOKEN" \
  --data-urlencode "after=$TOKENHUB_COST_WATERMARK"
```

使用 `after` 时，`query.incremental_mode` 为 `changes`，只返回提交序号大于已提交 watermark 且不超过新快照的请求日志。原始过滤条件和事件时间 `from` 会保留，`to` 可以持续推进。因此，即使延迟提交请求的 `occurred_at` 早于上一次快照中的最新事件，也不会被跳过。

请求粒度变更的 `dedupe_key` 等于 `request_id`。聚合变更行是新增提交请求形成的 Delta，其 Key 标识已提交的起始检查点、查询形状、Bucket 和维度。Agent 应先按该 Key 暂存或 Upsert 所有页面，完整拉取后只应用一次 Delta，并原子推进 watermark。用同一已提交 watermark 重试时，即使新快照继续推进，Key 仍保持稳定并覆盖暂存值。复用 watermark 时修改过滤条件或聚合形状会被拒绝；不同形状的报表应从新的 `from`、`to` 快照开始。

## CSV 导出

```bash
curl -sS -G https://tokenhub.example.com/api/v1/analytics/token-costs \
  -H "Authorization: Bearer $TOKENHUB_ANALYTICS_TOKEN" \
  -H 'Accept: text/csv' \
  --data-urlencode 'from=2026-08-01T00:00:00Z' \
  --data-urlencode 'to=2026-08-02T00:00:00Z' \
  --data-urlencode 'granularity=hour' \
  --data-urlencode 'group_by=project,provider,model' \
  -o token-costs.csv
```

CSV 使用与 JSON 相同的过滤、限制、指标、分页和 `dedupe_key`。元数据通过 `X-TokenHub-Schema-Version`、`X-TokenHub-Has-More`、`X-TokenHub-Next-Cursor`、`X-TokenHub-Watermark`、`X-TokenHub-Dedupe-By`、`X-TokenHub-Checkpoint-By` 和 `X-TokenHub-Incremental-Mode` 响应头返回。可能被电子表格解释为公式的文本单元格会添加单引号前缀。

## CLI 与 MCP 评估

| 选项 | 决策 | 原因 |
| --- | --- | --- |
| 版本化 HTTP + CSV | 当前支持 | 可用于任意语言、Cron 或 Agent Runtime，无需额外分发二进制，并让认证与检查点保持显式 |
| 专用 CLI | 暂缓 | CLI 主要只是包装 HTTP，却增加安装、升级和本地 Secret 配置；出现交互式凭证配置或定时报表打包需求时再评估 |
| MCP Server/Tool | 暂缓 | MCP 会增加长期运行的信任边界和 Host 专属部署；当 Agent Host 需要工具发现或共享检查点管理，而不是直接 HTTP 时再评估 |

未来任何 CLI 或 MCP 适配器都只能接收分析凭证，必须保留 Cursor/watermark 语义并暴露相同 Schema 版本，绝不能要求管理员会话或模型调用 API Key。

## 安全与运维

- 只有平台管理员可以创建、列出或吊销分析凭证。
- 每次成功查询、范围越权、非法查询和无效凭证尝试都会写入类型为 `token_cost_analytics` 的审计事件。
- 查询响应和审计快照不会包含分析凭证或其 Hash。请把只显示一次的 Token 当作 Secret 保存。
- 分析读取使用与网关核心连接池分离的小型专用连接池。文件型 SQLite 会启用 WAL，避免读取阻塞网关写入；PostgreSQL 使用独立分析连接池。除 `created_at`、`(project_id, created_at)` 索引、时间范围和页面大小限制外，每次查询还有 10 秒执行期限。
- 吊销从下一次请求立即生效。轮换时先创建新凭证、更新 Agent，再吊销旧凭证。
- 不要并发发起无界历史查询，应使用分页。超时会返回 `503 analytics_query_timeout`；重试前应缩短时间范围或减少聚合维度基数。

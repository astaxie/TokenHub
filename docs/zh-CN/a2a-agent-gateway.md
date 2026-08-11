# A2A 1.0 Agent 网关

Language: [English](../a2a-agent-gateway.md) | 简体中文 | [日本語](../ja/a2a-agent-gateway.md)

TokenHub 可将审核后的上游 Agent 通过 A2A 1.0 JSON-RPC 网关开放，并支持 SSE 流式返回。该接口不接受 A2A 0.3，也不提供 HTTP+JSON 或 gRPC 绑定。

## 启用与回退

设置 `TOKENHUB_A2A_ENABLED=true` 后重启后端。默认值为 `false`；改回 `false` 会关闭公开 Agent Card、A2A 网关、MCP 准入接口和 `agent/<slug>` Responses 桥，但不会删除注册表或任务数据。

生产环境的上游必须使用 HTTPS，且域名不得解析到回环、链路本地或私网地址。`TOKENHUB_A2A_ALLOW_PRIVATE_UPSTREAMS=true` 仅用于受控的本地开发环境。

## 注册并审核 Agent

在管理控制台打开「Agent 网关」，填写小写标识和 Agent Card 地址。TokenHub 会获取并校验 Card，要求其中提供 A2A 1.0 `JSONRPC` 接口，移除公开 Card 中的上游安全声明，加密静态请求头，并创建不可变版本。数据库托管的历史版本可在同一页面回滚；从 `data/agent-catalog.yaml` 同步的条目在控制台中只读。

也可调用 `POST /api/admin/agents` 注册。请求可使用 `card_url`，也可直接提供 `card`；`upstream_url` 可覆盖审核后的 JSON-RPC 地址。不得把凭据提交到 `data/agent-catalog.yaml`。该文件中的标识必须唯一。配置条目会接管数据库中同名的 Agent、停用原有实例，并在控制台中变为只读；管理员不能覆盖配置托管的 Agent。

```yaml
agents:
  - slug: research
    card_url: https://research.example/.well-known/agent-card.json
    status: active
    max_concurrency: 8
    allowed_forward_headers: [X-Request-ID, traceparent]
```

## 访问控制

Agent 调用默认拒绝。需要通过控制台或 `POST /api/admin/agent-access-bindings` 创建至少一条启用的允许规则。规则可限定 `global`、`team`、`project`、`api_key`、`end_user`、`agent` 或 `access_group`；只要存在匹配的启用拒绝规则，就会覆盖允许规则。规则还可限定 Agent Card 中的技能 ID。

仅当 API Key 元数据包含 `allow_end_user_identity=true` 时，才接受 `X-TokenHub-End-User-ID`，避免不可信调用方自行指定其他终端用户身份。

## A2A 调用

每个 JSON-RPC 请求都必须携带 `A2A-Version: 1.0` 和 TokenHub API Key。发现 Agent Card 时也需要 API Key，且只返回有权使用的技能。协议版本缺失或不同会返回 A2A 错误 `VERSION_NOT_SUPPORTED`；未授权的发现请求与未知 Agent 返回相同的未找到响应。

```bash
curl "$TOKENHUB_URL/a2a/research" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H "A2A-Version: 1.0" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc":"2.0",
    "id":"request-1",
    "method":"SendMessage",
    "params":{"message":{"messageId":"message-1","role":"ROLE_USER","parts":[{"text":"汇总本次发布"}]}}
  }'
```

网关支持 `SendMessage`、`SendStreamingMessage`、`GetTask`、`ListTasks`、`CancelTask` 和 `SubscribeToTask`，不支持推送通知方法。公开 Card 地址为 `/a2a/<slug>/.well-known/agent-card.json`，也可使用 `/.well-known/agent-card.json?agent=<slug>`。

TokenHub 会将上游任务 ID 替换为网关任务 ID，并把任务永久绑定到首次选中的实例和 API Key。后续消息、查询、取消和订阅都使用同一实例。可能已经到达上游的消息不会自动改投其他实例。

配置的静态上游请求头会覆盖客户端同名值。只有名称出现在 `allowed_forward_headers` 中的客户端请求头才会转发；认证凭据、Cookie、逐跳请求头、`A2A-Version` 和 TokenHub 委托请求头始终禁止加入该列表。静态凭据可通过管理 API 或密钥渲染后的部署配置提供，但不得提交到纳入版本控制的目录。

每次上游调用都会收到有效期 5 分钟的 `X-TokenHub-Delegation-Token`。完成接入的 Agent 可将它作为 Bearer Token 调用 TokenHub 模型或 Agent API。签名身份包含项目、Key、终端用户、调用方 Agent、执行、父步骤、深度、Agent 序列和过期时间；Agent ID 重复时按循环调用拒绝。现有「Provider 调用前」内容安全策略也会在请求到达上游前检查直接 A2A 文本片段和 Responses 桥输入。

## Responses 桥

仅实现 OpenAI Responses API 的应用可使用模型名 `agent/<slug>` 调用 Agent：

```json
{"model":"agent/research","input":"汇总本次发布","stream":true}
```

TokenHub 会把输入转换为 A2A 用户消息，再把 Agent 文本、状态消息和文本产物转换为 Responses 输出。其他 `/v1/responses` 请求保持原有行为。

## 运行治理与 MCP 计量

每次根调用都会创建保存在数据库中的执行记录和步骤图。默认限制 Agent 跳数、模型调用数、已接入计量的 MCP 调用数、运行时间、Token、成本和 Agent 步骤并发数。具体的 `TOKENHUB_A2A_MAX_*` 变量见部署文档。

完成接入的 Agent 应在执行 MCP 调用前提交准入，结束后报告实际用量：

```bash
curl "$TOKENHUB_URL/api/a2a/executions/mcp" \
  -H "Authorization: Bearer $TOKENHUB_DELEGATION_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"phase":"admit","step_id":"mcp-step-1"}'
```

完成时使用相同 `step_id` 再提交一次，将 `phase` 设为 `complete`，并提供非负的 `tokens` 和 `cost_usd`。TokenHub 无法统计未接入该协议、完全在上游 Agent 内部执行的不透明 MCP 调用；必须强制执行 MCP 限制时，运维规范应要求上游完成该准入接入。

注册版本、实例健康状态、任务快照与事件、执行步骤、模型与 MCP 计数、Token 和成本都会持久化到 SQLite 或 PostgreSQL。管理 API 不会返回静态凭据或委托令牌签名材料。

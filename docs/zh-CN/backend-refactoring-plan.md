# 后端重构规划

本文记录将 `backend/internal/server` 逐步迁移为更小的领域、传输和持久化模块的计划，
用于 issue #169 的后续实施。架构文档只描述当前运行时架构。

## 规划原则

- 每个阶段都保留现有 HTTP、认证、授权、持久化和事务行为。
- 持久化端口由使用它的领域或应用包定义。GORM 行模型、HTTP DTO 和数据库职责保留在适配器中。
- 明确依赖方向。领域包不得导入 `server`、HTTP handler 或 GORM。
- 开始下一阶段前，记录当前阶段的依赖变化、兼容接口、剩余工作和验证证据。

## 迁移状态

以下总览按阶段说明依赖变化、状态、退出条件和剩余工作，不包含内部任务编号。

| 阶段 | 依赖变化 | 状态 | 退出条件 | 剩余工作 |
| --- | --- | --- | --- | --- |
| 0. 基线与约束 | 新增包依赖前冻结可观察行为；目标目录稳定后再加入依赖检查 | 已完成，依赖检查延期 | 行为刻画矩阵已完成，依赖检查设计可通过独立 PR 实施 | 最终目录稳定后加入检查；此前不将其描述为已启用的仓库门禁 |
| 1. 业务模块 | 将 billing 和 reconciliation 调用方从 `server.Store` 迁移到消费者定义的端口，再引入 analytics 端口和适配器 | 进行中 | billing、reconciliation 和 analytics 的生产路径使用公开接口；`server` 不再包含这些业务实现 | 完成 analytics 提取，移除过时的 server 投影并迁移剩余测试 |
| 2. Gateway、协议与 Provider | 将 `server` 从协议和 Provider 实现转为 `gateway`、`protocol/*` 和 `provider` 的依赖组装 | 已规划 | OpenAI、Anthropic、Gemini 协议适配器和 Provider 调用都通过统一网关契约运行 | 定义契约，再迁移 Provider 管理、调用、协议适配器和网关错误映射 |
| 3. Project 与 Routing | 将认证、授权、配额准入、候选规划和路由策略迁入 `project` 与 `routing` 端口 | 已规划 | 网关不再通过全局 `Store` 处理身份、准入或候选规划 | 分离 Project 和 API Key 状态、配额语义、路由策略和故障回退规划，保持准入顺序 |
| 4. 请求生命周期与后台能力 | 将请求记录、用量、审计、载荷保留、图像任务和可观测性迁入 `usage`、`image` 和 `observability` 模块 | 已规划 | `Server` 不再拥有请求或 worker 的领域状态；生命周期和恢复行为不变 | 先提取用量写入和审计语义，再迁移网关编排、图像 worker 和遥测适配器 |
| 5. 适配器与依赖组装收尾 | 整理 HTTP 适配器和持久化实现，使 `server` 只负责依赖组装、路由请求和进程生命周期 | 已规划 | 兼容构造器、过时 Store 投影和迁移 shim 已移除；最终依赖图由检查强制执行 | 迁移剩余 admin 和持久化适配器，加入依赖检查，并按 issue #169 验证完整架构 |

各阶段有先后依赖，不能视为互不相关的并行功能。阶段 1 完成 analytics 边界后，
阶段 2 才改变请求路径；阶段 3 依赖网关契约；阶段 4 依赖 Project、Routing 和 Usage
接口边界；阶段 5 在所有生产调用方迁移后才删除 shim。

## 阶段 1 已完成的工作

### Billing

此前的 PR 已提取 billing 领域及其适配器。领域拥有同步、重试、游标和调度行为。
剩余 server 代码为尚未迁移到公开 billing 接口的调用方提供依赖组装入口。

### Reconciliation 适配器

当前阶段将对账持久化和管理 HTTP 处理移出 `server`。

迁移后的依赖方向为：

```text
server -> admin -> reconciliation
server -> reconciliation/persistence -> reconciliation
```

reconciliation 领域拥有 `Store` 和 `BillingReader` 端口。`admin` 包负责请求、响应、CSV
和审计的传输映射。`reconciliation/persistence` 负责 SQLite 和 PostgreSQL 的 GORM 行模型、
事务、成本证据及账单投影适配。依赖组装入口仍负责打开数据库、提供共享持久化互斥锁和
审计回调，以及执行路由授权。

兼容契约包括现有 HTTP 响应和错误、表名和数据库迁移、归属快照、显式零成本证据、分页、
CSV 转义、锁定、重算和审计行为。PostgreSQL CI job 执行带标签的生产适配器契约测试，
验证并发快照回填。

本阶段剩余工作是将其余 server 测试迁往公开模块边界，并在调用方迁移后移除兼容构造器
和测试投影。

## 下一阶段：Analytics

下一阶段分为两个可独立审查的步骤：

1. 提取 analytics 查询和应用模块，包括 analytics 凭证、游标和快照语义、聚合，以及消费者定义的持久化端口。
2. 迁移 SQLite/PostgreSQL 查询适配器和管理 HTTP 适配器，再删除 server 中的 analytics 实现，保持聚合、授权和一致性行为。

退出条件是 analytics 的生产路径使用新公开接口，适配器在 SQLite 和 PostgreSQL 上都可运行，
并且生产 analytics 调用方不再依赖全局 server `Store`。

analytics 的依赖变化为：

```text
server -> admin -> analytics
server -> analytics/persistence -> analytics
analytics -> usage (read-only usage projections)
```

本阶段必须保留凭证作用域、游标和水位语义、快照一致性、聚合边界、授权、分页和查询限制。
只有 SQLite 和 PostgreSQL 两条路径都有针对性测试，且现有 HTTP 响应保持兼容，才算完成适配器迁移。

## 后续阶段

### Gateway、协议与 Provider

网关阶段引入协议无关的请求和结果契约。`protocol/openai`、`protocol/anthropic` 和
`protocol/gemini` 将 HTTP 表示转换为该契约；`provider` 负责能力检查、凭证、上游传输、
健康状态和错误归一化。网关依赖 Routing 和 Project 端口，不依赖具体协议或数据库类型。
三种受支持的协议路径和 Provider 调用均有独立契约测试，且 `server` 仅负责注册它们时，
本阶段才算完成。

### Project 与 Routing

本阶段将 API Key 认证、Project 授权、RBAC、配额准入、并发、候选规划、权重、亲和性、
故障回退和熔断决策迁入 `project` 与 `routing`。网关从读取全局 Store 改为使用窄接口。
必须保留认证顺序、拒绝状态码和响应头、配额预留与结算的原子性、候选顺序，以及已经提交
响应的 Provider 调用不得重试的规则。

### Usage、Worker 与可观测性

生命周期阶段提取用量、请求日志、审计、载荷保留、图像任务、Responses worker、追踪、指标
和结构化日志。网关编排通过命令和投影调用这些模块，不拥有它们的行模型或 worker 状态。
退出条件是用量和审计输出、租约和 fencing 行为、遗留任务恢复、关闭顺序、可观测性标签
保持不变，且 `Server` 不再直接存储领域状态。

### 最终适配器与依赖组装收尾

最后阶段迁移剩余 admin 和持久化适配器，再将依赖规则转为可执行检查。兼容构造器和测试
投影只有在最后一个生产或行为刻画调用方迁移后才删除。最终审查需展示目标包依赖图、通过的
仓库门禁和后端测试，以及每个持久化边界的 SQLite/PostgreSQL 集成覆盖。

每个阶段沿用相同的兼容契约，在本文记录依赖变化和剩余工作；数据库行为涉及模块边界时，
补充针对性的 SQLite 和 PostgreSQL 测试。

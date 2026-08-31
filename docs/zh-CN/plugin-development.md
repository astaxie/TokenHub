# TokenHub 插件开发指南

Language: [English](../plugin-development.md) | 简体中文 | [日本語](../ja/plugin-development.md)

这份文档说明当前 TokenHub 的插件模式，以及如何基于当前版本开发插件。它面向插件作者、平台工程师和运维人员，重点是“怎么做”，而不是只讲概念。

TokenHub 的插件设计原则很明确：

- Core 保持小、稳、可审计。
- 变化快的能力交给 plugin。
- built-in plugin 与 external plugin 共用同一套契约。
- 管理端、Provider、Gateway 链路、后台任务、主题和 UI 贡献都通过显式的插件元数据接入。

## 1. 插件模式

TokenHub 把一个插件看成三个维度的组合：

| 维度 | 回答的问题 | 示例 |
| --- | --- | --- |
| Kind | 这是哪一类插件 | `provider`、`admin_ui`、`sim`、`extension` |
| Placement | 它运行在哪里 | `presentation`、`gateway_chain`、`background`、`management_action` |
| Capability | 它能贡献什么 | `provider_types`、`actions`、`hooks`、`background_jobs`、`theme_tokens` |

一个插件可以同时属于多个 kind，也可以同时声明多个 placement。

例如：

- Codex 订阅插件既是 `provider`，也会贡献 `gateway_chain` 行为和 `management_action` 入口。
- Trace exporter 通常是 `extension + gateway_chain`。
- Heartbeat job 通常是 `extension + background`。
- Shell 皮肤通常是 `sim + presentation`。

核心原则很简单：

> Core 负责管道、鉴权、路由、审计和兼容性；插件负责变化和扩展。

## 2. built-in plugin 与 external plugin

TokenHub 对 built-in plugin 和 external plugin 使用同一套契约。

| 类型 | 来源 | 常见用途 |
| --- | --- | --- |
| built-in plugin | 跟着 TokenHub 一起发布 | 核心 Provider 适配、基础管理端界面、核心 hook、默认主题 |
| external plugin | 来自 marketplace 或私有仓库 | 第三方 Provider、企业扩展、合作方 UI 贡献 |

两者的差别主要在分发方式和信任方式，不在能力形状。

built-in plugin 通常默认可信，因为它随产品一起发布。external plugin 在激活前必须通过 manifest 校验、权限校验和签名/信任校验。

## 3. manifest 结构

每个插件包都由 `plugin.yaml` 描述。

最小结构如下：

```yaml
schema_version: 1
id: tokenhub.provider.kimi-go
name: Kimi Subscription Go Provider
version: 1.0.0
description: Reference Go provider plugin for the TokenHub stdio-json-v1 contract kit.
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
entry:
  backend:
    protocol: stdio-json-v1
    command: bin/provider-kimi-go
capabilities:
  provider_types:
    - kimi_subscription
permissions:
  data:
    read:
      - provider_credentials
distribution:
  repository_url: https://github.com/tokenhub-dev/tokenhub-plugin-marketplace
  homepage_url: https://github.com/tokenhub-dev/tokenhub-plugin-marketplace/tree/main/samples/provider-kimi-go
  license: Apache-2.0
```

### 3.1 关键字段

- `schema_version`：manifest 语法版本。
- `tokenhub.plugin_api`：插件 API 版本，当前是 `v1`。
- `kinds`：`provider`、`admin_ui`、`sim`、`extension` 的一个或多个。
- `placement`：`presentation`、`gateway_chain`、`background`、`management_action` 的一个或多个。
- `entry.backend`：可执行入口；当前样例统一使用 `stdio-json-v1`。
- `capabilities`：真正的能力声明。
- `permissions`：最小权限声明。
- `distribution`：仓库地址、主页、校验和、签名和许可证等元数据。

manifest 是第一道门。manifest 不对，其它都不应该启动。

## 4. 运行时契约

TokenHub 在 Go SDK 里提供了四个标准入口：

- `ServeProvider`
- `ServeAction`
- `ServeGatewayHook`
- `ServeBackgroundJob`

它们分别对应一类契约。

### 4.1 Provider invocation

Provider 插件会收到：

- operation
- provider projection
- resource projection
- provider model
- request payload
- credentials projection

这意味着插件只看到投影后的数据，不会直接碰 core 内部细节。

### 4.2 Action invocation

Action 插件会收到：

- plugin ID
- action ID
- actor
- payload

适合做：

- OAuth start / exchange
- probe
- quota refresh
- 自定义管理动作

### 4.3 Gateway hook invocation

Gateway hook 插件会收到：

- request ID
- stage
- envelope
- 可选的 stage data

适合做：

- privacy filter
- context optimizer
- route rank
- cache lookup / write
- request / response transform
- trace export

### 4.4 Background job invocation

Background job 插件会收到：

- plugin ID
- job ID
- trigger
- actor
- payload

适合做：

- quota sync
- heartbeat
- refresh
- cleanup
- reporting

## 5. 如何开发一个插件

推荐按这个顺序来：

1. 先选 kind 和 placement。
2. 再定义最小 capability。
3. 写 manifest。
4. 写 runtime handler。
5. 加 contract tests。
6. 本地运行验证。
7. 发布到 marketplace。
8. 安装后在 TokenHub 中验证。

### 5.1 先回答你在解决什么问题

写代码前，先问：

- 这是 Provider 集成吗？
- 这只是管理端 UI 贡献吗？
- 这是 shell / theme 改造吗？
- 这是 gateway-chain 扩展吗？
- 这是后台任务吗？
- 这是管理动作吗？

如果这个问题答不出来，插件边界就还不够清楚。

### 5.2 先写最小 capability

不要一开始就把所有能力都塞进来。

建议从最小版本开始：

- Provider plugin：先做 `provider_types` 和 `gateway`
- Action plugin：先做一个 action ID
- Hook plugin：先做一个 stage
- Background plugin：先做一个 job
- SIM plugin：先做一个 theme 或 layout 贡献

### 5.3 写 handler

handler 应该尽量短：

- 解析 invocation
- 执行本插件的逻辑
- 返回结构化结果
- 不打印敏感信息

### 5.4 写 contract tests

每类插件都应该有 contract tests。

重点检查：

- manifest 能不能解析
- capability 是否完整
- 输入输出结构是否正确
- secret 是否不会泄露到 stdout
- 失败路径是否符合预期

### 5.5 跑本地 contract kit

marketplace 仓库里提供了本地 harness：

```bash
go test ./...
go run ./cmd/tokenhub-plugin-test provider --package ./samples/provider-kimi-go
go run ./cmd/tokenhub-plugin-test action --package ./samples/action-echo-go
go run ./cmd/tokenhub-plugin-test hook --package ./samples/hook-trace-go
go run ./cmd/tokenhub-plugin-test background --package ./samples/background-heartbeat-go
```

把 `--package` 换成你自己的插件目录即可。

### 5.6 发布与安装

发布时至少要带：

- version
- repository URL
- homepage URL
- checksum
- signature
- license
- compatibility metadata

安装完成后要验证：

1. manifest 是否通过校验。
2. 权限是否最小化。
3. 重启后是否真正生效。

## 6. 四类插件分别怎么开发

### 6.1 Provider plugin

Provider plugin 负责把 TokenHub 接到模型服务或订阅账号上。

通常会声明：

- `provider_types`
- `provider_resource_types`
- `provider.route_protocols`
- `provider.default_base_url`
- `provider.model_discovery`
- `provider.error_profile`
- `provider.credentials_scope`
- `provider.api_key_required`
- `provider.supports_custom_headers`

常见职责：

- 协议转换
- 模型发现
- quota 或账号同步
- 凭证刷新
- provider-specific 路由行为
- provider-specific UI 元数据

如果是订阅型 provider，quota refresh 和 account sync 尽量放到 background job 或 action 里。

### 6.2 Admin UI plugin

Admin UI plugin 负责管理端配置和操作界面。

常见内容：

- provider form section
- resource panel
- dashboard card
- route detail panel
- settings panel
- page template

规则：

- UI 可以由插件贡献。
- 执行仍然必须经过 Core。
- 插件不能绕过 RBAC。
- 插件不能直接拿原始 admin 凭证。

推荐做法是声明式：

1. 在 metadata 里描述 UI 贡献。
2. 所有动作走 core-mediated endpoint。
3. 数据整理放在 frontend domain helper。
4. React view 尽量只负责渲染。

### 6.3 SIM plugin

SIM plugin 负责视觉和布局。

常见贡献：

- theme tokens
- logo / icon 资产
- shell layout preset
- navigation composition
- dashboard composition
- page template

SIM plugin 只能影响 `presentation`。

如果还需要后台行为，那它就不只是 SIM plugin 了。

### 6.4 Extension plugin

Extension plugin 提供横向能力。

常见能力：

- DLP
- prompt firewall
- semantic cache
- context optimizer
- model router
- billing connector
- notification channel
- approval workflow
- export / import
- trace exporter

Extension plugin 可以跑在多个 placement 上：

- `gateway_chain`：请求路径逻辑
- `background`：同步和周期任务
- `management_action`：管理员触发动作
- `presentation`：相关 UI

尽量默认使用 `observe_only` 或 `read_only`。  
需要 mutation 时，必须显式声明并收紧范围。

## 7. 推荐的仓库结构

当前 marketplace 仓库采用“一套 SDK + 多个 sample package”的结构。

```text
tokenhub-plugin-marketplace/
  go.mod
  sdk/go/tokenhubplugin/
  contract-tests/
    provider/
    gateway-hook/
    management-action/
    background-job/
    protocol/stdio-json-v1/
  samples/
    provider-mock-go/
    provider-kimi-go/
    provider-glm-go/
    action-echo-go/
    hook-trace-go/
    background-heartbeat-go/
  cmd/tokenhub-plugin-test/
```

最小可用 package 通常至少包含：

- `plugin.yaml`
- `main.go`
- 一个 fixture 文件
- 一组 contract tests

命名建议：

- `provider-xxx-go`
- `action-xxx-go`
- `hook-xxx-go`
- `background-xxx-go`

## 8. 版本与兼容性

TokenHub 现在把版本拆成三种：

| Version | 含义 |
| --- | --- |
| Core Version | TokenHub 产品版本 |
| Plugin API Version | 插件协议和 envelope 契约版本 |
| Plugin Package Version | 插件包自己的版本 |

兼容原则：

1. `plugin_api` 在同一个 major 内尽量只做 additive change。
2. manifest schema 尽量保持向前兼容。
3. 同一个 API major 内的 stage 名称应保持稳定。
4. envelope 字段可以新增，但不能偷偷改变已有语义。
5. 新的敏感权限必须重新审批。
6. 新的 placement 或 capability 必须经过 Core 校验。

迁移原则很简单：

- 保留旧 provider ID
- 保留旧路由
- 保留旧资源和 quota
- 保留旧 admin payload alias，直到新契约准备好

## 9. 安全与信任

TokenHub 默认采用最小权限。

插件不能：

- 直接访问 core DB
- 绕过 RBAC
- 直接拿 raw admin token
- 静默扩大权限
- 重新定义 public `/v1` 端点

插件必须说明：

- 读什么
- 写什么
- 需要什么网络访问
- 绑定哪个 stage / job / action
- 是否需要重启

marketplace 分发至少要带：

- checksum
- signature
- key ID
- repository URL
- homepage URL
- license
- compatibility verdict
- advisories
- release notes

## 10. 测试与发布流程

推荐顺序：

1. 本地单元测试
2. manifest 解析测试
3. contract tests
4. package 级测试
5. TokenHub 集成测试
6. marketplace / signature / compatibility 检查
7. 安装与重启验证

不同 kind 的测试重点：

- Provider plugin：route protocols、discovery、credentials projection、response shape、secret redaction
- Admin UI plugin：schema 解析、action 绑定、payload 脱敏，且不能直接调用任意 admin API
- SIM plugin：theme selection、layout selection、template rendering、dashboard composition
- Extension plugin：stage order、mutation limits、failure policy、retry / cancel、permission enforcement

## 11. 从内置能力迁移到插件的路径

最现实的迁移顺序是：

1. 先把当前 built-in descriptor / registry 统一成 plugin 视角。
2. 再把 provider adapter、quota、OAuth、model discovery 外移到插件契约下。
3. 再把 admin UI 的页面、表单、面板、按钮做成 declarative contribution。
4. 再把 gateway enhancement 拆成显式 hooks。
5. 再把后台周期任务变成 background plugins。
6. 最后扩展 marketplace 给第三方作者使用。

这样做的好处是：

- 不会一次性打碎核心。
- 老用户升级路径清楚。
- 每一步都可以独立发布。
- 每一步都能用 contract tests 锁回归。

## 12. 一个简单的决策树

开发前先问：

```text
它是不是连接模型或订阅账号？
  -> Provider plugin

它是不是只改管理端页面、面板或外观？
  -> Admin UI 或 SIM plugin

它是不是会影响用户 token 到 provider 响应的链路？
  -> 带 gateway_chain placement 的 Extension plugin

它是不是定时任务或启动后任务？
  -> Background plugin

它是不是管理员触发的动作？
  -> Management action capability
```

然后再问：

```text
最小需要什么权限才安全？
```

如果答不出来，就说明插件边界还需要收紧。

## 13. 最后的一条原则

写插件时，始终优先考虑：

1. 更小
2. 更安全
3. 更容易升级
4. 更容易和 Core 分离

如果一个行为可以放进插件，就尽量留在插件里。  
如果它必须留在 Core，就让 Core 只做最终裁决，并尽量保持实现路径稳定。

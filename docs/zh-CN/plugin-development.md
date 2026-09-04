# TokenHub 插件架构与开发指南

Language: [English](../plugin-development.md) | 简体中文 | [日本語](../ja/plugin-development.md)

这份文档说明当前 TokenHub 的插件方向，以及如何在这个方向上开发插件。它面向插件作者、平台工程师和运维人员。

本文采用“先做出最小插件，再解释完整契约”的顺序。和 WordPress 通过插件主文件头发现插件类似，TokenHub 通过包根目录的 `plugin.yaml` 发现、校验和加载插件；区别是 TokenHub 插件必须显式声明运行位置、能力和最小权限。

> **当前实现边界：** 本文只描述仓库中已经实现的 Plugin API v1。界面模板是声明式主题和布局能力，并不是任意 React/JavaScript 扩展机制。已安装插件已有详情、文件清单和配置二级路由；配置页提供可检查的模板块和安全主题 Token 调整，源码仍只供只读预览，管理后台不能修改插件代码。

TokenHub 会把 core 保持得很小：

- core 负责鉴权、路由、计费、审计、兼容性和升级安全
- 变化更快的部分交给 plugin
- built-in plugin 与 external plugin 使用同一套契约
- 界面模板、Provider、链路注入、后台任务和 Admin UI 贡献都通过显式的插件元数据接入

## 管理已安装插件

TokenHub 采用 WordPress 插件管理模式中适合企业网关的部分，但将管理动作和插件类型分开。插件管理只有三个一级入口：“已安装插件”负责搜索、状态筛选、版本、更新和生命周期操作；“安装插件”集中提供市场入口、URL 安装、ZIP 上传、checksum 与权限差异预览；“扩展类型”再以二级导航展示 Provider、链路注入、界面模板和后台任务。

已安装列表中的插件名称和“详情”进入概览页，“设置”直接进入配置页。界面模板列表点击模板主体也会进入该模板的“配置”页；“设为默认模板”是独立操作，因此打开配置不会意外切换当前生效界面。这个层级参考了 WordPress 的[安装、更新和管理模式](https://www.waimaob2c.com/wordpress-plugins)，但不会照搬在线代码编辑和不适合企业网关的自动更新行为。

| 路由 | 用途 |
| --- | --- |
| `/plugins` | 已安装插件列表和生命周期操作 |
| `/plugins/[pluginId]` | 元数据、信任、兼容性、能力、Hook、界面贡献、动作、任务和包统计 |
| `/plugins/[pluginId]/files` | 包内相对路径文件清单和安全文本预览 |
| `/plugins/[pluginId]/settings` | 可检查模板块、安全主题 Token 调整、声明权限及插件自己的 UI/配置 Schema |

“文件”页有意不照搬 WordPress 的插件文件编辑器。TokenHub 不暴露包的绝对路径，也不允许在线修改已安装的可执行代码；符号链接会被跳过，二进制、运行状态、隐藏文件、凭证、secret、private 以及过大文件都不能预览。这样既能检查插件包的实现，又不会把管理后台变成远程代码执行入口。

经过管理员认证的检查 API 都是只读的：

- `GET /api/admin/plugins/{plugin_id}/detail`
- `GET /api/admin/plugins/{plugin_id}/file?path={包内相对路径}`

内置插件有实现元数据，但没有独立安装包文件清单。外部插件包可展示文件数、总大小、文件类型，以及符合安全条件的源码、配置和 Schema 内容。整体交互参考 WordPress 的[插件开发文档](https://codex.wordpress.org/zh-cn:%E5%BC%80%E5%8F%91%E4%B8%80%E4%B8%AA%E6%8F%92%E4%BB%B6)，但 TokenHub 保留自己的 manifest、权限和安全模型。

## 1. 插件家族

TokenHub 现在把插件分成几个清晰的家族。

| 家族 | 负责什么 | 示例 |
| --- | --- | --- |
| 界面模板 | 整体外观、布局和模板包 | shell 主题、页面模板、仪表盘组合 |
| Provider | 上游模型接入、鉴权、发现和配额 | Codex、Kimi、Gemini、Anthropic、OpenAI-compatible Provider |
| 链路注入 | 用户请求到上游响应的整条链路 | 隐私控制、路由、缓存、上下文优化、trace 导出 |
| 后台任务 | 定时或运维触发的任务 | 配额刷新、同步、清理、报表 |

Admin UI 贡献是一个能力面，不是顶层家族。它通常挂在 Provider、链路注入或后台任务插件上。

当前仓库里，兼容载荷里仍然使用内部名字 `sim`。对用户来说，可以直接理解成“界面模板”。

一个插件可以跨多个家族，但每个家族都应该保持聚焦。例如：

- Codex 订阅插件是 Provider 插件，同时也可以贡献 Admin UI、链路 Hook 和后台任务
- trace exporter 通常是一个很窄的链路注入插件，主要做 `observe_only`
- 配额同步 worker 通常是后台任务插件
- 整体替换 shell 的插件通常是界面模板插件

## 2. Manifest 契约

每个插件包都由 `plugin.yaml` 描述。

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

关键字段：

- `schema_version`：manifest 版本
- `tokenhub.plugin_api`：插件 API 版本
- `kinds`：`provider`、`admin_ui`、`sim`、`extension` 之一或多个
- `placement`：`presentation`、`gateway_chain`、`background`、`management_action` 之一或多个
- `capabilities`：真正的能力面
- `permissions`：最小权限声明
- `distribution`：仓库地址、主页、校验和、签名和许可证元数据

`management_action` 只是一个过渡能力面，主要给运维触发的操作使用。新的请求路径行为应该进入 `gateway_chain`，重复性任务应该进入 `background`。

## 3. 运行时能力面

TokenHub 目前有三个核心运行时能力面，再加一个过渡兼容能力面。

- `ServeProvider`
- `ServeGatewayHook`
- `ServeBackgroundJob`
- `ServeAction` 仅用于兼容性的管理操作

### 3.1 Provider 调用

Provider 插件会收到：

- operation
- provider projection
- resource projection
- provider model
- request payload
- credentials projection

这样插件只会看到投影后的数据，不会直接碰 core 内部实现。

### 3.2 链路 Hook 调用

链路 Hook 插件会收到：

- request ID
- stage
- envelope
- 可选的 stage data

适合做：

- 隐私控制
- 路由候选生成和排序
- cache lookup / cache write
- 上下文优化
- 请求和响应变换
- trace 导出

### 3.3 后台任务调用

后台任务插件会收到：

- plugin ID
- job ID
- trigger
- actor
- payload

适合做：

- 配额同步
- heartbeat
- refresh
- cleanup
- reporting

### 3.4 Admin UI 贡献

Admin UI 贡献不是一个独立运行时能力面。它是声明式的面板、tab、卡片和设置区块，仍然应该通过 Core 来执行。

## 4. 如何开发插件

最安全的流程是：

1. 先选家族
2. 再定义最小能力集
3. 写 manifest
4. 实现运行时 handler 或 UI 贡献
5. 添加 contract tests
6. 本地运行
7. 发布到 marketplace
8. 安装后在 TokenHub 中验证

写代码之前，先回答：

- 这是 Provider 集成吗？
- 这是界面模板还是 Admin UI 贡献？
- 这是链路注入问题吗？
- 这是后台任务吗？
- 这只是一个过渡性的管理操作吗？

如果答不出来，边界还是太模糊。

### 4.1 先从最小能力开始

不要一开始就把所有能力都塞进去。

- Provider 插件：先做一个 provider type 和一个 resource / route contract
- 链路注入插件：先做一个 hook stage
- 后台任务插件：先做一个 job
- 界面模板插件：先做一个 template、shell 或 layout 贡献

### 4.2 实现 handler

handler 应该尽量短：

- 解析 invocation
- 执行插件逻辑
- 返回结构化结果
- 不输出敏感信息

### 4.3 添加 contract tests

每个插件家族都应该有 contract tests。

重点检查：

- manifest 是否能解析
- capability 是否完整
- 输入输出结构是否正确
- secret 是否会泄露
- 失败行为是否符合预期

### 4.4 跑本地 contract kit

marketplace 仓库里提供了本地 harness：

```bash
go test ./...
go run ./cmd/tokenhub-plugin-test provider --package "$PWD/samples/provider-kimi-go"
go run ./cmd/tokenhub-plugin-test hook --package "$PWD/samples/hook-trace-go"
go run ./cmd/tokenhub-plugin-test background --package "$PWD/samples/background-heartbeat-go"
```

把 `--package` 换成你自己的插件目录。

### 4.5 五分钟跑通第一个插件

最快的起点是仓库中已经跟踪的 heartbeat 后台任务样例：

```text
tokenhub-plugin-marketplace/
├── cmd/tokenhub-plugin-test/          # 本地契约测试工具
├── sdk/go/tokenhubplugin/             # Go 协议辅助包
└── samples/background-heartbeat-go/
    ├── main.go
    └── plugin.yaml
```

先构建可执行文件，再运行契约测试：

```bash
cd tokenhub-plugin-marketplace
mkdir -p samples/background-heartbeat-go/bin
go build -o samples/background-heartbeat-go/bin/background-heartbeat-go \
  ./samples/background-heartbeat-go
go run ./cmd/tokenhub-plugin-test background \
  --package "$PWD/samples/background-heartbeat-go"
```

这个样例的 manifest 把“插件是什么”和“TokenHub 可以怎样调用它”写在一起：

```yaml
schema_version: 1
id: tokenhub.background.heartbeat-go
name: Heartbeat Go Background Job
version: 1.0.0
description: Reference background job plugin.
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - background
entry:
  backend:
    protocol: stdio-json-v1
    command: bin/background-heartbeat-go
capabilities:
  background_jobs:
    - id: heartbeat.ping
      title: Heartbeat ping
      capability: contract.heartbeat
      subject: background-heartbeat-go
      schedule: "@startup"
      timeout_millis: 5000
      max_concurrency: 1
      retry:
        max_attempts: 2
        backoff_millis: 10
      input_schema:
        type: object
        required: [resource_id]
        properties:
          resource_id:
            type: string
          count:
            type: integer
      output_schema:
        type: object
        required: [resource_id, heartbeat, trigger, actor_id]
        properties:
          resource_id:
            type: string
          heartbeat:
            type: string
          trigger:
            type: string
          actor_id:
            type: string
          count:
            type: integer
```

`main.go` 只需要从标准输入读取一次 JSON invocation，并向标准输出写回一次 JSON result。日志和诊断信息必须写到标准错误，不能混入标准输出：

```go
package main

import (
	"context"
	"fmt"
	"os"

	"tokenhub-plugin-marketplace/sdk/go/tokenhubplugin"
)

type payload struct {
	ResourceID string `json:"resource_id"`
	Count      int64  `json:"count"`
}

func main() {
	os.Exit(tokenhubplugin.ServeBackgroundJob(
		context.Background(), os.Stdin, os.Stdout, os.Stderr, handle,
	))
}

func handle(_ context.Context, invocation tokenhubplugin.BackgroundJobInvocation) (tokenhubplugin.BackgroundJobResult, error) {
	input, err := tokenhubplugin.DecodeBackgroundPayload[payload](invocation)
	if err != nil {
		return tokenhubplugin.BackgroundJobResult{}, err
	}
	if input.ResourceID == "" {
		return tokenhubplugin.BackgroundJobResult{}, fmt.Errorf("resource_id is required")
	}
	return tokenhubplugin.BackgroundJobResult{Data: map[string]any{
		"resource_id": input.ResourceID,
		"heartbeat":   "ok",
		"trigger":     invocation.Trigger,
		"actor_id":    invocation.Actor.ID,
		"count":       input.Count,
	}, Metadata: map[string]string{"status": "ok"}}, nil
}
```

先原样跑通样例，再修改 `id`、job ID、输入输出 Schema 和 handler。`plugin.yaml`、handler 和 contract fixture 中的标识必须同时修改。

## 5. 各家族怎么做

### 5.1 Provider 插件

Provider 插件把 TokenHub 接到某个模型服务或订阅账户上。

通常会声明：

- `provider_types`
- `provider_resource_types`
- provider policies
- `provider.default_base_url`
- `provider.model_discovery`
- `provider.credentials_scope`

常见职责：

- 协议转换
- 模型发现
- 配额或账户同步
- 凭证刷新
- provider 特有的路由行为
- provider 特有的 UI 元数据

订阅型 Provider 最好把配额刷新和账户同步放进后台任务里。

### 5.2 链路注入插件

链路注入插件负责塑造“用户请求到上游响应”的整条路径。

典型阶段包括：

- `decode_normalize`
- `admission`
- `privacy_pre`
- `guardrail_pre`
- `cache_lookup`
- `route_candidates`
- `route_rank`
- `provider_call`
- `guardrail_post`
- `usage_attribution`
- `cache_write`
- `settlement`
- `trace_export`

典型策略：

- `fail_closed` 用于 admission、privacy、guardrail 和 routing
- `fail_open` 用于 cache lookup 和 cache write
- `skip_route` 用于 provider call 包装
- `observe_only` 用于 settlement 和 trace export

好的链路插件应该是确定性的、边界窄的，并且明确说明自己读什么、写什么。

### 5.3 界面模板插件

界面模板插件负责视觉识别和有限的声明式布局。它不需要执行二进制文件，最小包可以只包含 `plugin.yaml`：

```yaml
schema_version: 1
id: example.sim.operations
name: Operations UI Template
version: 1.0.0
description: A compact operations template for TokenHub.
tokenhub:
  plugin_api: v1
kinds:
  - sim
placement:
  - presentation
capabilities:
  sim:
    theme_tokens:
      - id: operations-light
        mode: light
        default: true
        tokens:
          bg: "#f5f7fa"
          surface: "#ffffff"
          ink: "#172033"
          accent: "#1677ff"
          border: "#d9d9d9"
    shell_layouts:
      - id: operations-shell
        navigation: sidebar
        density: compact
        content_width: fluid
        default: true
    page_templates:
      - id: provider-detail
        target: provider.detail
        layout: two_column
        regions: [main, side]
    dashboard_compositions:
      - id: operations-dashboard
        layout: grid
        cards:
          - contribution_id: cost-overview
            region: main
            size: wide
            order: 100
```

Plugin API v1 当前支持四类界面模板能力：

| 能力 | 当前可声明内容 |
| --- | --- |
| `theme_tokens` | allowlist 中的颜色、文字、边框、状态色和阴影 token；mode 为 `light`、`dark` 或 `all` |
| `shell_layouts` | `sidebar` 导航，`compact` / `comfortable` / `spacious` 密度，`fluid` / `comfortable` 内容宽度 |
| `page_templates` | target、`single_column` / `two_column` / `grid` / `detail` 布局和 region 名称 |
| `dashboard_compositions` | `grid` / `operations` / `compact_grid` 布局及卡片位置、尺寸和顺序 |

这里有几个重要限制：

- 不能注入任意 CSS、JavaScript、远程脚本、stylesheet URL、`@import` 或 `url(...)`。
- 安装后可以选择模板，也可以调整该模板已声明且位于 allowlist 内的安全主题 Token。调整保存在当前浏览器，不是服务端或团队范围配置。
- “配置”页会把 `shell_layouts` 展开为可检查的导航栏、顶部栏、全局搜索、账号区和内容区，同时展示页面模板、页面区域、仪表盘组合、卡片和插件自己的 Admin UI 贡献。
- 点击任意模板块，会在右侧查看它的声明内容和位置；主题块还提供已声明 Token 的输入控件和“恢复默认”。
- 模板块检查位于插件“配置”二级页内，不为每个块单独分配 URL。目前也没有草稿预览、版本历史或服务端一键回退。

因此，当前能发布的是“结构化主题/布局预设”，不是完整的页面构建器或任意 CSS 编辑器。如果模板需要后台行为，应把行为拆成 Provider、Hook、后台任务或 management action，并分别声明权限。

### 5.4 后台任务插件

后台任务插件负责周期性或运维触发的工作。

典型功能：

- 配额刷新
- heartbeat
- 同步
- 清理
- 报告

后台任务插件应该暴露很小的输入、可预测的重试，以及脱敏后的结果。

### 5.5 Admin UI 贡献

Admin UI 贡献是用来展示插件状态和运维控制的声明式面板、tab、卡片和路由区块。

在 `plugin.yaml` 中用相对路径引用 JSON Schema 文件：

```yaml
kinds: [admin_ui]
placement: [presentation]
entry:
  frontend:
    schema: ui/admin-ui.schema.json
```

`ui/admin-ui.schema.json` 的最小结构如下：

```json
{
  "schema_version": 1,
  "contributions": [
    {
      "id": "provider-setup",
      "slot": "provider.form.section",
      "title": "Connection settings",
      "provider_types": ["example_provider"],
      "schema": {
        "placement": "advanced",
        "fields": [
          {"name": "base_url", "type": "url", "target": "provider"},
          {"name": "api_key", "type": "secret", "target": "plugin_options"}
        ]
      }
    }
  ]
}
```

可用 slot 包括 `nav.section`、`dashboard.card`、`provider.catalog.card`、`provider.form.section`、`provider.model.panel`、`provider.resource.form.section`、`provider.resource.panel`、`route.detail.panel`、`settings.panel`、`report.template`、`theme.tokens`、`layout.preset`、`page.template` 和 `dashboard.composition`。

Schema 可声明的 control type 包括 `text`、`secret`、`url`、`select`、`multi_select`、`switch`、`segmented`、`metric`、`table`、`log_viewer`、`code_viewer`、`action_button`、`oauth_button` 和 `file_import`。但是各个 slot 的 renderer 支持范围不同；发布前必须在目标页面做集成测试，不能只以 manifest 通过校验作为“已支持”的依据。

规则：

- 执行仍然通过 Core
- 插件不能绕过 RBAC
- 插件不能直接使用原始 admin 凭证
- 插件管理的动作必须保持权限收敛并可审计

## 6. 打包与分发

TokenHub 对 built-in 和 external 插件使用同一种包形态。

典型包内容：

- `plugin.yaml`
- 一个运行入口
- 可选资源
- contract tests

分发元数据至少应包含：

- 仓库地址
- 主页地址
- 下载地址
- 校验和
- 签名
- 许可证
- 兼容性元数据

插件市场地址默认是 `https://plugins.betokenhub.com`。运维可以从这个 marketplace 或直接 ZIP URL 安装插件包，校验 checksum，然后重启后端使其生效。

ZIP 可以把 `plugin.yaml` 放在归档根目录，也可以只包一层插件目录；归档中必须且只能发现一个 `plugin.yaml`。不要包含 symlink。运行入口必须保留可执行权限，并且 `entry.backend.command` 必须是插件目录内的相对路径。

以 heartbeat 样例为例：

```bash
cd tokenhub-plugin-marketplace
go build -o samples/background-heartbeat-go/bin/background-heartbeat-go \
  ./samples/background-heartbeat-go
cd samples/background-heartbeat-go
zip -r ../../../background-heartbeat-go.zip plugin.yaml bin
cd ../../..
shasum -a 256 background-heartbeat-go.zip
```

在管理后台打开“插件扩展”，可上传 ZIP，或提供 HTTPS `download_url` 与小写 SHA-256 checksum。安装完成后状态为 `pending_restart`；重启 TokenHub 后端后，再检查插件状态、能力清单和后台任务/页面贡献是否出现。

## 7. 版本与兼容性

把版本看成三件事：

| 版本 | 含义 |
| --- | --- |
| Core version | TokenHub 产品版本 |
| Plugin API version | 插件协议和 envelope 契约版本 |
| Plugin package version | 插件包自己的版本 |

兼容性规则：

1. plugin API 的变化应该在 major 内保持可增量兼容
2. manifest schema 的变化应该尽量保持前向兼容
3. stage 名称在同一个 API major 内要稳定
4. envelope 可以增加字段，但已有语义不能悄悄改变
5. 新的敏感权限需要重新批准
6. 新的 placement 或 capability 需要 Core 校验
7. `sim` 兼容别名可以在内部暂时保留，直到界面模板重命名完全结束

迁移原则很简单：

- 保留旧 provider ID
- 保留旧 route
- 保留旧 resource 和 quota
- 在新契约准备好之前，保留旧 admin payload alias

## 8. 测试与发布流程

推荐顺序：

1. 本地单元测试
2. manifest 解析测试
3. contract tests
4. 包级测试
5. TokenHub 集成测试
6. marketplace 和兼容性检查
7. 安装与重启验证

各家族重点关注：

- Provider 插件：route protocol、发现、credentials projection、响应结构、secret 脱敏
- 链路注入插件：阶段顺序、变更边界、失败策略、重试和取消行为、权限控制
- 界面模板插件：主题选择、布局选择、模板渲染、仪表盘组合
- 后台任务插件：调度、重试规则、并发、结果脱敏
- Admin UI 贡献：schema 解析、动作绑定、payload 脱敏、不能任意调用 admin API

## 9. 从当前内置实现迁移

实际迁移顺序建议如下：

1. 把当前内置描述和注册表统一到插件视角
2. 把 provider adapter、quota、OAuth 和模型发现移动到 provider 插件
3. 把 gateway 增强拆成显式链路 Hook
4. 把周期性任务移动到后台任务插件
5. 把 admin 页面、面板和按钮改成声明式贡献
6. 旧的动作面只保留成兼容桥，等 request path 全部拆出来再收口
7. 扩大 marketplace，支持外部作者

这样做的好处是每一步都可以独立发布，并且能通过 contract tests 验证。

## 10. 一个简单的判断树

```text
它是把 TokenHub 接到某个模型或订阅账户上吗？
  -> Provider 插件

它影响的是用户 token 请求到 provider 响应的路径吗？
  -> 链路注入插件

它只改变 admin 页面、面板、卡片或外观吗？
  -> Admin UI 贡献 或 界面模板插件

它是定时运行或者启动后运行的吗？
  -> 后台任务插件

它暴露的是运维触发动作吗？
  -> 只有在暂时无法迁移到 hook 或 job 时才保留 transitional management_action
```

然后再问一句：

```text
什么是这个插件最小的安全权限集？
```

如果答不上来，就继续缩小插件边界。

## 11. 迁移清单

- [ ] 把当前内置模块映射成 Provider、链路注入、界面模板和后台任务包
- [ ] 把 provider 特有的模型发现和配额逻辑抽进 provider 插件
- [ ] 把请求路径逻辑抽成显式链路 Hook
- [ ] 让 Admin UI 贡献保持声明式和权限收敛
- [ ] 从主插件管理页移除旧的动作执行面
- [ ] 在重命名完成前继续把 `sim` 当作内部兼容别名
- [ ] 在 marketplace 仓库里发布外部插件样例
- [ ] 每个插件家族在发布前都补齐 contract tests

## 12. 最后一句原则

做插件时，优先优化：

1. 更小
2. 更安全
3. 更容易升级
4. 更容易与 Core 分离

如果某个行为可以放进 plugin，就把它留在 plugin 里。
如果它必须留在 Core，就让 Core 做最后决定，并把实现路径保持稳定。

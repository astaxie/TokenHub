# 订阅与账户 Provider

Language: [English](../subscription-providers.md) | 简体中文 | [日本語](../ja/subscription-providers.md)

TokenHub 集成了固定版本 Magpie 清单中的 6 个启用的内置账户 Provider，以及社区清单中的全部 29 个账户 Provider ID。管理员将每个账户连接为 Provider 资源，加载模型，再通过 TokenHub 现有的认证、授权和用量计量链路路由请求。不需要运行 Magpie 服务。

账户适配器与 API Key 预设是不同的接入路径。购买应用订阅不代表自动获得 API 套餐、所有模型或无限用量。本集成不代表上游允许账户共享或自动化访问；账户使用应遵循对应套餐条款。

## 运行环境

- 社区适配器要求后端主机安装 **Node.js 22.23.1 或更新版本**。受支持的 Docker 镜像和原生发行包已包含 Node；源码部署需要将 Node 加入后端进程的 `PATH`。`start.sh` 会检查版本。运行时不安装 npm 包。
- Claude Code 要求后端进程的 `PATH` 中存在官方 `claude` CLI。TokenHub 当前不打包或固定其版本。连接 Claude 账户前，需要在后端执行环境中安装；Docker 部署时应安装在容器内。缺少可执行文件时返回 `claude_cli_unavailable`。
- Codex、Copilot、ChatGPT API、Gemini CLI 和 Antigravity 由 Go 代码接入。其余 29 个适配器使用应用管理的 Node worker，源码固定并随应用打包；不运行 Magpie 守护进程，不加载任意 JavaScript 插件。

受支持的部署方式见[部署文档](deployment.md)。

## 账户 Provider 矩阵

以下是 6 个原生集成对应的 TokenHub Provider 类型。“Chat”指 `/v1/chat/completions`，“Responses”指 `/v1/responses`。流式输出与调用方定义的工具取决于表中协议及上游模型支持情况。

| Provider | TokenHub Provider 类型 | 连接方式 | 对外文本接口范围 |
| --- | --- | --- | --- |
| Claude Code | `claude_subscription` | 浏览器 OAuth、setup token 或账户会话 | 通过官方 Claude Code CLI 提供 Chat |
| OpenAI Codex | `openai_codex` | 现有 Codex OAuth 或显式账户导入 | Responses 及现有兼容转换 |
| GitHub Copilot | `github-copilot-subscription` | GitHub 设备码 | Chat 和 Responses |
| ChatGPT API | `chatgpt-subscription` | Sign in with ChatGPT，或显式导入 token | 使用直接 API token 的 Responses；独立于 Codex 后端 |
| Gemini CLI | `gemini_cli_subscription` | 使用内置桌面客户端配置的 Google 浏览器 OAuth，可选自定义配置 | 通过 Code Assist 提供 Chat |
| Antigravity | `antigravity_subscription` | 使用内置桌面客户端配置的 Google 浏览器 OAuth，可选自定义配置 | 通过 Code Assist 提供 Chat |

全部 29 个社区集成对外提供 **Chat**，在上游模型支持时包含流式输出和调用方定义的工具。内部采用 Chat、Anthropic、Responses 或 Gemini 协议，不代表对外增加 Responses、Embedding 或媒体接口。下表每项还支持显式会话 JSON 导入；Kimi Work 仅支持此方式。

| Provider | 清单 ID | 可用连接方式 |
| --- | --- | --- |
| Cursor | `cursor` | 浏览器；API Key |
| Kiro | `kiro` | Google、GitHub、AWS Builder ID 或 IAM Identity Center 浏览器登录；`ksk_` API Key |
| Qoder | `qoder` | 浏览器/设备授权；个人访问令牌 |
| Qoder China | `qoder-cn` | 浏览器/设备授权；个人访问令牌；官方 Qoder 企业 VPC |
| QwenWork | `qwenwork` | 浏览器/设备授权 |
| QwenWork Enterprise | `qwenwork-enterprise` | 显式指定企业域名的浏览器/设备授权 |
| GCP AI | `gcp-ai` | Google 桌面 OAuth；显式导入 authorized-user ADC JSON 并指定项目和区域 |
| WorkBuddy | `workbuddy` | 浏览器 |
| WorkBuddy AI | `workbuddy-ai` | 浏览器 |
| Xiaomi MiMo | `mimo-app` | 小米账户浏览器/轮询授权；仅种子模型 |
| ZCode | `zcode` | Z.ai、BigModel 或团队浏览器授权；选择站点后使用 GLM Coding Plan API Key |
| Factory | `factory` | 设备码；`fk-` API Key |
| Grok | `grok` | 设备码 |
| Devin | `devin` | 浏览器 |
| Command Code Go | `commandcode-plan` | 浏览器；API Key |
| Zed | `zed` | 浏览器及本地回环地址回调 |
| Kimi Work | `kimi-work` | 仅显式会话 JSON 导入 |
| MiniMax Coding Plan | `minimax-code` | 账户设备/浏览器授权 |
| MiniMax Coding Plan Global | `minimax-code-global` | 账户设备/浏览器授权 |
| Cline | `cline` | 浏览器/设备授权；API Key |
| Trae China | `trae-cn` | 浏览器；上游适配器处于实验阶段 |
| Trae Global | `trae-global` | 浏览器；上游适配器处于实验阶段 |
| OpenDesign | `opendesign` | 账户设备授权；API Key |
| Nous Portal | `nous-portal` | 设备码 |
| Raccoon | `raccoon` | 浏览器；手动回调/token 流程 |
| AutoClaw | `autoclaw` | 通过本地回环地址落地页进行 Google 或 z.ai 授权 |
| AutoClaw China | `autoclaw-cn` | 手机号与短信验证码 |
| CodeArts | `codearts` | 华为云浏览器授权 |
| JoyCode | `joycode` | 京东浏览器授权；`ptKey`，可选企业租户 |

社区条目的 Provider 类型为清单 ID 中的 `-` 替换为 `_` 后追加 `_subscription`，资源类型追加 `_account`。例如，`kimi-work` 对应 `kimi_work_subscription` 和 `kimi_work_account`。平台插件 ID 为 `tokenhub.provider.<清单ID>-subscription`。

## 连接账户

1. 打开管理员 Provider 编辑器，选择对应的账户 Provider。
2. 添加账户资源，选择可用连接方式，按上游提示完成浏览器、设备码或 token 操作。已有会话可以显式导入 JSON。TokenHub 不搜索主机上的 CLI 凭据或桌面账户数据库。
3. 完成授权或导入后，**保存资源**。编辑器会临时接收新凭据以填充资源；仅完成授权不保证账户资源已持久化。
4. 加载账户模型，使用资源探测验证目标模型，并配置路由与价格后再开放应用流量。探测可能消耗账户额度。

新增或编辑供应商时，模型列表支持**全选**和**取消全选**，API Key 接入也支持。输入搜索条件后，批量操作只影响匹配的可引入模型，保留搜索结果之外的选择。已引入的模型保持勾选，不能在此取消。保存供应商后才会引入所选模型。

引入库存后，可在**模型目录 > 从 Provider 引入**中，为每个所选模型分别创建同名对外模型和初始路由。发布前需检查对外售价并确认未知的上游成本。批量选择、订阅成本确认和失败重试方式见[Provider 库存、模型目录与发布](administrator-guide.md#provider-库存模型目录与发布)。

WorkBuddy 和 WorkBuddy AI 的网页登录会通过轮询自动完成。在供应商页面登录后返回 TokenHub，无需粘贴授权码或 Token；凭据回填后还需保存账号资源。授权流程兼容数字或字符串状态码。WorkBuddy 的账号 `domain` 按有长度和字符限制的 `X-Domain` 请求头元数据保留；API 请求目标仍限制为固定官方域名。失败时显示所处阶段及可用的 HTTP／供应商状态码，不暴露账号凭据或上游响应原文。

WorkBuddy 的 Chat 接口要求流式请求。供应商测试和非流式 Chat API 调用也由 TokenHub 在内部以流式方式请求，再汇总为完整 JSON，保留文本、推理内容、工具调用、结束原因和上游报告的用量。原有流式调用仍逐步返回；未完成的响应会报错，不会被误判为测试成功。

凭据应填写在资源的受保护凭据字段中，包括加密会话 JSON；不要放入 Provider Options、普通请求头、日志或源码仓库。待完成授权会话绑定发起管理员和 Provider，使用随机 state、有有效期且只能消费一次。大部分流程十分钟后过期；Claude setup-token 交接五分钟后过期。不再使用的授权流程可在编辑器中取消。如果取消或超时使授权交换中的 worker 停止，TokenHub 会立即释放这个无法继续使用的会话。

通过 `PATCH /api/admin/provider-resources/{id}` 更换已有账户，或在会话 JSON 与 token 之间切换时，将 `credentials.replace` 设为 `true`，并提供新的 `access_token` 或完整 `session_json`。这会整体替换凭据，清除未提供的旧 token、OAuth 客户端配置及账户元数据。所需的新客户端配置和账户元数据也应放入 `credentials`；不带该标记的普通凭据更新继续只合并已提供的凭据字段。未提供新 token 或会话的替换请求会被拒绝。

显式替换凭据时，未提供的连接覆盖配置、健康状态、限流与并发设置保留当前值；显式提交空值或零限制时，仍沿用现有的重置和默认值规则。替换保存成功后，原账号的额度缓存会失效。

更新已有订阅账户时，打开 Provider 编辑器，从账号列表中选择一个账号，在「连接」页点击「重新授权账号」。完成登录或导入后，点击「保存新账号凭据」。这只替换所选账号的凭据，保留其调度配置。切换所选账号会取消待完成的授权，并清除尚未保存的凭据。

Grok 设备码授权现在会申请推理所需的 `api:access` 权限。此前保存或导入的账号可能缺少该权限，即使模型发现成功也无法据此确认推理可用。出现 `grok_api_scope_missing` 时，编辑已保存的 Grok 账号，点击「重新授权账号」，完成设备码授权后点击「保存新账号凭据」，无需删除重建账号。刷新旧授权不能补充缺失权限的用户同意。保存后请测试账号；模型和推理访问仍取决于上游套餐及账号资格。

原生 Copilot 和 ChatGPT 的 HTTP 错误保留上游状态码及有效的 `Retry-After` 等待时间，供路由使用；凭据和模型错误沿用正常的账号与模型故障转移行为。

浏览器界面和后端 API 均使用本地 HTTP 回环地址，且后端配置的公开地址支持此流程时，ChatGPT API 会自动回填凭据。点击 **打开授权**，完成登录后返回 TokenHub，等待凭据回填，再保存账号。本地流程直接回到正在运行的 TokenHub 后端，无需复制回调 URL，也不需要单独启动 `localhost:1455` 监听器。

如果 ChatGPT API 提示客户端注册已完成、需要再次登录，请在同一编辑器中再次点击 **打开授权**。TokenHub 会保留已签发的客户端身份，并使用新的 state、nonce 和 PKCE 参数开始新授权，不会重复兑换被拒绝的授权码。请勿将本次重试与添加另一个账号混用。网关的安装身份在重启后保持不变，注册重试仍绑定到发起操作的管理员。协议说明见 OpenAI 的[注册与登录文档](https://developers.openai.com/siwc/token-sharing-open-source/sign-in)。

此更新通过数据库 v10 迁移新增加密的主机身份和客户端注册记录，保留已有账号。启动时会自动执行增量迁移。受管理的回滚要求目标版本兼容数据库 v10；否则需要在回滚时恢复升级前的数据库备份。

远程部署及其他配置继续使用手动回调流程。请及时粘贴最新的完整回调 URL，保留 `client_id` 参数，不要复用旧授权码。没有 `localhost:1455` 监听器时，浏览器显示连接被拒绝是预期现象，本身不代表授权失败。本次授权码交换被拒绝与已保存账号凭据过期会分别提示。手动交换失败后，会清除一次性会话和回调内容，重新启用 **打开授权**；请重新发起授权后再试。

Copilot 和社区 Chat 请求路由到 Responses 模型时，会保留 `temperature`、`top_p`、`user` 和 `service_tier`，包括显式零值，具体支持取决于所选模型。对于 Factory，显式 `service_tier: "priority"` 可选择模型的 Fast 变体。Gemini CLI 和 Antigravity 会保留终止错误事件报告的用量，并将上游授权、额度和临时故障分类后交给路由处理。

Gemini CLI 和 Antigravity 将代理认证失败与连接超时保留为出口故障，不会惩罚有效账号。GCP 的 token 刷新和模型发现会区分临时上游故障、限流和凭据被拒绝。ChatGPT、Gemini CLI 和 Antigravity 的 token 刷新同样保留临时故障的上游状态和有效重试等待时间，包括绑定账号的限流。

Gemini CLI 和 Antigravity 默认选择 **Google 账号（默认）**，直接点击蓝色的 **打开授权** 即可，无需填写 client ID 或 client secret。每个 Provider 使用各自公开的桌面应用 OAuth 配置；这些应用参数本身不能授权账号，用户仍需登录 Google 并授权。只有需要自定义客户端时，才选择 **自定义 Google OAuth（高级）** 并填写完整的 client ID/secret，缺少其中一项会被拒绝。所选配置绑定到加密的授权会话，并随账号保存供 token 刷新使用；已有账号保留原来的客户端配置。

Google 账号如果需要指定 Cloud 项目，可选择 **Google 账号（指定 Cloud 项目）**，只填写项目 ID，继续使用内置客户端配置。自定义客户端方式也支持项目 ID。项目随新凭据保存，重新授权时不会自动沿用旧账号的项目。完成 Google 登录后，将回调 URL 粘贴到 TokenHub，凭据回填后保存账号。账号资格和模型访问权限仍由上游服务决定。

Google 返回 `UNSUPPORTED_LOCATION` 时，TokenHub 会报告 `google_subscription_location_unsupported`。Google 登录和模型发现可能成功，但推理仍受地区可用性限制。请检查账号的地区使用资格，以及 TokenHub 服务端的实际网络出口地区，包括已配置代理的出口。填写 OAuth 客户端参数或反复授权账号无法解除这一地区限制。

Antigravity 工具参数会转换为其支持的 OpenAPI schema 子集，支持本地引用和可空字段；递归、外部、无法解析或展开过大的 schema 会在推理前返回 HTTP 400。Gemini CLI 保留现有的 JSON Schema 处理方式。

Kimi Work 需要导入包含 `type: "oauth"`、`access`、`webToken`、`uid` 的对象，`deviceId` 可选。这些值应由管理员从本人有权使用的账户中显式获取；TokenHub 不读取桌面数据库。其他 Provider 的会话结构各不相同，必须与所选 Provider 匹配。

Claude Code 可在编辑器中启动官方 `claude auth login --claudeai` 浏览器登录，完成本地回调或粘贴授权码。TokenHub 使用独立配置目录，只读取此次登录的凭据文件或对应的 macOS 钥匙串条目。也可导入 `claude setup-token` 生成的长期 token，或包含 `claudeAiOauth.accessToken` 及可用刷新令牌元数据的会话 JSON。目录中的 `sonnet`、`opus`、`haiku`、`fable` 是请求时由 CLI 校验的别名，不是账户实时可用模型清单。

### 远程浏览器与回调

设备码流程可在其他电脑的浏览器完成。使用手动回调时，按编辑器提示粘贴完整回调 URL。使用本地回环 GET 监听器的社区流程只向该待完成授权会话拥有的确切监听器转发回调，上游适配器校验回调路径、state 和 PKCE。

部分上游流程通过 POST 发送凭据，或依赖本地落地页资源，单独复制 URL 无法替代。AutoClaw 国际版的 Google/z.ai 流程会打开后端主机上的 HTTP 回环地址落地页，需要在该主机浏览器操作，或使用能到达该进程回环监听器的可信隧道。容器有独立回环网络；普通 TokenHub 反向代理不会暴露这些临时监听器。Command Code 的浏览器流程不适合远程部署时，也可选择 API Key 登录。

多实例部署时，同一次授权的启动、轮询、交换与取消必须落到同一 TokenHub 后端实例。社区授权 worker 与回调监听器保留进程内状态。已保存凭据可以通过数据库持久化和共享，但待完成授权及活动对话进程不能跨实例迁移。应为这些操作配置会话保持；仅共享 PostgreSQL 数据库并不能满足要求。

## 账户选项和额度

Copilot 支持 `auto`，包含常规模型列表为空的套餐。上游选择器会在推理前确定模型和协议；显式 Responses 请求要求选中的模型支持 Responses。企业托管用户可在设备授权时填写 `github_host`（`<tenant>.ghe.com`）；该域名随账户加密保存，并决定授权、会话交换和 API 的目的地。

Copilot 的模型协议和 Auto 缓存绑定账号凭据。更换凭据会加载新账号的路由；旧账号尚未完成的模型发现响应不会覆盖新缓存。每次推理请求始终使用模型选择时的账号。

Gemini CLI 读取账户实验标志，在推理前将展示的模型 ID 映射为账户实际使用的上游 ID。手动指定项目不会跳过 Code Assist 的资格校验和开通。Antigravity 工具 schema 同时补齐上游要求的空数组元素 schema。

GCP AI 需要 Google 桌面 OAuth client ID/secret、项目 ID 或编号，以及 Vertex 区域（默认 `global`）。也可通过专用导入方式粘贴包含 `client_id`、`client_secret`、`refresh_token` 的 `authorized_user` ADC JSON。TokenHub 验证项目访问权限、保存可刷新的凭据，并从 Vertex 发现 Gemini 文本模型。不导入服务账号 JSON、文件路径、宿主机 gcloud 配置或浏览器会话。项目需启用 Vertex AI 并授予该用户访问权限。没有接入 GCP Cloud Console 优惠金页面抓取，因此不会显示虚构的剩余优惠金额。

Cursor 团队账户可在导入的会话 JSON 中显式填写正整数 `teamId`。团队请求头与用量查询绑定该资源，TokenHub 不读取宿主机当前选择的 Cursor 团队。

QwenWork Enterprise 在授权时要求输入企业域名。TokenHub 将凭据、后续刷新和推理绑定到该精确域名，Go 传输层继续执行 DNS/IP 限制。每个企业账户的域名在令牌轮换后仍保留。

Provider 编辑器的高级设置提供 Grok X 搜索（`x_search`）、Factory 仅标准额度（`standard_only`）和 Command Code 零数据保留（`zdr`）布尔选项。资源选项覆盖 Provider 默认值。启用 Factory 仅标准额度后，标准额度耗尽时推理返回可重试的额度错误，进入现有路由回退流程。标准用量缺失或格式异常时，即使额外用量仍有余额，也会阻止推理，直到能够确认标准额度。选项的实际效果仍取决于上游支持和套餐资格。

除 Nous Portal 和 GCP AI 外，社区账户均提供额度查询。适配器支持时，手动签到动作返回 `claimed`、`done`、`inactive` 或 `ineligible`；不会自动定时领取。Qoder 国际版可能要求原生设备身份，TokenHub 不采集此身份。特定模型的额度窗口和仅展示窗口不会与全账户额度耗尽混淆。 上游仅返回余额或不完整的窗口时，额度百分比保持未知，监控显示 `-`；明确耗尽的额度显示 `0%`，手动刷新后也保留这一区别。

已保存的社区 OAuth 账号，如果包含刷新令牌且适配器支持令牌刷新，会在账号资源列表中显示「续租 Token」。API Key 账号和不支持令牌刷新的适配器不显示此操作。

额度查询或签到报告订阅登录过期时，返回 HTTP 409 和 `provider_resource_reauthorization_required`，管理员保持登录，可重新授权该资源。Factory Standard-only 也会将此状态识别为账号凭据失效，允许正常回退到其他可用路由。

固定版本清单共覆盖 35 个账户 Provider ID。Cloudflare Workers AI 和 OpenCode Zen Free 属于 API/公共服务，Kimi Desktop 是 agent 连接器，不属于额外的订阅账户 Provider。账号覆盖不等于复刻 Magpie 的全部功能：本机账号扫描、agent 切换、浏览器优惠金抓取及新增媒体生成接口不在此次集成范围内。


## 新增 API Key 预设

目录在现有 API Key Provider 之外新增以下 12 项。它们使用 `openai_compatible` 资源及各自的套餐/API Key，不使用上文的账户 worker 登录。

| 目录 ID | 套餐或 API | 初始模型来源 |
| --- | --- | --- |
| `baidu-qianfan-personal` | 百度千帆 Token Plan 个人版 | 固定版本种子 ID |
| `baidu-qianfan-team` | 百度千帆 Token Plan 企业版 | 固定版本种子 ID |
| `huawei-maas-token-plan` | 华为云 MaaS Token Plan | 固定版本种子 ID |
| `volcengine-coding-plan` | 火山引擎方舟 Coding Plan | 固定版本种子 ID |
| `volcengine-agent-plan` | 火山引擎方舟 Agent Plan | 固定版本种子 ID |
| `qwen-token-plan` | 千问 AI Token Plan | 固定版本种子 ID |
| `kimi-code-global` | Kimi Code 国际版 | 账户模型发现 |
| `tencent-token-plan-enterprise-intl` | 腾讯云 Token Plan 企业国际版 | 固定版本种子 ID |
| `clinepass` | ClinePass | 固定版本种子 ID |
| `ollama-cloud` | Ollama Cloud | 账户模型发现 |
| `commandcode` | Command Code API | 账户模型发现 |
| `nous` | Nous Portal API | 账户模型发现 |

四个使用账户模型发现的预设初始不包含候选模型。现有 StepFun 国际版 Step Plan 预设仍为 `stepfun-ai`。现有 `ollama` 目录模型不会被视为 Ollama Cloud 托管模型权益。刷新行为与来源信息见[模型目录更新](model-catalog-updates.md)。

## 凭据、隔离与限制

已保存账户凭据使用 TokenHub 的加密资源存储。读取凭据时会验证所选适配器类型；账户迁移到另一种 Provider 后，旧请求会返回 HTTP 409，不会使用新 Provider 的凭据。社区 worker 按账户和已认证调用方隔离，拥有独立的 home、工作目录和环境。账户级锁与集群租约串行化可能轮换 token 的操作。父进程收到凭据轮换结果后，即使请求被取消或超时，也会使用独立上下文继续保存，最多等待十秒。保存和 worker 清理完成前，账户级锁与集群租约会保持持有，租约续期也会继续。凭据更新采用比较并交换写入；worker 只有收到持久化成功确认后才可使用新凭据。管理员并发修改或保存失败会使操作失败，不会覆盖更新的凭据。持久化期间，即使账户以相同资源 ID 被替换或删除重建，旧会话轮换也不会覆盖新账户。模型和额度结果同时绑定资源身份与账户替换版本；删除重建或移到另一个 Provider 会使进行中的查询结果失效，包括模型未变化的响应。如果保存成功的确认消息晚于 worker 等待超时到达，仅恢复与该确认匹配的已保存凭据版本，供下一次请求使用；失败或不相关的确认不会授权使用新凭据。

原生 token 刷新（包括后台续期）还会在返回或保存结果前验证原账号身份和凭据替换版本。迁移、替换或删除重建账号都会使进行中的刷新失效，即使 token 值未变；旧刷新失败也不会把新账号标记为需要重新授权。最终写入凭据或状态时还会原子校验当前 Provider 类型，覆盖原生续期、插件刷新和会话轮换。

打包的 worker 属于可信应用代码，不是任意代码沙箱。直接 HTTP/HTTPS 调用和子进程创建被禁用；支持的上游 fetch 经过 Go 传输层的目标地址与网络防护。各 Provider 的域名限制继续生效，包括拒绝任意 Factory 企业主机名。Cursor 使用上游 HTTP/1 RunSSE 兼容路径。错误响应脱敏后仍保留有效的上游重试等待时间，供路由使用；不会转发私有错误响应头或凭据值。

内置适配器明确报告应保留账号登录凭据时，其 HTTP 401/403 响应会按临时故障处理，不会归类为凭据损坏；明确报告登录过期时仍需重新授权。该判断仅采用可信适配器的内部元数据，上游响应头不能覆盖它。

社区 worker 请求会将代理配置、连接、认证和超时故障保留为出口错误，即使内置适配器捕获网络异常并返回 HTTP 错误也不例外。这些共享传输故障不会惩罚账号或触发账号切换，私有传输诊断信息仍会脱敏。

每个中继上游响应的累计大小上限为 64 MiB，流式响应也受此限制，另有 32 MiB 的排队数据限制。超限后会取消上游请求。进程池最多保留 32 个 Node worker，每个以 256 MiB 的 V8 old-space 限制启动。这不是进程 RSS 上限；部署时应为全部 worker、原生缓冲区及 TokenHub 其他组件预留足够内存。

订阅 Responses 流收集器另外将 SSE 数据累计大小限制为 32 MiB，多个小事件的累积也受此限制。超过此组装上限会中止响应，同时保留上游已报告的用量。

Claude Code 独立运行，使用隔离的 home/配置，禁用主机文件和 shell 工具、设置来源、hooks、skills 与浏览器集成。只有调用方请求中声明的工具会通过私有本地 MCP 连接暴露。官方 CLI 自行发送上游网络请求，不经过 Go worker 中继；应对该进程应用主机出站网络策略。可执行文件由应用控制，不能通过 Provider 选项设置。这些隔离措施不构成操作系统沙箱。

Claude 每个账户最多有一个运行中请求或等待工具结果的调用，每个后端进程最多 16 个会话。工具结果必须使用同一已认证租户/API 凭据，返回同一后端进程。即使加权路由选中了其他账户，Chat 和 Messages 工具续接仍绑定原账户及上游模型；原路由不再符合使用条件时，续接会失败。等待工具结果五分钟后超时，单次会话最长 30 分钟。重启或切换后端实例会丢失等待续接的状态。

启用调用方自定义工具时，Claude 会将 `parallel_tool_calls: false` 作为不支持的能力拒绝，因为 CLI 无法保证串行工具调用。没有启用工具的请求，包括 `tool_choice: "none"`，仍然受支持。Claude Code 会在非流式与流式 Chat 响应中保留思考文本、上游报告的缓存用量以及结束原因，包括 token／上下文限制和拒绝。

Claude 还会拒绝 CLI 无法执行的生成控制，包括采样惩罚、`min_p`、`top_k`、多候选输出、seed／logit bias／log probability 设置，以及仅 API 支持的输出或工具控制。零惩罚、`min_p: 0`、`top_k: 0`、`n: 1`、`logprobs: false` 等受支持的默认值、null 控制项和调用方元数据仍可使用。工具续接必须保留最初的有效 token 上限和 `reasoning_effort`；被拒绝的设置变更不会消耗待续接会话。

可续租的账户租约跨后端实例覆盖 Claude 凭据重载、执行、等待工具结果、凭据持久化和进程清理。取消或超时后，清理流程仍会尝试保存已轮换凭据，并持续续租，直到有时限的凭据保存与进程清理完成。等待租约期间仍可取消，并保留请求超时语义。临时存储故障在三次尝试后仍未恢复时，TokenHub 会保留私有临时恢复目录，并记录其路径，不记录凭据值。恢复需要人工处理；在账户凭据持久化恢复前应保留该目录。过期会话不会覆盖管理员并发修改。

模型发现失败时可能回退到固定种子 ID。目录元数据区分 `subscription-account-live`、`subscription-account-fallback`、`subscription-account-empty`；回退模型的权益与价格仍未验证。上游原始变体保留为元数据。显式推理强度和具有完整思考参数的命名变体会成为可选项。Messages 模型会在原生请求中保留声明的推理强度、自适应或关闭思考设置及 token 预算；不支持的推理强度返回 HTTP 400。目录中存在某模型不代表账户有权限、价格已确认、上下文大小已确认或尚有额度。已支持的 worker 额度 hook 通过 `quota.read` 暴露，签到 hook 使用 `checkin.run`。仅针对特定模型的额度窗口不会将整个账户标记为耗尽。

测试覆盖合成 HTTP 流程、协议转换、流式输出、工具调用、加密凭据轮换、取消、进程隔离和回调限制。**尚未使用真实付费账户验证当前套餐额度及全部上游登录流程。** Trae 仍处于实验阶段，上游非公开接口可能独立于 TokenHub 发生变化。已有 Codex 生图能力保持不变；此次新增接入不承诺新的 Embedding、图像、音频或视频能力。

## 固定来源与许可证

- [yetone/magpie](https://github.com/yetone/magpie)，提交 `d580537201938b9f510070a213e09b46479e53ad`，提供已审阅的内置清单、原生协议参考。MIT 许可证保存在 `tools/licenses/magpie-LICENSE`。`backend/internal/server/SUBSCRIPTION_THIRD_PARTY_NOTICES.md` 列明原生 Copilot、ChatGPT、Claude、Gemini CLI 和 Antigravity 的来源；相应平台包与生成的 API 预设包均包含第三方声明。
- [magpie-community/plugins](https://github.com/magpie-community/plugins)，提交 `5c9b7a413756c2b4f139290047085075c83eb3e8`，以 MIT 许可证提供静态打包的社区适配器。`backend/internal/subscriptionworker/vendor/PROVENANCE.json` 记录包版本与 SHA256，旁边保留上游许可证。

Vendored 源码固定版本，与来源清单记录的字节内容一致。TokenHub 的适配修改位于 vendor 目录之外；运行时不会更新源码或安装包。

API 预设信息仍单独固定于 `d7ad3f2a3eea4fadc6f7131bf8b0957024892ba1`。

可复核的账户清单与明确排除项保存在 [`tools/magpie-subscription-coverage.json`](../../tools/magpie-subscription-coverage.json)，自动检查会将其与运行时注册表和插件包逐项核对。

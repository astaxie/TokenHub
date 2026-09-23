# Codex 接入 TokenHub：四种配置方式与恢复指南

Language: [English](../codex-tokenhub-configuration.md) | 简体中文 | [日本語](../ja/codex-tokenhub-configuration.md)

> 本文说明如何将本地 Codex CLI、Codex 桌面端及 IDE 扩展接入 TokenHub，并提供 Profile、进程级临时配置、CLI 全局配置和桌面端配置四种实施方案。
>
> 每种方案均包含适用范围、配置步骤、验证标准和恢复方法。
>
> 如果仅需通过独立 Profile 快速完成接入，请参阅 [Codex 接入 TokenHub：Profile 快速配置](codex-tokenhub-profile-quick-start.md)。

## 1. 配置方案总览

### 1.1 方案对比

| 配置方式 | 影响范围 | 持久化 | 适用场景 | 恢复方式 |
| --- | --- | --- | --- | --- |
| Profile 局部配置 | 仅显式选择该 Profile 的会话 | 是 | 指定项目或任务需要通过 TokenHub | 启动时不传入 `--profile tokenhub` |
| 进程级临时配置 | 当前 Codex 进程或终端会话 | 否 | 首次验证、临时测试或低频使用 | 退出进程并清除临时环境变量 |
| CLI 全局配置 | 当前用户的本地 Codex 会话 | 是 | 本地 CLI 默认通过 TokenHub | 恢复用户级 `config.toml` |
| Codex 桌面端配置 | 桌面端、CLI 和 IDE 扩展 | 是 | 通过桌面端完成长期配置 | 恢复同一份 `config.toml` 并重启 |

首次接入建议先采用**进程级临时配置**完成连通性验证，确认模型、路由和鉴权均正常后，再选择持久化方案。

### 1.2 配置边界

- Codex CLI、Codex 桌面端和 IDE 扩展共用用户级 `~/.codex/config.toml`。CLI 全局配置与桌面端配置仅入口不同，不属于相互隔离的配置。
- 受信任项目中的 `.codex/config.toml` 可以配置模型、沙箱和 MCP 等项目设置，但不能覆盖 `model_provider`、`model_providers` 和 `openai_base_url`。需要局部切换 Provider 时，应使用 Profile。
- TokenHub 控制台登录令牌不能替代项目 API Key。
- TokenHub 项目 API Key 属于敏感凭证，不得写入代码仓库、提交到 Git 或保存在 Shell 历史中。
- 推荐通过 `env_key` 从环境变量读取 Key。个人本地开发环境也可以使用 `experimental_bearer_token` 将 Key 直接写入用户级配置文件，但该方式仅适合开发用途，必须限制文件权限。

### 1.3 截图与脱敏规范

文档截图必须来自真实配置或真实调用结果，并满足以下要求：

1. 仅截取当前步骤所需的窗口区域，不得包含无关终端标签、项目名称、聊天内容或桌面通知。
2. 项目 API Key、`Authorization` 请求头、登录令牌、OAuth Token 和上游账号凭据必须完全遮盖，不得保留首尾字符。
3. 非公开部署地址、用户名、绝对路径、项目 ID、账号 ID、Session ID 和请求 ID 应按组织安全要求打码。
4. 模型 ID、Provider 名称、HTTP 状态和必要的错误码可以保留，以支持配置验收和故障排查。
5. 不得为截图将真实 Key 写入命令。使用环境变量时应采用隐藏输入；使用配置文件直接保存 Key 时，必须在截图前完整遮盖对应字段。
6. 打码应使用不可逆的实体遮盖，不得使用可恢复内容的半透明覆盖或轻度模糊。

---

## 2. 接入前准备

### 2.1 必备信息

| 配置项 | 获取方式 | 要求 |
| --- | --- | --- |
| TokenHub Base URL | TokenHub 部署信息 | 必须以 `/v1` 结尾 |
| TokenHub 项目 API Key | TokenHub 控制台的 **Key 管理** | 必须为项目 API Key |
| 模型 ID | 使用项目 API Key 调用 `GET /v1/models` | 必须采用响应中的实际 `data[].id` |

### 2.2 在当前终端注入配置（推荐）

#### macOS zsh

```bash
export TOKENHUB_BASE_URL="填写实际的 TokenHub Base URL"
read -r -s "TOKENHUB_API_KEY?TokenHub 项目 API Key: "
export TOKENHUB_API_KEY
echo
```

macOS zsh 的实际操作顺序如下：

1. 复制第一行命令，将引号中的内容替换为实际 Base URL，然后按回车执行。
2. 复制第二行 `read` 命令并按回车。终端将显示 `TokenHub 项目 API Key:`。
3. 粘贴实际项目 API Key 并按回车。由于启用了隐藏输入，终端不会显示字符或星号，这是正常现象。
4. 执行 `export TOKENHUB_API_KEY`，将刚才输入的值传递给后续启动的 Codex 进程。
5. 执行 `echo`，恢复正常的命令行换行显示。

命令参数说明：

- `read`：从终端读取一行输入；
- `-r`：按原样读取反斜杠；
- `-s`：关闭输入回显，避免 Key 显示在屏幕上；
- `TOKENHUB_API_KEY?...`：将输入保存到 `TOKENHUB_API_KEY` 变量，并显示问号后的提示文字；
- `export TOKENHUB_API_KEY`：将 Shell 变量导出为环境变量，使 Codex 可以读取。

#### Bash

```bash
export TOKENHUB_BASE_URL="填写实际的 TokenHub Base URL"
read -r -s -p "TokenHub 项目 API Key: " TOKENHUB_API_KEY
export TOKENHUB_API_KEY
echo
```

#### Windows PowerShell

```powershell
$env:TOKENHUB_BASE_URL = Read-Host "TokenHub Base URL（需要以 /v1 结尾）"
$tokenHubSecureKey = Read-Host "TokenHub 项目 API Key" -AsSecureString
$env:TOKENHUB_API_KEY = [System.Net.NetworkCredential]::new("", $tokenHubSecureKey).Password
Remove-Variable tokenHubSecureKey
```

上述环境变量仅在当前终端会话中有效。

### 2.3 查询可用模型

#### macOS、Linux 或 Git Bash

```bash
curl --fail-with-body \
  --url "${TOKENHUB_BASE_URL%/}/models" \
  --header "Authorization: Bearer ${TOKENHUB_API_KEY}"
```

#### Windows PowerShell

```powershell
$tokenHubModels = Invoke-RestMethod `
  -Uri "$($env:TOKENHUB_BASE_URL.TrimEnd('/'))/models" `
  -Headers @{ Authorization = "Bearer $env:TOKENHUB_API_KEY" }

$tokenHubModels.data | Select-Object id
```

应从响应的 `data[].id` 中选择实际模型 ID，不得根据上游模型名称推测，也不得直接复用其他环境的模型 ID。

将模型 ID 保存到当前终端：

#### macOS zsh

```bash
read -r "TOKENHUB_MODEL_ID?输入上一步返回的实际模型 ID: "
export TOKENHUB_MODEL_ID
```

#### Bash

```bash
read -r -p "输入上一步返回的实际模型 ID: " TOKENHUB_MODEL_ID
export TOKENHUB_MODEL_ID
```

#### Windows PowerShell

```powershell
$env:TOKENHUB_MODEL_ID = Read-Host "输入上一步返回的实际模型 ID"
```

### 2.4 验证 Responses 流式调用

`GET /v1/models` 仅能证明当前 Key 对模型可见，不能证明模型路由处于健康状态。Codex 使用 Responses API 的流式响应，因此必须完成一次真实请求验证。

#### macOS、Linux 或 Git Bash

```bash
curl --fail-with-body --no-buffer \
  --request POST \
  --url "${TOKENHUB_BASE_URL%/}/responses" \
  --header "Authorization: Bearer ${TOKENHUB_API_KEY}" \
  --header "Content-Type: application/json" \
  --data "$(printf '{"model":"%s","input":"仅回复：连接成功","stream":true}' "$TOKENHUB_MODEL_ID")"
```

#### Windows PowerShell

```powershell
$tokenHubRequestBody = @{
  model = $env:TOKENHUB_MODEL_ID
  input = "仅回复：连接成功"
  stream = $true
} | ConvertTo-Json -Compress

Invoke-WebRequest `
  -Method Post `
  -Uri "$($env:TOKENHUB_BASE_URL.TrimEnd('/'))/responses" `
  -Headers @{ Authorization = "Bearer $env:TOKENHUB_API_KEY" } `
  -ContentType "application/json" `
  -Body $tokenHubRequestBody
```

如果返回 `provider_capability_not_supported`，应由管理员检查模型路由和 Provider 资源类型。修改本机 Codex 配置无法绕过该限制。

对于 DeepSeek 官方 Provider，Responses 与 Codex 能力按模型开放，`deepseek-v4-flash` 和 `deepseek-v4-pro` 均可使用。两个模型都支持服务端 `web_search`、Codex 的 `apply_patch` 自定义工具，以及范围为 0–20 的 `top_logprobs`，但不支持图片或文件输入。DeepSeek 的 Responses API 是无状态的，因此客户端每轮都必须在 `input` 中传入完整对话历史，不能依赖 `previous_response_id` 或 `conversation`。DeepSeek 会自动管理上下文缓存。启用 `TOKENHUB_CACHE_AFFINITY_ENABLED=true` 后，TokenHub 会使用 `session-id`、`client_metadata.session_id` 或 `prompt_cache_key` 等稳定的 Codex 会话提示，将连续 Responses 请求固定到同一个上游账号；该标识仅控制网关路由，不会在 TokenHub 内创建另一份响应缓存。

---

## 3. 方案一：Profile 局部配置

### 3.1 适用范围

Profile 文件保存在用户目录中，仅在显式传入 `--profile tokenhub` 时生效。该方案适用于仅允许指定项目或任务通过 TokenHub 的场景。

### 3.2 配置文件路径

- macOS / Linux：`~/.codex/tokenhub.config.toml`
- Windows：`%USERPROFILE%\.codex\tokenhub.config.toml`

### 3.3 备份现有 Profile

#### macOS / Linux

```bash
if [ -f "$HOME/.codex/tokenhub.config.toml" ]; then
  cp -p "$HOME/.codex/tokenhub.config.toml" \
    "$HOME/.codex/tokenhub.config.toml.before-edit.$(date +%Y%m%d-%H%M%S)"
fi
```

#### Windows PowerShell

```powershell
if (Test-Path "$env:USERPROFILE\.codex\tokenhub.config.toml") {
  $tokenHubBackupTime = Get-Date -Format "yyyyMMdd-HHmmss"
  Copy-Item `
    "$env:USERPROFILE\.codex\tokenhub.config.toml" `
    "$env:USERPROFILE\.codex\tokenhub.config.toml.before-edit.$tokenHubBackupTime"
}
```

### 3.4 写入 Profile 配置

推荐通过环境变量提供 Key。将以下内容写入或合并到 Profile 文件：

```toml
model_provider = "tokenhub"
model = "填写 GET /v1/models 返回的实际模型 ID"

[model_providers.tokenhub]
name = "TokenHub"
base_url = "填写实际的 TokenHub Base URL"
env_key = "TOKENHUB_API_KEY"
env_key_instructions = "启动 Codex 前请设置 TOKENHUB_API_KEY"
wire_api = "responses"
```

配置要求：

- `base_url` 必须包含 `/v1`。
- 同一文件中不得重复声明 `model_provider`、`model` 或 `[model_providers.tokenhub]`。
- Codex 0.134.0 及以上版本使用独立的 `<profile>.config.toml` 文件，不再使用旧版 `[profiles.tokenhub]` 表。

如果用户希望免去每次执行 `read` 和 `export`，可以将自己的项目 API Key 直接写入 Profile。此时必须删除 `env_key` 和 `env_key_instructions`，改为：

```toml
[model_providers.tokenhub]
name = "TokenHub"
base_url = "填写实际的 TokenHub Base URL"
experimental_bearer_token = "在此粘贴自己的 TokenHub 项目 API Key"
wire_api = "responses"
```

`experimental_bearer_token` 是 Codex 提供的开发用途直接 Bearer Token 配置。不得与 `env_key`、`[model_providers.tokenhub.auth]` 或 `requires_openai_auth` 同时使用。

直接保存 Key 时，应限制配置文件权限：

```bash
chmod 600 "$HOME/.codex/tokenhub.config.toml"
```

该文件不得提交到 Git、上传、分享或包含在截图中。多人共用设备或企业环境应优先使用环境变量或组织批准的凭证管理工具。

![TokenHub Profile 实际配置，Base URL 已打码](../assets/codex-profile/tokenhub-profile-config-redacted.png)

*图 1：使用环境变量方式的 `tokenhub.config.toml` 实际配置。Base URL 已打码，配置文件中不包含 API Key。*

### 3.5 启动方式

启动交互式会话：

```bash
codex --profile tokenhub
```

指定工作目录：

```bash
codex --profile tokenhub --cd "/填写实际项目绝对路径"
```

执行非交互任务：

```bash
codex exec --profile tokenhub --cd "/填写实际项目绝对路径" "填写本次真实任务"
```

如果项目级 `.codex/config.toml` 设置了其他模型，可以在本次启动时显式指定 TokenHub 返回的模型 ID：

```bash
codex --profile tokenhub --model "填写 GET /v1/models 返回的实际模型 ID"
```

### 3.6 验证标准

进入 Codex 后执行 `/status`，确认：

- `Model` 与 TokenHub 返回的实际模型 ID 一致；
- `Model provider` 为 TokenHub；
- TokenHub 请求日志存在对应成功请求。

![Codex 使用 TokenHub Profile 的真实状态，敏感信息已打码](../assets/codex-profile/codex-status-redacted.png)

*图 2：Profile 生效后的 `/status`。Provider 详情、窗口标题和 Session ID 已打码。*

### 3.7 恢复方法

临时恢复默认配置时，直接启动 Codex，不传入 Profile：

```bash
codex
```

停用 Profile：

#### macOS / Linux

```bash
mv "$HOME/.codex/tokenhub.config.toml" \
  "$HOME/.codex/tokenhub.config.toml.disabled"
```

#### Windows PowerShell

```powershell
Rename-Item `
  "$env:USERPROFILE\.codex\tokenhub.config.toml" `
  "tokenhub.config.toml.disabled"
```

如果配置前已经存在同名 Profile，应恢复备份文件。

#### macOS / Linux

```bash
ls -1t "$HOME"/.codex/tokenhub.config.toml.before-edit.*
cp -p "填写需要恢复的实际备份文件路径" \
  "$HOME/.codex/tokenhub.config.toml"
```

#### Windows PowerShell

```powershell
Get-ChildItem "$env:USERPROFILE\.codex\tokenhub.config.toml.before-edit.*" |
  Sort-Object LastWriteTime -Descending

Copy-Item `
  "填写需要恢复的实际备份文件路径" `
  "$env:USERPROFILE\.codex\tokenhub.config.toml"
```

---

## 4. 方案二：进程级临时配置

### 4.1 适用范围

该方案不修改配置文件。Provider、模型和 Base URL 通过 `-c` 参数覆盖，配置仅对当前 Codex 进程有效。

### 4.2 macOS、Linux 或 Git Bash

完成第 2 节的环境变量设置后执行：

```bash
codex \
  -c 'model_provider="tokenhub"' \
  -c "model=\"${TOKENHUB_MODEL_ID}\"" \
  -c 'model_providers.tokenhub.name="TokenHub"' \
  -c "model_providers.tokenhub.base_url=\"${TOKENHUB_BASE_URL}\"" \
  -c 'model_providers.tokenhub.env_key="TOKENHUB_API_KEY"' \
  -c 'model_providers.tokenhub.env_key_instructions="启动 Codex 前请设置 TOKENHUB_API_KEY"' \
  -c 'model_providers.tokenhub.wire_api="responses"'
```

执行非交互任务时，将 `codex` 替换为 `codex exec`，并在命令末尾添加实际任务内容。

### 4.3 Windows PowerShell

完成第 2 节的环境变量设置后执行：

```powershell
codex `
  -c 'model_provider="tokenhub"' `
  -c "model=`"$env:TOKENHUB_MODEL_ID`"" `
  -c 'model_providers.tokenhub.name="TokenHub"' `
  -c "model_providers.tokenhub.base_url=`"$env:TOKENHUB_BASE_URL`"" `
  -c 'model_providers.tokenhub.env_key="TOKENHUB_API_KEY"' `
  -c 'model_providers.tokenhub.env_key_instructions="启动 Codex 前请设置 TOKENHUB_API_KEY"' `
  -c 'model_providers.tokenhub.wire_api="responses"'
```

### 4.4 验证与截图

进入 Codex 后执行 `/status`。验证标准与第 3.6 节一致。


### 4.5 恢复方法

退出当前 Codex 进程后，`-c` 覆盖自动失效。再次直接执行 `codex` 即可使用原配置。

清除当前终端中的临时变量：

#### macOS、Linux 或 Git Bash

```bash
unset TOKENHUB_BASE_URL TOKENHUB_API_KEY TOKENHUB_MODEL_ID
```

#### Windows PowerShell

```powershell
Remove-Item Env:TOKENHUB_BASE_URL -ErrorAction SilentlyContinue
Remove-Item Env:TOKENHUB_API_KEY -ErrorAction SilentlyContinue
Remove-Item Env:TOKENHUB_MODEL_ID -ErrorAction SilentlyContinue
```

关闭当前终端窗口也会清除上述变量。

---

## 5. 方案三：CLI 全局配置

### 5.1 适用范围

该方案适用于当前用户的大多数本地 Codex CLI 会话均需要通过 TokenHub 的场景。

### 5.2 配置文件路径

- macOS / Linux：`~/.codex/config.toml`
- Windows：`%USERPROFILE%\.codex\config.toml`

### 5.3 备份现有配置

#### macOS / Linux

```bash
if [ -f "$HOME/.codex/config.toml" ]; then
  cp -p "$HOME/.codex/config.toml" \
    "$HOME/.codex/config.toml.before-tokenhub.$(date +%Y%m%d-%H%M%S)"
fi
```

#### Windows PowerShell

```powershell
if (Test-Path "$env:USERPROFILE\.codex\config.toml") {
  $tokenHubBackupTime = Get-Date -Format "yyyyMMdd-HHmmss"
  Copy-Item `
    "$env:USERPROFILE\.codex\config.toml" `
    "$env:USERPROFILE\.codex\config.toml.before-tokenhub.$tokenHubBackupTime"
}
```

如果原文件不存在，可直接创建，无需执行备份。

### 5.4 写入全局配置

将以下内容合并到 `config.toml`：

```toml
model_provider = "tokenhub"
model = "填写 GET /v1/models 返回的实际模型 ID"

[model_providers.tokenhub]
name = "TokenHub"
base_url = "填写实际的 TokenHub Base URL"
env_key = "TOKENHUB_API_KEY"
env_key_instructions = "启动 Codex 前请设置 TOKENHUB_API_KEY"
wire_api = "responses"
```

如果文件中已经存在顶层 `model_provider`、`model` 或 `[model_providers.tokenhub]`，应修改现有配置，不得重复追加同名键或同名表。

推荐按照第 2.2 节安全注入 `TOKENHUB_API_KEY`，或使用组织批准的密钥管理工具向 Codex 进程提供该变量。

个人本地开发环境也可以删除 `env_key` 和 `env_key_instructions`，并在 `[model_providers.tokenhub]` 中加入：

```toml
experimental_bearer_token = "在此粘贴自己的 TokenHub 项目 API Key"
```

该字段会把 Key 以明文保存在 `config.toml` 中，仅适合受控的个人开发环境。应执行 `chmod 600 "$HOME/.codex/config.toml"`，并确保该文件不会被提交、上传、分享或纳入截图。

### 5.5 启动与验证

```bash
codex doctor --summary
codex
```

进入 Codex 后执行 `/status`，并在 TokenHub **请求日志**中按时间、项目和模型确认请求已进入网关。

### 5.6 恢复方法

优先使用配置前创建的备份恢复 `config.toml`。

#### macOS / Linux

```bash
ls -1t "$HOME"/.codex/config.toml.before-tokenhub.*
cp -p "填写需要恢复的实际备份文件路径" \
  "$HOME/.codex/config.toml"
```

#### Windows PowerShell

```powershell
Get-ChildItem "$env:USERPROFILE\.codex\config.toml.before-tokenhub.*" |
  Sort-Object LastWriteTime -Descending

Copy-Item `
  "填写需要恢复的实际备份文件路径" `
  "$env:USERPROFILE\.codex\config.toml"
```

如需手动恢复：

1. 将顶层 `model_provider` 和 `model` 恢复为原值；如果原文件不包含这两项，则删除本次新增内容。
2. 删除本次新增的 `[model_providers.tokenhub]` 配置块。
3. 完全退出 Codex 并重新启动。
4. 不再需要当前终端中的 Key 时，执行第 4.5 节的环境变量清理命令。

不得仅删除 `[model_providers.tokenhub]` 而保留 `model_provider = "tokenhub"`，否则 Codex 将无法找到已选择的 Provider。

---

## 6. 方案四：Codex 桌面端配置

### 6.1 适用范围

Codex 桌面端、CLI 和 IDE 扩展共用 `~/.codex/config.toml`。通过桌面端修改该文件后，CLI 的默认配置也会同步变化。

### 6.2 打开配置文件

在 Codex 中依次进入：

**Settings → Configuration → Open config.toml**

备份原文件后，将第 5.4 节的 TokenHub 配置块合并到 `config.toml`。不得重复声明同名 TOML 键或 `[model_providers.tokenhub]` 表。



### 6.3 配置桌面端 API Key

从 Dock、启动台或开始菜单启动的桌面应用通常不会继承终端中的临时环境变量。可将以下配置合并到 `~/.codex/.env`：

```dotenv
TOKENHUB_API_KEY=填写真实的 TokenHub 项目 API Key
```

不得覆盖 `.env` 中的其他变量。macOS / Linux 建议限制文件权限：

```bash
chmod 600 "$HOME/.codex/.env"
```

该文件包含明文敏感凭证，不得上传、分享或提交到代码仓库。企业环境应优先遵循组织规定的凭证管理方案。

> **截图限制｜`.env` 文件**
>
> 不得截取真实 `.env` 内容。如确需记录本步骤，仅保留文件名和权限检查结果，并将 `TOKENHUB_API_KEY` 的值整体替换为 `********`。

### 6.4 重启与验证

完全退出 Codex 后重新启动，并新建一个本地任务。验证以下项目：

- 当前任务使用的模型与 TokenHub 返回的实际模型 ID 一致；
- TokenHub 请求日志存在对应请求；
- 请求所属项目、模型和状态符合预期。

Codex 云端任务的默认模型不受本机 `config.toml` 控制。本文配置仅面向本地桌面端、CLI 和 IDE 扩展。


### 6.5 恢复方法

1. 完全退出 Codex。
2. 恢复配置前备份的 `~/.codex/config.toml`，或按照第 5.6 节手动删除 TokenHub 配置。
3. 如果本次在 `~/.codex/.env` 中新增了 `TOKENHUB_API_KEY`，仅删除对应行，不得删除文件中的其他变量。
4. 重新启动 Codex。

---

## 7. 故障排查

### 7.1 常见错误

| 现象或状态码 | 常见原因 | 处理建议 |
| --- | --- | --- |
| 缺少 `TOKENHUB_API_KEY` | 当前 Codex 进程未读取环境变量 | 检查变量是否存在；桌面端需检查 `~/.codex/.env` 后完全重启 |
| HTTP 401 / `invalid_api_key` | 未携带项目 API Key、认证格式错误或 Key 无法识别 | 确认使用 TokenHub 项目 API Key，而非控制台登录令牌 |
| HTTP 403 | `api_key_disabled`、`api_key_expired` 或 `model_not_allowed` | 检查项目状态、Key 状态、模型白名单和调用策略 |
| HTTP 404 | Base URL 或模型 ID 不正确 | 确认 Base URL 以 `/v1` 结尾，并重新查询 `GET /v1/models` |
| HTTP 429 / `quota_exceeded` | 请求、Token、成本、并发额度或 Provider 限制已触发 | 等待额度窗口恢复，或由管理员调整策略 |
| HTTP 503 / `provider_unavailable` | 当前模型没有可用 Provider | 检查模型路由、Provider 和账号资源健康状态 |
| HTTP 501 / `provider_capability_not_supported` | 路由无法提供 Responses 或流式 Responses API | 调整模型路由或 Provider 资源；Chat Completions 路由不能替代该协议 |

仅检查环境变量是否存在，不得输出 Key：

#### macOS、Linux 或 Git Bash

```bash
test -n "${TOKENHUB_API_KEY:-}" && echo "TOKENHUB_API_KEY 已注入"
```

#### Windows PowerShell

```powershell
if ($env:TOKENHUB_API_KEY) { "TOKENHUB_API_KEY 已注入" }
```

### 7.2 配置未生效

修改配置后仍使用旧 Provider 时，应完全退出当前 Codex 进程并重新启动，同时检查启动命令是否传入 `--profile`、`--model` 或 `--config`。

配置优先级从高到低如下：

1. CLI 参数和 `--config`
2. 受信任项目中的 `.codex/config.toml`
3. `--profile` 选择的 Profile 文件
4. 用户级 `~/.codex/config.toml`
5. 系统配置
6. Codex 内置默认值

---

## 8. 参考资料

- [Codex Config basics](https://learn.chatgpt.com/docs/config-file/config-basic)
- [Codex Advanced Configuration：Profile、单次覆盖与自定义 Provider](https://learn.chatgpt.com/docs/config-file/config-advanced)
- [Codex Environment variables](https://learn.chatgpt.com/docs/config-file/environment-variables)
- [Codex Configuration Reference](https://learn.chatgpt.com/docs/config-file/config-reference)

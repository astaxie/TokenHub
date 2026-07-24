# Codex 接入 TokenHub：Profile 快速配置指南

Language: [English](../codex-tokenhub-profile-quick-start.md) | 简体中文 | [日本語](../ja/codex-tokenhub-profile-quick-start.md)

> 本文面向仅需通过独立 `tokenhub` Profile 接入 TokenHub 的用户，提供配置文件创建、Key 设置、启动验证和恢复默认环境的简化流程。
>
> 本文不比较其他接入方式。如需了解 Profile、进程级临时配置、CLI 全局配置和桌面端配置，请参阅 [Codex 接入 TokenHub：四种配置方式与恢复指南](codex-tokenhub-configuration.md)。

用户开始配置前，管理员需要先在 TokenHub 中接入 OpenAI Codex Provider、订阅账号资源和模型路由，再为实际项目创建 API Key。

## 1. Profile 方案说明

### 1.1 设计目标

`tokenhub` Profile 是一份独立的 Codex 启动配置。仅在启动命令显式传入 `--profile tokenhub` 时生效。

| 启动方式 | 使用的主要配置 | 请求路径 |
| --- | --- | --- |
| `codex` | `~/.codex/config.toml` | 默认 Codex Provider |
| `codex --profile tokenhub` | `~/.codex/tokenhub.config.toml` | TokenHub |

该方案具备以下特征：

- 不覆盖默认 `config.toml`；
- 不在 Profile 文件中保存项目 API Key；
- 仅影响显式选择 `tokenhub` Profile 的会话；
- 可以通过移除启动参数立即恢复默认配置。

### 1.2 Profile 配置内容

Profile 负责定义：

1. TokenHub Provider；
2. TokenHub 当前实际可用的模型；
3. TokenHub Responses API 地址；
4. API Key 对应的环境变量名称 `TOKENHUB_API_KEY`。

### 1.3 截图与脱敏规范

本文仅使用真实配置和真实调用结果。截图必须满足以下要求：

- 完全遮盖 API Key、登录令牌、OAuth Token、账号标识和 Session ID；
- 非公开地址、用户名、绝对路径、项目名称和请求 ID 应按组织要求打码；
- 仅保留当前步骤需要确认的模型、Provider、HTTP 状态和必要命令；
- 不得将真实 Key 写入命令后截图；
- 不得使用模拟终端、演示数据或绘制结果替代真实验证。

---

## 2. 已配置环境的使用

本文对应的 macOS 环境已经创建 `tokenhub` Profile。使用前应确认：

- TokenHub 后端处于可用状态；
- 已获取有效的 TokenHub 项目 API Key；
- Profile 中配置的模型仍存在健康路由。

在终端中安全注入 Key，并启动 Codex：

```bash
read -r -s "TOKENHUB_API_KEY?请输入 TokenHub 项目 API Key: "
export TOKENHUB_API_KEY
echo

codex --profile tokenhub
```

上述命令的详细操作方法见第 4.1 节。如果已经按照第 3.4 节使用 `experimental_bearer_token` 将 Key 直接写入 Profile，则无需执行 `read` 和 `export`，可以直接运行 `codex --profile tokenhub`。

> **验收要求**
>
> 启动后执行 `/status`，确认 `Model provider` 为 TokenHub，且 `Model` 与 Profile 中配置的实际模型一致。

---

## 3. Profile 初始化

本节适用于需要在另一台 macOS 设备上创建相同配置的场景。

### 3.1 检查 Codex CLI

```bash
codex --version
```

命令应返回实际版本号。如果终端提示 `command not found: codex`，应先完成 Codex CLI 安装和登录。

### 3.2 检查 TokenHub 服务

本文对应环境的本机服务地址为：

```text
http://127.0.0.1:8080
```

执行健康检查：

```bash
curl --fail-with-body http://127.0.0.1:8080/healthz
```

预期响应：

```json
{"service":"tokenhub-backend","status":"ok"}
```

如果服务不可用，在 TokenHub 仓库目录启动本地环境：

```bash
cd "/填写 TokenHub 仓库的实际绝对路径"
./start.sh
```

启动进程需要保持运行。后续配置应在新的终端窗口中执行。


### 3.3 创建配置目录并备份

创建 Codex 配置目录：

```bash
mkdir -p "$HOME/.codex"
```

如果同名 Profile 已存在，先创建带时间戳的备份：

```bash
if [ -f "$HOME/.codex/tokenhub.config.toml" ]; then
  cp -p "$HOME/.codex/tokenhub.config.toml" \
    "$HOME/.codex/tokenhub.config.toml.before-edit.$(date +%Y%m%d-%H%M%S)"
fi
```

该操作仅复制现有文件，不删除原配置。

### 3.4 写入 Profile 配置

打开配置文件：

```bash
nano "$HOME/.codex/tokenhub.config.toml"
```

推荐通过环境变量提供 Key。写入本文对应环境的实际配置：

```toml
model_provider = "tokenhub"
model = "gpt-5.6-luna"

[model_providers.tokenhub]
name = "TokenHub Local"
base_url = "http://127.0.0.1:8080/v1"
env_key = "TOKENHUB_API_KEY"
env_key_instructions = "启动 Codex 前请设置 TOKENHUB_API_KEY"
wire_api = "responses"
```

在 `nano` 中依次执行：

1. 按 `Control-O` 保存；
2. 按回车确认文件名；
3. 按 `Control-X` 退出。

配置要求：

- `base_url` 必须包含 `/v1`；
- 同一文件中不得重复声明 `model_provider`、`model` 或 `[model_providers.tokenhub]`；
- Codex 0.134.0 及以上版本使用独立的 `<profile>.config.toml` 文件，不再使用旧版 `[profiles.tokenhub]` 表。

如果用户希望直接在配置文件中保存自己的项目 API Key，可以删除 `env_key` 和 `env_key_instructions`，改为：

```toml
[model_providers.tokenhub]
name = "TokenHub Local"
base_url = "http://127.0.0.1:8080/v1"
experimental_bearer_token = "在此粘贴自己的 TokenHub 项目 API Key"
wire_api = "responses"
```

`experimental_bearer_token` 是 Codex 提供的开发用途直接 Bearer Token 配置。不得与 `env_key`、`[model_providers.tokenhub.auth]` 或 `requires_openai_auth` 同时使用。

直接保存 Key 时，应限制 Profile 文件权限：

```bash
chmod 600 "$HOME/.codex/tokenhub.config.toml"
```

该方式会以明文保存 Key，仅适合受控的个人开发环境。Profile 文件不得提交到 Git、上传、分享或包含在截图中。多人共用设备或企业环境应使用环境变量或组织批准的凭证管理工具。

![TokenHub Profile 实际配置，Base URL 已打码](../assets/codex-profile/tokenhub-profile-config-redacted.png)

*图 1：使用环境变量方式的 `tokenhub.config.toml` 实际配置。Base URL 已打码，配置文件中不包含 API Key。*

### 3.5 可选：验证 TOML 配置

**此步骤为可选项。** 如果 Profile 已能正常启动并通过第 4.3 节的连通性测试，可以跳过本节。首次手动创建或修改 TOML 配置时，建议执行以下检查，以便提前发现格式或字段错误。

使用 macOS 自带的 Python 检查配置文件：

```bash
python3 - <<'PY'
from pathlib import Path
import tomllib

path = Path.home() / ".codex" / "tokenhub.config.toml"
with path.open("rb") as file:
    config = tomllib.load(file)

print("Profile 配置可读取")
print("模型：", config["model"])
print("Provider：", config["model_provider"])
print("地址：", config["model_providers"]["tokenhub"]["base_url"])
PY
```

本文对应环境的预期输出：

```text
Profile 配置可读取
模型： gpt-5.6-luna
Provider： tokenhub
地址： http://127.0.0.1:8080/v1
```

该检查仅验证 TOML 格式及必要字段，不会调用模型，也不会读取或输出 API Key。

---

## 4. 日常使用与验证

### 4.1 注入项目 API Key

本节适用于通过 `env_key = "TOKENHUB_API_KEY"` 读取 Key 的配置。如果 Profile 已使用 `experimental_bearer_token` 直接保存 Key，可以跳过本节。

#### 操作步骤

每次打开新的终端后，按以下顺序操作：

1. 复制下面的 `read` 命令并按回车；
2. 终端显示“请输入 TokenHub 项目 API Key:”后，粘贴实际 Key；
3. 按回车确认。输入过程中屏幕不会显示字符或星号；
4. 执行 `export TOKENHUB_API_KEY`；
5. 执行 `echo`，然后启动 Codex。

```bash
read -r -s "TOKENHUB_API_KEY?请输入 TokenHub 项目 API Key: "
export TOKENHUB_API_KEY
echo
```

各参数的含义如下：

- `read`：从终端读取一行输入；
- `-r`：按原样读取反斜杠；
- `-s`：关闭输入回显，避免 Key 显示在屏幕上；
- `TOKENHUB_API_KEY?...`：将输入保存到 `TOKENHUB_API_KEY` 变量，并显示问号后的提示文字；
- `export TOKENHUB_API_KEY`：将变量导出，使后续启动的 Codex 进程可以读取；
- `echo`：在隐藏输入结束后补充一个换行。

环境变量仅在当前终端会话中有效，关闭终端后自动失效。

不建议采用以下形式：

```bash
export TOKENHUB_API_KEY="真实 Key"
```

直接写入命令可能导致 Key 进入 Shell 历史、聊天记录或截图。

如需确认变量已经注入，只检查变量是否存在：

```bash
test -n "${TOKENHUB_API_KEY:-}" &&
  echo "TOKENHUB_API_KEY 已注入"
```

### 4.2 启动交互式会话

在当前目录启动：

```bash
codex --profile tokenhub
```

指定项目目录启动：

```bash
codex --profile tokenhub \
  --cd "/填写实际项目绝对路径"
```

### 4.3 执行一次性连通性测试

```bash
codex exec \
  --profile tokenhub \
  --ephemeral \
  --sandbox read-only \
  "不要运行工具，仅回复：连接成功"
```

本文对应环境的真实测试结果：

```text
OpenAI Codex v0.145.0
model: gpt-5.6-luna
provider: tokenhub
连接成功
```

验收标准：

- `model` 为 TokenHub 中已启用的实际模型；
- `provider` 为 `tokenhub`；
- 最终响应为“连接成功”；
- TokenHub 请求日志存在对应 HTTP 200 记录。

### 4.4 检查运行状态

进入 Codex 后执行 `/status`，确认模型和 Provider。

![Codex 通过 TokenHub Profile 运行的真实状态，敏感信息已打码](../assets/codex-profile/codex-status-redacted.png)

*图 2：本机真实 `/status`。窗口标题、Provider 详情和 Session ID 已打码。*

---

## 5. 请求路径与运行依赖

使用 `tokenhub` Profile 时，请求链路如下：

```text
当前终端
  → tokenhub Profile
  → http://127.0.0.1:8080/v1
  → TokenHub 项目鉴权与模型路由
  → 已接入的 OpenAI Codex 账号资源
  → 模型响应
```

完成 `GET /v1/models` 查询仅表示模型对当前 Key 可见。实际调用还需要满足以下条件：

- Provider 处于健康状态；
- 账号资源处于健康状态；
- 模型路由已启用；
- 项目 API Key 有效；
- 路由支持流式 Responses API。

本文对应环境已经完成 `gpt-5.6-luna` 的真实流式 Responses 请求验证，TokenHub 请求日志返回 HTTP 200。

---

## 6. 恢复与停用

### 6.1 恢复默认 Codex 配置

退出当前 Codex 会话后，直接执行：

```bash
codex
```

未传入 `--profile tokenhub` 时，Codex 使用默认配置：

```text
~/.codex/config.toml
```

该操作不需要删除 Profile 文件。

### 6.2 清除当前终端中的 Key

```bash
unset TOKENHUB_API_KEY
```

验证变量已经清除：

```bash
test -z "${TOKENHUB_API_KEY:-}" &&
  echo "当前终端中的 TOKENHUB_API_KEY 已清除"
```

### 6.3 停用与重新启用 Profile

停用：

```bash
mv "$HOME/.codex/tokenhub.config.toml" \
  "$HOME/.codex/tokenhub.config.toml.disabled"
```

重新启用：

```bash
mv "$HOME/.codex/tokenhub.config.toml.disabled" \
  "$HOME/.codex/tokenhub.config.toml"
```

如果创建 Profile 前已经存在同名文件，应恢复配置前创建的备份，不得使用改名操作替代原配置。

---

## 7. 故障排查

| 现象 | 原因判断 | 处理方式 |
| --- | --- | --- |
| 提示缺少 `TOKENHUB_API_KEY` | 当前终端尚未注入 Key | 重新执行第 4.1 节的隐藏输入命令 |
| HTTP 401 | Key 不存在、已失效，或不是当前项目的 API Key | 在 TokenHub 项目空间复制或轮换项目 API Key，并重新注入 |
| HTTP 503 / `provider_unavailable` | 当前模型没有可用路由 | 检查 Provider、账号资源、模型路由和 Provider 启用状态 |
| 找不到 Profile | 文件不存在或文件名错误 | 检查 `~/.codex/tokenhub.config.toml` |
| 修改后仍使用旧 Provider | 当前进程未重新加载配置 | 完全退出 Codex 后重新执行 `codex --profile tokenhub` |
| `doctor` 提示 `--profile only applies...` | 当前 Codex CLI 的 `doctor` 不接受 `--profile` | 使用真实 `codex exec --profile tokenhub` 请求验证 |

检查 Profile 文件：

```bash
ls -l "$HOME/.codex/tokenhub.config.toml"
```

文件名必须为：

```text
tokenhub.config.toml
```

不得使用 `tokenhub.toml` 或 `tokenhub.config.toml.txt`。

验证 Profile 时使用：

```bash
codex exec \
  --profile tokenhub \
  --ephemeral \
  --sandbox read-only \
  "不要运行工具，仅回复：连接成功"
```

---

## 8. 安全控制

- 推荐通过 `env_key` 从环境变量读取 Key；
- 仅在受控的个人开发环境中使用 `experimental_bearer_token` 直接保存 Key；
- 直接保存 Key 时，应将 Profile 文件权限设置为 `600`；
- 不建议在 `export` 命令中直接写入真实 Key；
- 不得提交 `~/.codex/.env`、API Key、OAuth Token 或账号凭据；
- 包含 `experimental_bearer_token` 的 Profile 文件同样不得提交、上传或分享；
- 不得在截图中保留 Key、Session ID、账号标识或其他内部信息；
- 如果 Key 已出现在聊天、截图或终端历史中，应立即轮换；
- 使用结束后，可执行 `unset TOKENHUB_API_KEY` 清除当前终端中的 Key。

---

## 9. 操作速查

| 操作 | 命令 |
| --- | --- |
| 使用 TokenHub Profile | `codex --profile tokenhub` |
| 使用默认 Codex 配置 | `codex` |
| 直接在 Profile 中保存 Key | 使用 `experimental_bearer_token`，并删除 `env_key` 和 `env_key_instructions` |
| 检查 Key 是否已注入 | `test -n "${TOKENHUB_API_KEY:-}" && echo "TOKENHUB_API_KEY 已注入"` |
| 清除当前终端中的 Key | `unset TOKENHUB_API_KEY` |
| 检查当前模型和 Provider | 在 Codex 中执行 `/status` |

## 10. 相关文档

- [Codex 接入 TokenHub：四种配置方式与恢复指南](codex-tokenhub-configuration.md)
- [普通用户大模型 API 指南](user-guide.md)

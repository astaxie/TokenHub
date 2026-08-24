# 为 TokenHub 贡献代码

[English](CONTRIBUTING.md) | 简体中文 | [日本語](CONTRIBUTING.ja.md)

TokenHub 包含 Go 后端、Next.js 管理后台、Node.js SDK 冒烟测试、YAML 模型目录和 Docker Compose 部署文件。本指南说明本地开发、修改验证、可选的 AI Agent 工作流和 Pull Request 准备要求。

## 仓库结构

| 路径 | 用途 |
| --- | --- |
| `backend/` | Go HTTP API、持久化、路由、认证、管理功能和后端测试 |
| `frontend/` | Next.js 和 React 管理后台 |
| `sdk/` | OpenAI-Compatible API 和安全策略接口的 Node.js 冒烟测试 |
| `data/model-catalog.yaml` | 纳入版本控制的模型目录源文件 |
| `deploy/` | Docker Compose 部署和环境变量模板 |
| `docs/` | 英文、简体中文和日文文档 |

## 本地开发

本地开发需要 Go 1.26 和 Node.js 20 或更高版本。仅容器和部署检查需要 Docker 与 Docker Compose。

在仓库根目录启动完整的本地开发环境：

```bash
./start.sh
```

需要单独运行组件时，在 `backend/` 目录启动后端：

```bash
TOKENHUB_CORS_ALLOWED_ORIGINS=http://localhost:3000 go run ./cmd/tokenhub
```

在 `frontend/` 目录启动管理后台：

```bash
npm ci
npm run dev
```

仅在兼容的后端可用且必需的环境变量已配置时，才在 `sdk/` 目录运行 SDK 冒烟测试：

```bash
npm ci
npm run test:deepseek
npm run test:anthropic-messages
npm run test:security-policy
```

## 修改要求

- 保持修改范围集中，并保留无关的现有改动。
- 后端行为发生变化时，添加或更新测试。优先使用进程内的 HTTP 或 SMTP 模拟服务器，避免依赖外部网络。
- 除非修改明确更新接口契约，否则保持 OpenAI-Compatible `/v1` 接口兼容。
- 不要提交凭证、本地 `.env` 文件、数据库、生成的备份或运行日志。
- 环境变量发生变化时，同步更新相关示例、Compose 文件、`start.sh` 和部署文档。
- 面向使用者的共享文档需要同步维护英文、简体中文和日文版本。
- 保持 `data/model-catalog.yaml` 纳入版本控制，不要提交其他运行时数据文件。

## 验证

修改过程中先运行范围最小的相关测试，准备提交 Pull Request 前再运行适用的完整检查。

在 `backend/` 目录运行后端检查：

```bash
gofmt -w path/to/changed.go
go test ./...
go vet ./...
```

在 `frontend/` 目录运行前端检查：

```bash
npm ci
npm run lint
npm run typecheck
npm test
npm run build
npx playwright install chromium
npm run test:e2e
```

浏览器冒烟测试会启动相互隔离的 Next.js 前端、Go 后端、模拟 Provider 上游和临时 SQLite 数据库，不需要真实凭据，也不要求预先启动 TokenHub。

修改 Docker 或部署配置时，如果本地可以使用 Docker，需要渲染 Compose 配置：

```bash
docker compose --env-file deploy/.env.example \
  -f deploy/docker-compose.yml config
```

所有修改都需要运行 `git diff --check`。无法运行的检查需要明确说明，并区分新增失败与基础分支已有失败。

## 可选的 AI Agent 开发工作流

TokenHub 为 AI Agent 修改仓库提供两套可选工作流：

| 工作流 | 适用范围 |
| --- | --- |
| [`fast-dev`](docs/development/workflows/fast-dev.md) | 范围明确、风险较低，且不涉及公共 API、持久化、认证或授权、部署及跨组件行为的局部修改 |
| [`feature-dev`](docs/development/workflows/feature-dev.md) | 重要功能、用户可见或跨组件修改、公共 API 或数据模型修改、安全敏感修改、部署修改、大范围重构或架构决策 |

在请求中指定工作流即可启用，例如 `本次修改使用 fast-dev。` 未指定时，Agent 按仓库常规指引执行。如果 `fast-dev` 不再适用，Agent 会在切换到 `feature-dev` 前请求确认。选择工作流不代表允许提交、推送、创建 Pull Request、合并或执行其他外部写入操作。

Agent 专用的仓库指令见 [AGENTS.md](AGENTS.md#optional-development-workflows)。

## Pull Request

- 标题和正文的每一节均使用英文。
- 使用英文 Conventional Commits 风格的标题：`<type>[optional scope][!]: <short summary>`。
- 标题不超过 72 个字符，摘要使用小写祈使句，不添加句号。
- 完整填写 [Pull Request 模板](.github/pull_request_template.md)的每一节，并说明跳过或不适用的检查。
- 根据修改范围说明 API 兼容性、安全、数据库、环境变量、部署、上线和回滚影响。
- 默认创建可直接评审的 Pull Request。仅在明确要求时使用 Draft。

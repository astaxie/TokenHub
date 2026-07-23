# 部署

Language: [English](../deployment.md) | 简体中文 | [日本語](../ja/deployment.md)

TokenHub 面向私有化部署，由 Go 后端、Next.js 管理后台和 SQLite 持久化组成。

## 数据库选择

TokenHub 支持两种数据库后端：

### SQLite（默认）

**优点：**
- 零配置，无需单独的数据库服务
- 适合中小规模部署
- 备份简单（直接复制文件）

**适用场景：**
- 开发和测试环境
- 少于 1000 用户的部署
- 单机部署

**部署：**

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d
```

### PostgreSQL（推荐用于生产）

**优点：**
- 企业级数据库，适合高并发场景
- 更好的事务支持和数据完整性
- 支持复制和高可用

**适用场景：**
- 生产环境
- 超过 1000 用户的部署
- 高可用需求

**部署：**

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.postgres.yml up -d
```

PostgreSQL 的详细配置见 [PostgreSQL 设置指南](../postgresql-setup.md)。

### 使用远端 PostgreSQL 的多实例部署

数据库由 Compose 项目之外的平台托管时，使用 `deploy/docker-compose.remote-postgres.yml`。该配置在可扩容的前后端服务前提供 Nginx 网关，并且不会启动本地数据库。

配置远端 `TOKENHUB_DATABASE_URL`、公网网关地址、生产密钥和可信代理 CIDR 后运行：

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.remote-postgres.yml up -d \
  --scale tokenhub-backend=3 \
  --scale tokenhub-frontend=2
```

所有实例必须使用相同的 `TOKENHUB_SECRET_KEY`。`TOKENHUB_DB_MAX_OPEN_CONNS` 是单实例连接数，需要确保所有实例的连接池总和低于 PostgreSQL 限制。不得让多个后端实例共享 SQLite 文件。

使用 `./deploy/test-multi-instance.sh` 运行真实的双实例 PostgreSQL E2E 测试。

## Docker Compose

创建部署环境变量文件：

```bash
cp deploy/.env.example deploy/.env
```

启动前请编辑 `deploy/.env`：

- `TOKENHUB_ADMIN_TOKEN`：Admin API 启动 Token，请使用至少 32 字节的随机值。
- `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`：仅用于创建初始 `admin` 用户，请设置至少 12 字节的密码。
- `TOKENHUB_SECRET_KEY`：后端密钥，请使用至少 32 字节的随机值并保持稳定。
- `TOKENHUB_PUBLIC_BASE_URL`：展示给用户的 TokenHub 网关访问地址。
- `TOKENHUB_GATEWAY_PORT`：Caddy 网关宿主机端口，默认 `3000`。
- `NEXT_PUBLIC_API_BASE_URL`：保持为空，使浏览器管理后台通过 Caddy 使用同源 API 路径。

在仓库根目录启动：

```bash
./deploy/install.sh
```

脚本会在构建前校验 Compose 环境变量，不输出敏感值地逐项提示不安全的变量。如果 Compose 失败，且本次创建或重启的后端容器处于已退出、重启中、失效或不健康状态，脚本会打印本次启动产生的最多 100 行后端日志。后端之外的故障不会导出无关的后端日志。

只校验配置，不构建或启动容器：

```bash
./deploy/install.sh --check-only
```

使用其他环境文件时，可执行 `./deploy/install.sh --env-file /path/to/deploy.env`。

### 可选：服务器侧构建加速

项目 Dockerfile 不写死区域性的包镜像源。如果服务器访问 Docker Hub、npm 或 Go Module 源较慢，请优先在部署服务器上配置加速，而不是修改 Dockerfile。

对于基础镜像拉取，可在服务器 Docker daemon 中配置镜像加速，例如 `/etc/docker/daemon.json`，然后重启 Docker：

```json
{
	"registry-mirrors": [
		"https://<your-docker-registry-mirror>"
	]
}
```

对于镜像构建阶段的依赖下载，建议在服务器上为 Docker 或 BuildKit 配置 HTTP/HTTPS 出口代理。这样可以保持构建可移植，避免把特定环境的 npm 或 Go 代理配置提交到仓库。

如果部署环境直接访问上游源较慢，可以参考以下服务器侧配置示例：

```bash
# Go Module 下载
go env -w GOPROXY=https://goproxy.cn,direct

# npm 包下载
npm config set registry https://registry.npmmirror.com
```

这些命令用于配置服务器或构建环境。除非明确维护特定环境的分支，否则不要直接写入项目 Dockerfile。

Compose 会启动：

- Caddy 网关：`http://localhost:3000`（通过 `TOKENHUB_GATEWAY_PORT` 设置其他端口）
- TokenHub 前端和后端：仅在内部 Docker 网络中访问
- SQLite 数据：保存在 Docker named volume `tokenhub-data`
- 模型目录：从 `data/model-catalog.yaml` 挂载

供公司内网访问时，将 `TOKENHUB_PUBLIC_BASE_URL` 设为网关地址，例如 `http://172.25.88.170:4003`，设置 `TOKENHUB_GATEWAY_PORT=4003`，并在宿主机防火墙中仅放通该 TCP 端口。不要设置 `NEXT_PUBLIC_API_BASE_URL`：管理后台通过 Caddy 使用同源的 `/api` 请求，Caddy 也会将 `/v1` 请求代理到后端。

查看状态：

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml ps
```

首次登录后台：

- 用户名：`admin`
- 密码：配置的 `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`

在 `prod`、`production`、预发布等非开发环境中，服务会拒绝占位值、少于 32 字节的 Admin Token 或后端密钥，以及少于 12 字节的初始密码。

手动查看或持续跟踪日志：

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml logs -f
```

停止服务：

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml down
```

停止并删除 SQLite 数据卷：

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml down -v
```

只有在你明确要删除本地数据时，才使用 `down -v`。

## 后端环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TOKENHUB_ENV` | `prod` | 运行环境标识 |
| `TOKENHUB_HTTP_ADDR` | `:8080` | 后端监听地址 |
| `TOKENHUB_PUBLIC_BASE_URL` | `http://localhost:3000` | 展示给用户的 TokenHub 网关地址 |
| `TOKENHUB_TRUSTED_PROXY_CIDRS` | 空 | 允许提供 `X-Forwarded-For` 的代理 IP 或 CIDR，逗号分隔 |
| `TOKENHUB_CORS_ALLOWED_ORIGINS` | 公网地址 | 允许调用后端的浏览器 Origin，逗号分隔 |
| `TOKENHUB_ADMIN_TOKEN` | `change-me-tokenhub-admin-token` | Admin API 启动访问 Token |
| `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` | `change-me-tokenhub-admin-password` | 初始 `admin` 用户密码；生产启动前必须修改 |
| `TOKENHUB_SECRET_KEY` | `change-me-tokenhub-secret-key` | 后端密钥 |
| `TOKENHUB_DATABASE_URL` | `sqlite:///app/data/tokenhub.db` | 容器内 SQLite 数据库路径 |
| `TOKENHUB_SQLITE_BACKUP_DIR` | `/app/data/backups` | 备份目录 |
| `TOKENHUB_MODEL_CATALOG_FILE` | `/app/catalog/model-catalog.yaml` | 标准模型目录文件 |
| `TOKENHUB_SEED_DEMO` | `false` | 是否写入演示数据 |
| `TOKENHUB_LOG_LEVEL` | `info` | 日志级别 |
| `TOKENHUB_RESOURCE_FAILURE_THRESHOLD` | `3` | Provider 资源进入冷却前的失败阈值 |
| `TOKENHUB_RESOURCE_COOLDOWN_SECONDS` | `300` | Provider 资源冷却秒数 |
| `TOKENHUB_IN_FLIGHT_LEASE_TTL_SECONDS` | `300` | 集群并发租约的过期时间及续租周期基准 |
| `TOKENHUB_CLUSTER_LOCK_TTL_SECONDS` | `180` | 集群协调锁的过期时间及续租周期基准 |
| `TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS` | `150` | 停机时等待在途请求完成的最长秒数 |
| `TOKENHUB_STOP_GRACE_PERIOD` | `180s` | Docker 强制停止后端前的 Compose 宽限时间 |

## 前端环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NEXT_PUBLIC_API_BASE_URL` | 空 | 保持为空，通过 Caddy 使用同源 API 路径 |
| `NEXT_PUBLIC_APP_NAME` | `TokenHub` | 页面展示名称 |

## 数据和备份

SQLite 是项目、Key、Provider、路由、用户、请求日志、用量、告警、审批、会话和备份记录的持久化来源。

在一键 compose 部署中：

- 容器内数据库路径：`/app/data/tokenhub.db`
- 容器内备份路径：`/app/data/backups`
- Docker volume 名称：`tokenhub-data`

生产建议：

- 将 SQLite 数据库放在持久化磁盘上。
- 将备份保存到应用容器外部。
- 按保留策略清理旧备份。
- 将 Provider 凭证和 Admin Token 放在密钥管理系统或受保护的环境变量中。

## 模型目录

部署文件会把仓库里的 `data/model-catalog.yaml` 挂载到后端容器的 `/app/catalog/model-catalog.yaml`。

更新标准模型目录：

1. 编辑 `data/model-catalog.yaml`。
2. 重启后端容器。
3. 打开管理后台的 `模型目录` 确认结果。

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml restart tokenhub-backend
```

## Caddy 网关

默认 Compose 部署将 Caddy 作为唯一发布宿主机端口的服务。它提供管理后台，并将 `/api/*` 和 `/v1/*` 原样代理到内部 TokenHub 后端。随附配置仅面向受信任内网的 HTTP；如部署需要，请在 Caddy 中增加 TLS 和访问限制。

长文本生成和流式响应可能耗时较长，请合理设置请求体大小和超时时间。

存活探针使用 `/livez`，就绪探针使用 `/readyz`。数据库不可用时，`/readyz` 和向后兼容的 `/healthz` 会返回 `503`。

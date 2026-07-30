# 部署

Language: [English](../deployment.md) | 简体中文 | [日本語](../ja/deployment.md)

TokenHub 面向私有化部署，由 Go 后端、Next.js 管理后台和 SQLite 持久化组成。

## 数据库选择

TokenHub 支持两种数据库后端：

下面的命令使用 Docker Compose。两种后端同样支持不使用 Docker 的方式，参见[裸机部署](#裸机部署不使用-docker)。

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
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d --remove-orphans
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
docker compose --env-file deploy/.env -f deploy/docker-compose.postgres.yml up -d --remove-orphans
```

PostgreSQL 的详细配置见 [PostgreSQL 设置指南](../postgresql-setup.md)。

### 使用远端 PostgreSQL 的多实例部署

默认安装使用 SQLite 启动一个前端实例和一个后端实例。需要横向扩容且数据库由 Compose 项目之外的平台托管时，使用 `deploy/docker-compose.remote-postgres.yml`。该配置在可扩容的前后端服务前提供 Nginx 网关，并且不会启动本地数据库。

```mermaid
flowchart TB
    clients["客户端<br/>管理后台 · OpenAI SDK"] --> nginx["Nginx 网关<br/>负载均衡 · 健康检查"]
    nginx --> frontend["前端副本 × N"]
    frontend --> backend["后端副本 × N"]
    backend <--> providers["模型 Provider"]

    local["data/model-catalog.yaml<br/>候选模型元数据"] -->|"启动时解析并写入候选模板<br/>集群租约串行化各副本"| backend
    providerCatalog["data/provider-catalog.json<br/>受版本控制的 Provider 模板与候选模型"] -->|"管理员新建或刷新 Provider"| backend
    backend <-->|"模型 · 路由 · Provider 目录快照<br/>共享状态 · 数据库锁"| postgres[("共享 PostgreSQL")]

    backend -->|"创建 Provider"| rule["路由创建规则<br/>显式开启候选模型 → 写入 Model → Route<br/>自动候选模型 ∩ 本地 Model → Route"]
    local -.-> rule
    providerCatalog -.-> rule
    rule -->|"创建匹配的 Route"| postgres
```

多实例模式下：

- Nginx 将管理后台、API 和健康检查流量负载均衡到健康副本。
- 后端副本将持久化配置、OAuth 会话、配额计数、审计数据、集群锁和请求并发租约统一存储在 PostgreSQL 中。
- 租约过期和归属判断使用 PostgreSQL 时钟，避免不同宿主机的时钟偏差导致租约被提前接管；失去租约后，心跳会取消对应任务或请求。
- 每个后端启动时都会同步当前配置目录中的候选模型元数据，并通过集群租约串行执行幂等同步。
- Provider 模板和候选模型从仓库中受版本控制的本地目录读取，运行时不依赖远端目录服务。
- 后端会将本地 Provider 目录快照持久化到 PostgreSQL，使各副本使用同一目录；本地文件缺失时则回退至已写入数据库的内置模板。
- 数据库协调故障只释放 Provider 容量，不会把健康的模型 Provider 错误计为失败。

在 `deploy/.env` 中配置远端 `TOKENHUB_DATABASE_URL`、公网网关地址、生产密钥、可信代理 CIDR，以及所需的 `TOKENHUB_BACKEND_REPLICAS` 和 `TOKENHUB_FRONTEND_REPLICAS` 后运行：

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.remote-postgres.yml up -d
```

所有实例必须使用相同的 `TOKENHUB_SECRET_KEY`。`TOKENHUB_DB_MAX_OPEN_CONNS` 是单实例连接数，需要确保所有实例的连接池总和低于 PostgreSQL 限制。不得让多个后端实例共享 SQLite 文件。

使用 `./deploy/test-multi-instance.sh` 运行真实的双实例 PostgreSQL E2E 测试。

## 原生 Release + systemd

单机 Linux 使用 systemd 时，可以选择原生 Release 安装方式。原生安装包支持 `linux/amd64` 和 `linux/arm64`，其中包含 Go 后端、独立运行的 Next.js 管理后台和匹配的 Node.js 运行时。

下载安装脚本并检查内容，然后安装最新稳定版：

```bash
curl -fsSL https://raw.githubusercontent.com/astaxie/TokenHub/main/deploy/native/install.sh \
  -o /tmp/tokenhub-install.sh
sudo bash /tmp/tokenhub-install.sh install
```

未设置 `TOKENHUB_PUBLIC_HOST` 时，安装器会请求 `https://ipinfo.io/json` 并使用其中通过校验的 IP；查询失败后依次回退到 `hostname -I` 返回的第一个地址和 `127.0.0.1`。如果服务器位于 NAT、代理或负载均衡器之后，检测到的出口 IP 可能不是用户访问的入口地址，此时应显式设置 `TOKENHUB_PUBLIC_HOST`。使用 IPv6 字面地址时，安装脚本会在生成 URL 时自动添加方括号：

```bash
sudo env TOKENHUB_PUBLIC_HOST=tokenhub.example.com \
  bash /tmp/tokenhub-install.sh install
```

安装器会把最终使用的主机地址写入 `/etc/tokenhub/tokenhub.env`，后续执行升级等操作时会继续使用该值，避免自动 IP 检测结果变化导致输出地址漂移。

如果希望首次启动就使用 PostgreSQL，而不是默认的 SQLite，请在第一次安装时传入数据库 URL：

```bash
sudo env \
  TOKENHUB_DATABASE_URL='postgres://user:password@db.example.com:5432/tokenhub?sslmode=require' \
  bash /tmp/tokenhub-install.sh install
```

安装器只会在首次创建配置时将该值写入 `/etc/tokenhub/tokenhub.env`。后续执行 install、upgrade 或 rollback 会保留已有配置；确需切换数据库时，请编辑该文件并重启 TokenHub。

首次安装会生成生产密钥和初始管理员密码，密码只会输出一次。运行文件分别保存在：

- Release 和 `current` 软链接：`/opt/tokenhub`
- 配置与密钥：`/etc/tokenhub/tokenhub.env`
- SQLite 数据库与备份：`/var/lib/tokenhub`
- 生成图片：`/var/lib/tokenhub/images`
- Linux systemd 单元：`/etc/systemd/system/tokenhub.service`

需要修改公网地址、CORS Origin、端口、数据库或密钥时，编辑 `/etc/tokenhub/tokenhub.env`，然后重启服务：

```bash
sudo systemctl restart tokenhub
sudo systemctl status tokenhub
sudo journalctl -u tokenhub -f
```

安装脚本会先使用 `checksums.txt` 校验 Release 压缩包，再激活版本；升级时会保留配置和数据：

```bash
sudo bash /tmp/tokenhub-install.sh upgrade
sudo bash /tmp/tokenhub-install.sh upgrade --version 0.3.3
sudo bash /tmp/tokenhub-install.sh rollback --version 0.3.2
sudo bash /tmp/tokenhub-install.sh uninstall
```

`upgrade` 会拒绝低于当前安装版本的目标；需要降级时必须显式使用 `rollback`。使用新版安装器升级旧安装时，如果尚未配置 `TOKENHUB_IMAGE_STORAGE_DIR`，安装器会自动将持久化图片目录补充为 `/var/lib/tokenhub/images`。

`uninstall` 会保留 `/etc/tokenhub` 和 `/var/lib/tokenhub`。只有确定要同时删除配置和应用数据时，才使用 `uninstall --purge`。
安装器会在应用、配置和状态目录中写入所有权标记。卸载时，如果目录没有标记或标记与当前配置不一致，安装器会拒绝递归删除；`/opt`、`/etc`、`/var/lib` 等系统级目录也不会被接受为托管目录。首次安装会拒绝相同的前后端端口；如果系统提供 `ss` 或 `lsof`，脚本还会在下载 Release 前拒绝已被占用的端口。安装或升级只有在 systemd 服务处于 active 状态、后端健康检查和管理后台都可访问后才会报告成功；就绪检查失败时会同时输出近期服务日志。

使用 fork 测试时，请下载该 fork 中的安装脚本，并指定它的公开 Release 仓库：

```bash
sudo env TOKENHUB_RELEASE_REPOSITORY=your-account/TokenHub \
  bash /tmp/tokenhub-install.sh install --version 0.3.3
```

原生 Release 安装会在版本面板中显示为「原生 Release」。管理员可以直接在页面下载并校验更新或回退版本，然后点击「立即重启」，由 systemd 激活目标版本。每个 GitHub Release 标签必须是以 `v` 开头的严格语义化版本，并包含 Linux 压缩包和 `checksums.txt`；发布 Release 后，`.github/workflows/native-release.yml` 会构建并附加 `linux/amd64` 和 `linux/arm64` 文件。
已经下载并校验通过的版本会保留在本地，即使 GitHub Releases API 暂时不可访问，也可以继续回退。

## Docker Compose

创建部署环境变量文件：

```bash
cp deploy/.env.example deploy/.env
```

启动前请编辑 `deploy/.env`：

- `TOKENHUB_ADMIN_TOKEN`：Admin API 启动 Token，请使用至少 32 字节的随机值。
- `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`：仅用于创建初始 `admin` 用户，请设置至少 12 字节的密码。
- `TOKENHUB_SECRET_KEY`：后端密钥，请使用至少 32 字节的随机值并保持稳定。
- `TOKENHUB_IMAGE_TAG`：托管 TokenHub 镜像标签，默认 `latest`。
- `TOKENHUB_PUBLIC_BASE_URL`：展示给用户的后端访问地址。
- `TOKENHUB_API_BASE_URL`：浏览器管理后台访问后端的地址，由前端服务在运行时读取。旧变量 `NEXT_PUBLIC_API_BASE_URL` 保留一个兼容周期，作为回退配置。
- `TOKENHUB_BACKEND_PORT`：后端宿主机端口，默认 `8080`。
- `TOKENHUB_FRONTEND_PORT`：管理后台宿主机端口，默认 `3000`。
- `TOKENHUB_BACKEND_REPLICAS`：远端 PostgreSQL Compose 的后端副本数，默认 `2`。
- `TOKENHUB_FRONTEND_REPLICAS`：远端 PostgreSQL Compose 的前端副本数，默认 `2`。

在仓库根目录启动：

```bash
./deploy/install.sh
```

脚本会先校验 Compose 环境变量，再拉取已发布镜像并启动托管应用容器，不在部署服务器构建镜像；只有在最多等待 180 秒且 Compose 健康检查通过后才报告成功。从旧的双容器结构升级时，脚本会移除已废弃的独立前端容器，但保留 `tokenhub-data` 数据卷。首次发布 GHCR 镜像期间，如果镜像无法拉取，脚本会自动改为从当前代码构建。校验失败时会列出不安全的变量，但不会输出敏感值。新后端启动失败或未能进入健康状态时，脚本会打印本次启动产生的最多 100 行日志。

只校验配置，不拉取镜像或启动容器：

```bash
./deploy/install.sh --check-only
```

使用其他环境文件时，可执行 `./deploy/install.sh --env-file /path/to/deploy.env`。

### 已发布镜像的版本规则

GitHub Actions 为 `linux/amd64` 和 `linux/arm64` 发布完整的 `ghcr.io/astaxie/tokenhub-backend` 镜像。镜像名称为兼容旧部署而保留，其中实际包含后端、独立 Next.js 管理后台、Node.js 运行时和容器监督进程。

- 发布带有严格 `v` 前缀语义化标签的 GitHub Release 后，自动构建对应的纯数字镜像标签；非预发布版本同时更新主次版本标签和 `latest`。
- `workflow_dispatch` 仅允许发布 `edge` 或独立的 `manual-*` 标签，不能覆盖正式版本标签或 `latest`。
- PR 不构建或推送容器镜像。
- 合并到 `main` 不发布镜像。

工作流先使用本次运行专用的暂存标签推送并验证多平台镜像，再发布最终标签。生产部署建议固定完整版本标签，不依赖持续变化的 `latest`。

GHCR 首次发布产生的 Package 默认为私有。开放匿名部署前，仓库所有者需要将该 Package 调整为 Public。在此之前，使用默认 `latest` 标签的安装会在拉取失败后自动改为从本地源码构建。如果显式配置的 `TOKENHUB_IMAGE_TAG` 无法拉取，安装脚本会直接退出，不会把当前源码标记成该版本。

### Docker 版本状态与回退

平台管理员可以点击 TokenHub 标志下方的版本胶囊，查看当前运行版本、检查最新的 GitHub 正式 Release，并列出最多 3 个更早的稳定版本。正式镜像构建会从发布工作流获得精确版本号；本地源码构建使用项目包版本，并明确标记为源码构建。托管更新、回退和重启请求都会写入管理员审计日志。

版本检查会通过限时的出站 HTTPS 请求访问公开的 GitHub Releases API，并将成功结果缓存 20 分钟。默认检查 `astaxie/TokenHub`；维护者可以将 `TOKENHUB_RELEASE_REPOSITORY` 设置为其他可信的公开 `owner/repository`，用于 fork 发布验证。GitHub 故障或仓库尚无 Release 不会影响网关流量；面板会展示不可用状态，同时保留当前版本信息。

例如，在源码部署中检查 fork 的 Release：

```bash
TOKENHUB_RELEASE_REPOSITORY=your-account/TokenHub ./start.sh
```

默认 SQLite 和本地 PostgreSQL Compose 使用一个托管应用容器。管理员可以点击「立即更新」，等待系统下载、校验并将当前平台的完整 Release 包安装到 `tokenhub-releases` 卷，然后点击「立即重启」。接口返回成功后进程主动退出，Docker 的 `restart: unless-stopped` 会同时以目标版本重新启动后端和前端。容器不会挂载 Docker Socket，也不会控制宿主机 Docker daemon。

新拉取的镜像首次使用该卷时，镜像版本和内容指纹共同构成基线。页面安装的版本、`current` 链接和历史 Release 都保存在 `tokenhub-releases`，因此使用同一镜像进行普通重启或重建容器不会丢失更新结果；拉取其他镜像，或者在相同版本下重新构建了不同源码时，会激活新的镜像内容。远端 PostgreSQL 多实例 Compose 禁用原地更新，因为只更新收到管理员请求的单个副本会造成集群版本分裂；该模式提示管理员使用原来的 Compose 文件和环境配置手工更新，以保留已配置的副本数。源码部署仍提示手工更新。回退前必须完成数据库备份，并确认目标版本支持当前数据库结构。

### 可选：本地构建

需要从当前代码构建镜像时执行：

```bash
./deploy/install.sh --build
```

以下加速配置仅适用于本地源码构建。

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

- 后端：`http://localhost:8080`
- 前端：`http://localhost:3000`
- SQLite 数据：保存在 Docker named volume `tokenhub-data`
- 模型目录：使用所选后端镜像中内置的版本

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

仅在明确需要删除本地数据时使用 `down -v`。

## 裸机部署（不使用 Docker）

`deploy/bare-metal/` 以完全不依赖 Docker 的方式运行同样的两个服务。当环境无法使用或不允许使用 Docker 时使用这种方式。应用本身没有任何改动：后端是单个 Go 二进制，控制台是 Next.js standalone 服务。

它提供两个入口：

| | 用途 | 需要 root | 重启后保留 |
| --- | --- | --- | --- |
| `run-local.sh` | 在自己的机器上运行生产构建 | 否 | 否 |
| `build.sh` + `install.sh` | 在服务器上以 systemd 方式安装 | 是 | 是 |

两者都**仅支持 SQLite**。后端本身也支持 PostgreSQL，但这套脚本不负责管理，参见下文[数据库](#数据库)。

### 本地运行

```bash
./deploy/bare-metal/run-local.sh          # 前台运行，Ctrl-C 同时停止两者
./deploy/bare-metal/run-local.sh -d       # 后台运行，立即返回
./deploy/bare-metal/run-local.sh status
./deploy/bare-metal/run-local.sh logs -f
./deploy/bare-metal/run-local.sh stop
```

按需构建两个组件，然后以 loopback 方式运行。二进制、控制台产物、数据库、日志和 pid 文件都放在仓库内的 `.tokenhub/`（已被 gitignore），删除该目录即可清除全部痕迹。不安装任何系统级内容，也不创建服务账号。

加 `-d` 后服务会脱离启动它的 shell，在该 shell 退出、终端关闭后继续运行；但重启机器不会自动拉起，需要开机自启请用 systemd 安装方式。两种模式都会写 pid 文件，因此 `status` 和 `stop` 对前台实例同样有效。`stop` 在发信号前会校验记录的 pid 仍属于本实例，因此不会误杀被系统复用了该号码的其它进程；启动前还会先占用两个端口，端口被占用时直接报错，不会把别的服务的响应误判成启动成功。

以上在 Linux 上验证过。macOS 没有 `setsid`，脚本会退化为遍历进程树来停止服务；该路径已实现但未在 macOS 上实测。

它运行的是**生产构建**——和部署时完全相同的 standalone 产物——而不是 dev server，因此能暴露只在生产构建下出现的问题。它使用开发用凭据（`admin` / `admin123456`）且只监听 loopback，仅供本地使用，不是部署方式。

其他参数：`--rebuild`、`--reset`（清空本地数据库）、`--backend-port N`、`--console-port N`、`restart`。

### 环境要求

| 主机 | 要求 |
| --- | --- |
| 构建机 | Go（版本见 `backend/go.mod`）、Node 22 或更高、npm、C 编译器 |
| 目标机 | 带 systemd 和 GNU coreutils/findutils 的 Linux（主流发行版均满足）、系统级安装的 Node 22 或更高 |

后端通过 cgo 链接 SQLite，因此需要 C 编译器，并且产出的二进制与目标机的架构和 libc 绑定。请在与目标机一致的主机上构建。

Node 必须是系统级安装。服务单元设置了 `ProtectHome=true`，位于用户主目录下的解释器（nvm、fnm、asdf）在运行时不可访问。安装器会直接拒绝这类解释器，而不是装出一个稍后才失败的服务。

### 构建发布包

```bash
./deploy/bare-metal/build.sh
```

产出 `dist/tokenhub-<version>-<os>-<arch>/` 及对应的 `.tar.gz`。该目录是自包含的：后端二进制、控制台产物、两份目录文件、unit 文件、配置示例、安装器和 `SHA256SUMS`。将任一形式拷贝到目标机即可，目标机不需要仓库检出，也不需要 Go 工具链。

### 安装

```bash
sudo ./install.sh --generate-secrets
```

`--generate-secrets` 会为新创建的配置填充 `TOKENHUB_SECRET_KEY`、`TOKENHUB_ADMIN_TOKEN` 和 `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`。不加该参数时，若配置仍保留 `change-me-*` 占位值，两个 unit 会被安装但**保持 disabled 且不启动**——把配置无效的 unit 设为开机自启，只会让它在下次启动时反复崩溃。

启动后，安装器会轮询 `/readyz` 和控制台端口；任一失败时打印 `systemctl status` 与最近的日志。

其他参数：`--no-start`、`--install-timers`（为当前数据库安装备份定时器）、`--skip-backup`（跳过升级前快照，不推荐）。

### 安装后的目录结构

| 路径 | 属主 | 内容 |
| --- | --- | --- |
| `/opt/tokenhub/releases/<version>/` | `root:root` | 只读的发布包 |
| `/opt/tokenhub/current` | 符号链接 | 当前生效的版本，原子切换 |
| `/etc/tokenhub/backend.env` | `root:tokenhub` 0640 | 后端配置与密钥 |
| `/etc/tokenhub/frontend.env` | `root:tokenhub-web` 0640 | 控制台配置，不含密钥 |
| `/var/lib/tokenhub/` | `tokenhub:tokenhub` 0750 | SQLite 数据库、备份、升级前快照 |

使用两个服务账号：后端用 `tokenhub`，控制台用 `tokenhub-web`。二者不共享配置文件，因此控制台被攻破也读不到密钥、管理员令牌或数据库凭据。后端 unit 以 `UMask=0077` 运行，确保 SQLite 数据库及其备份不会被其他用户读取。

### 数据库

数据库位于 `/var/lib/tokenhub/tokenhub.db`，备份位于 `/var/lib/tokenhub/backups`。`backend.env` 中特意使用绝对路径：内置默认值是相对于工作目录的，而这里的工作目录是只读的发布目录。

后端也能运行在 PostgreSQL 上，但安装器会**直接拒绝**这类配置，而不是提供半吊子支持。要正确管理 PostgreSQL，就必须同时负责启动前预检、升级前的 `pg_dump`、备份保留策略，以及一套版本必须跟随服务端的客户端工具链——这些都不在本安装器职责内。若要让 TokenHub 跑在 PostgreSQL 上，请自行配置并托管后端，参见 [PostgreSQL 配置指南](../postgresql-setup.md)。

### 备份

`--install-timers` 会安装一个每日定时器：调用后端自身的在线备份 API，随后删除已过期的备份。应用只记录过期时间，从不自行清理，因此保留策略由定时器驱动。切勿改为直接拷贝运行中的 SQLite 文件，那样得到的是残缺副本。备份期间会占用后端唯一的 SQLite 连接，因此默认排在低峰时段；另外静态管理令牌对应的是最早创建的管理员用户，把该用户降级会导致定时器失效。

定时器失败时以非零码退出并写入 journal；请监控 `systemctl --failed` 或添加 `OnFailure=` unit。

恢复备份必须同时具备原始的 `TOKENHUB_SECRET_KEY`，它用于解密已保存的厂商凭据，请单独备份该密钥。与数据库同盘的备份只是本地恢复副本，不构成容灾，请复制到其他主机。

### 升级与回滚

在新版本发布包中运行 `install.sh`。它会暂存产物、校验 checksum、停止两个服务、执行**带校验的升级前数据库快照**、原子切换 `current`，然后重启。上一个版本会被保留。

该快照不是可选的保险措施：启动时无条件执行 `AutoMigrate`，把 `current` 指回旧版本**并不会**回滚数据库 schema。回滚必须连同快照一起恢复。如果服务停止后仍存在 `-wal`、`-shm` 或 `-journal` 附属文件，快照会中止，因为此时数据库并未被干净关闭。

### 卸载

```bash
sudo /opt/tokenhub/current/uninstall.sh            # 保留 /etc/tokenhub 与 /var/lib/tokenhub
sudo /opt/tokenhub/current/uninstall.sh --purge    # 同时删除数据库和所有本地备份
```

### 对外暴露服务

与 Compose 部署一致，两个服务默认监听所有网卡。除单机试用外，请置于带 TLS 的反向代理之后，用防火墙限制端口，并把 `TOKENHUB_TRUSTED_PROXY_CIDRS` 设为代理地址。

`frontend.env` 中的 `TOKENHUB_API_BASE_URL` 会被渲染进页面并由**浏览器**调用，而不是由 Node 进程调用：

- `http://127.0.0.1:8080` 只在服务器本机打开控制台时有效。
- 通过 HTTPS 提供的控制台无法调用 HTTP 的 API，浏览器会拦截混合内容。
- `TOKENHUB_CORS_ALLOWED_ORIGINS` 必须写控制台的精确 origin —— 协议、主机、端口，且不带路径。
- 管理会话会把该地址缓存在浏览器 local storage 中，因此修改该值不会影响已有会话，需退出登录或清除站点数据。

## 后端环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TOKENHUB_ENV` | `prod` | 运行环境标识 |
| `TOKENHUB_HTTP_ADDR` | `:8080` | 后端监听地址 |
| `TOKENHUB_PUBLIC_BASE_URL` | `http://localhost:8080` | 展示给用户的后端地址 |
| `TOKENHUB_RELEASE_REPOSITORY` | `astaxie/TokenHub` | 版本检查使用的可信公开 GitHub 仓库，格式为 `owner/repository` |
| `TOKENHUB_DEPLOYMENT_TYPE` | 编译期取值 | 覆盖二进制中编译的部署类型：`source`、`container` 或 `native`。Compose 文件设置为 `container` |
| `TOKENHUB_MANAGED_UPDATES` | `false` | 允许容器部署执行在线更新与回退；原生部署始终允许 |
| `TOKENHUB_INSTALL_ROOT` | `/opt/tokenhub` | 托管 Release 在线更新与回退使用的安装根目录 |
| `TOKENHUB_TRUSTED_PROXY_CIDRS` | 空 | 允许提供 `X-Forwarded-For` 的代理 IP 或 CIDR，逗号分隔 |
| `TOKENHUB_CORS_ALLOWED_ORIGINS` | 公网地址 | 允许调用后端的浏览器 Origin，逗号分隔 |
| `TOKENHUB_ADMIN_TOKEN` | `change-me-tokenhub-admin-token` | Admin API 启动访问 Token |
| `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` | `change-me-tokenhub-admin-password` | 初始 `admin` 用户密码；生产启动前必须修改 |
| `TOKENHUB_SECRET_KEY` | `change-me-tokenhub-secret-key` | 后端密钥 |
| `TOKENHUB_DATABASE_URL` | `sqlite:///app/data/tokenhub.db` | 容器内 SQLite 数据库路径 |
| `TOKENHUB_DB_HOST` | 空 | PostgreSQL 主机。设置后改用 `TOKENHUB_DB_*` 各字段拼装 DSN，而不是 `TOKENHUB_DATABASE_URL`，可避免密码含 `#`、`?`、`/`、`%` 时的 URL 编码问题。两者同时设置时仍以 `TOKENHUB_DATABASE_URL` 优先 |
| `TOKENHUB_DB_PORT` | `5432` | PostgreSQL 端口；仅在设置了 `TOKENHUB_DB_HOST` 时生效 |
| `TOKENHUB_DB_USER` | 空 | PostgreSQL 用户名；仅在设置了 `TOKENHUB_DB_HOST` 时生效 |
| `TOKENHUB_DB_PASSWORD` | 空 | PostgreSQL 密码；仅在设置了 `TOKENHUB_DB_HOST` 时生效 |
| `TOKENHUB_DB_NAME` | 空 | PostgreSQL 数据库名；仅在设置了 `TOKENHUB_DB_HOST` 时生效 |
| `TOKENHUB_DB_SSLMODE` | `disable` | PostgreSQL sslmode；仅在设置了 `TOKENHUB_DB_HOST` 时生效 |
| `TOKENHUB_SQLITE_BACKUP_DIR` | `/app/data/backups` | 备份目录 |
| `TOKENHUB_MODEL_CATALOG_FILE` | `/opt/tokenhub/current/catalog/model-catalog.yaml` | 托管部署中的标准模型目录文件 |
| `TOKENHUB_PROVIDER_CATALOG_FILE` | `/opt/tokenhub/current/catalog/provider-catalog.json` | 托管部署中的 Provider 模板与候选模型目录文件 |
| `TOKENHUB_SEED_DEMO` | `false` | 是否写入演示数据 |
| `TOKENHUB_RESOURCE_FAILURE_THRESHOLD` | `3` | Provider 资源进入冷却前的失败阈值 |
| `TOKENHUB_RESOURCE_COOLDOWN_SECONDS` | `300` | Provider 资源进入冷却后获得半开重试前的基础等待秒数 |
| `TOKENHUB_RESOURCE_COOLDOWN_MAX_SECONDS` | `3600` | 反复恢复失败时指数退避的上限秒数 |
| `TOKENHUB_METRICS_ENABLED` | `false` | 采集 Prometheus 指标并提供 `GET /metrics` |
| `TOKENHUB_METRICS_TOKEN` | 空 | `/metrics` 的 Bearer 令牌；留空时回落到管理员令牌 |
| `TOKENHUB_METRICS_PROJECT_LABEL` | `false` | 为网关指标添加 `project_id` 标签，会按项目数放大时间序列数量 |
| `TOKENHUB_IN_FLIGHT_LEASE_TTL_SECONDS` | `300` | 集群并发租约的过期时间及续租周期基准 |
| `TOKENHUB_CLUSTER_LOCK_TTL_SECONDS` | `180` | 集群协调锁的过期时间及续租周期基准 |
| `TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS` | `150` | 停机时等待在途请求完成的最长秒数 |
| `TOKENHUB_STOP_GRACE_PERIOD` | `180s` | Docker 强制停止后端前的 Compose 宽限时间 |
| `TOKENHUB_CACHE_AFFINITY_ENABLED` | `false` | 将同一会话固定到同一个上游账号，使上游 prompt cache 持续命中。默认关闭，因为它会改变路由行为 |
| `TOKENHUB_CACHE_AFFINITY_MODELS` | 空 | 逗号分隔的模型灰度名单；留空表示对全部模型生效 |
| `TOKENHUB_CACHE_AFFINITY_ALLOW_USER_SCOPE` | `false` | 是否接受用户级标识作为亲和键。默认关闭，因为同一用户的并发会话会共享取值、全部落到同一个账号 |
| `TOKENHUB_IMAGE_STORAGE_DIR` | `data/images` | 生成图片资产的存放目录 |
| `TOKENHUB_IMAGE_WORKER_CONCURRENCY` | `2` | 消费图片生成队列的工作协程数量 |
| `TOKENHUB_IMAGE_QUEUE_CAPACITY` | `64` | 队列中允许排队的图片任务上限 |
| `TOKENHUB_IMAGE_JOB_TIMEOUT_SECONDS` | `300` | 单个图片生成任务的超时时间，超时判定为失败 |
| `TOKENHUB_IMAGE_CAPABILITY_RETRY_SECONDS` | `86400` | 被标记为不支持图片生成的供应商资源，隔多久重新探测一次 |

## 前端环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TOKENHUB_API_BASE_URL` | `http://localhost:8080` | 前端服务在运行时读取的后端 Admin API 地址 |
| `NEXT_PUBLIC_API_BASE_URL` | 空 | 已弃用的兼容回退配置，需要迁移到 `TOKENHUB_API_BASE_URL` |

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

## 目录文件

发布的托管镜像和原生安装包都包含对应版本的 `data/model-catalog.yaml` 与 `data/provider-catalog.json`。它们会随其余 Release 一起激活到 `/opt/tokenhub/current/catalog/`，确保后端程序和两类目录来自同一版本。Provider 目录由 PublicProviderConf 数据迁入并随仓库维护，TokenHub 运行时不会拉取远端目录数据。

需要使用自定义模型目录时，显式指定挂载文件：

```bash
./deploy/install.sh --model-catalog /absolute/path/to/model-catalog.yaml
```

自定义文件会覆盖镜像内的候选模板目录，其版本需要与 `TOKENHUB_IMAGE_TAG` 分别管理。更新文件后，重启后端容器，并在管理后台「模型目录」的「候选模板库」页签确认结果。

更新当前配置的目录文件后，可以重启后端，也可以在管理后台「模型目录」的「候选模板库」页签点击「恢复候选模板」。该操作会刷新参考元数据、保留自定义对外模型，但不会发布任何模板。

`data/model-catalog.yaml` 提供候选模板的参考元数据，它不是路由准入清单，也不会发布模型。`data/provider-catalog.json` 提供 Provider 模板，以及在 Provider 配置中可选择的候选上游模型。引入选中项只会创建持久化的 Provider 模型库存；选择发布时，还会创建或复用对外模型并添加启用映射。`GET /v1/models` 只返回启用且至少存在一条启用路由的对外模型；配置 API Key 模型白名单时还会进一步过滤。如需使用自定义 Provider 目录，将 `TOKENHUB_PROVIDER_CATALOG_FILE` 指向具有相同 `providers` 结构的本地 JSON 文件。

## 反向代理

生产环境建议使用 HTTPS，并转发：

- 管理后台流量到前端服务。
- `/v1/*` 和 `/api/admin/*` 流量到后端服务。

长文本生成和流式响应可能耗时较长，请合理设置请求体大小和超时时间。

存活探针使用 `/livez`，就绪探针使用 `/readyz`。数据库不可用时，`/readyz` 和向后兼容的 `/healthz` 会返回 `503`。

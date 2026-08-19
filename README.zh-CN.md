<p align="center">
  <img src="frontend/public/brand/tokenhub-logo.png" alt="TokenHub" width="96" />
</p>

<h1 align="center">TokenHub</h1>

<p align="center">
  TokenHub 让企业通过一个私有化网关统一接入和治理 AI 模型，让每一次调用都可控、可追踪、可归因。
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License" /></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26" />
  <img src="https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white" alt="Docker Compose" />
  <img src="https://img.shields.io/badge/OpenAI-Compatible-10A37F" alt="OpenAI Compatible" />
</p>

<p align="center">
  <a href="README.md">English</a> | 简体中文 | <a href="README.ja.md">日本語</a>
</p>

## 支持的 Provider

> [!TIP]
> **支持接入 Codex 订阅：**可将 OpenAI Codex 订阅账号接入 TokenHub，与 API Provider 一样通过统一网关进行模型服务与治理。[查看 Codex 接入指南 →](docs/zh-CN/codex-tokenhub-profile-quick-start.md)

TokenHub 原生适配 Codex 订阅、OpenAI、Azure OpenAI、Anthropic、Gemini、DeepSeek、Qwen 和本地模型，并内置 150+ Provider 模板，也支持自定义 OpenAI-Compatible 上游。常用接入包括：

<p align="center">
  <img src="docs/assets/provider-showcase.svg" alt="TokenHub 常用 Provider 接入，包括 Codex 订阅、OpenAI、Anthropic、Google Gemini、DeepSeek、Qwen、Llama 和自定义上游。" width="100%">
</p>

Provider 模板会优先使用对应的原生适配器，其余模板通过 OpenAI-Compatible 接口接入；实际可用模型和能力以对应上游服务及账号权限为准。

## 产品截图

<p align="center">
  <img src="docs/assets/screenshots/tokenhub-tour.webp" alt="TokenHub 产品导览：登录、概览、接口文档、Provider 渠道、模型目录、路由策略、用量统计和系统设置" width="100%">
</p>

## 围绕三大角色设计

TokenHub 将日常模型使用、团队治理和平台运维拆成清晰的角色入口，让企业用户只看到和自己职责相关的工作流。

| 角色 | 工作台重点 | 指南 |
| --- | --- | --- |
| 普通用户 | 查看可用模型、创建项目 Key、调用模型 API、查看个人用量 | [普通用户指南](docs/zh-CN/user-guide.md) |
| 团队负责人 | 管理项目空间、项目成员、项目 Key、团队报表和项目成本归因 | [团队负责人指南](docs/zh-CN/team-leader-guide.md) |
| 平台管理员 | 配置 Provider、模型目录、路由策略、身份源、RBAC、审计和成本管控 | [管理员指南](docs/zh-CN/administrator-guide.md) |

## 平台能力

- OpenAI-Compatible 模型 API：`/v1/chat/completions`、`/v1/responses`、`/v1/embeddings`；Anthropic Messages API：`/v1/messages`、`/v1/messages/count_tokens`。
- OpenAI-Compatible 生图与参考图编辑 API：`/v1/images/generations`、`/v1/images/edits`，支持异步任务和服务端图片留存；`codex-gpt-image-2` 使用 Codex 订阅额度，`gpt-image-2` 使用 OpenAI API Provider。参见[生图 API 调用与测试指南](docs/zh-CN/codex-image-generation-api.md)。
- Provider 渠道：OpenAI-Compatible、Azure OpenAI、Anthropic、Gemini、DeepSeek、Qwen、本地 vLLM/Ollama 和自定义上游。
- 模型目录和路由策略：支持优先级、权重、失败回退顺序和路由健康诊断。
- 按项目归属的 Key 管理：支持团队归属、成员权限、额度和并发限制。
- 用量统计和请求日志：可归因到用户、项目、团队、模型和成本中心。
- 身份源配置：支持 OAuth/OIDC 企业登录，并配合 RBAC 和审计追踪。
- 简洁控制台：分角色导航、全局搜索、黑白主题，以及左侧 API 导航 + 右侧详情的接口文档。
- SQLite-first 私有化部署，提供原生 systemd 和 Docker Compose 两种方式。
- PostgreSQL 支持多实例部署：通过远端 PostgreSQL 共享状态，实现前后端实例横向扩展，并提供连接池配置。参见[部署指南](docs/zh-CN/deployment.md)。
- 管理后台支持英文、中文、日文切换。
- TokenHub 还支持接入 OpenAI Codex 订阅账号资源，并通过可隔离、可恢复的 Codex Profile，让指定的本地 Codex CLI 或桌面端会话经过 TokenHub。参见 [Codex 接入指南](docs/zh-CN/codex-tokenhub-profile-quick-start.md)。
- Gemini CLI 可以直接连接 TokenHub 的 Gemini 原生接口，并使用 Codex 订阅账号提供的 GPT 模型，不需要 CCswitch。参见 [Gemini CLI 接入指南](docs/zh-CN/gemini-cli-codex-subscription.md)。

## 快速开始

Linux systemd 主机使用原生 Release：

```bash
curl -fsSL https://raw.githubusercontent.com/astaxie/TokenHub/main/deploy/native/install.sh \
  -o /tmp/tokenhub-install.sh
sudo bash /tmp/tokenhub-install.sh install
```

从仓库检出目录使用 Docker Compose：

```bash
cp deploy/.env.example deploy/.env
# 将 deploy/.env 中所有 change-me 值替换为强密钥。
./deploy/install.sh
```

访问地址：

- 管理后台：`http://localhost:3000`
- 后端 API：`http://localhost:8080`
- 健康检查：`http://localhost:8080/healthz`

初始管理员账号：

- 用户名：`admin`
- 原生安装密码：由安装脚本输出一次
- Docker 密码：`TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` 的配置值

原生安装脚本会校验 Release 文件、安装 systemd 服务，并在版本面板提供直接更新、回退和重启。默认 Docker 部署使用一个托管容器同时运行后端与管理后台，无需挂载 Docker Socket，也提供相同的直接操作。Release 完整包保存在 `tokenhub-releases` 卷中，普通重启或重建容器不会丢失页面更新结果。多实例 Docker 部署仍由运维人员通过 Compose 统一更新，避免不同副本版本不一致。完整说明见[部署指南](docs/zh-CN/deployment.md)。

## 文档

- [文档首页](docs/zh-CN/README.md)
- [整体架构](docs/zh-CN/architecture.md)
- [普通用户指南](docs/zh-CN/user-guide.md)
- [团队负责人指南](docs/zh-CN/team-leader-guide.md)
- [管理员指南](docs/zh-CN/administrator-guide.md)
- [贡献指南](CONTRIBUTING.zh-CN.md)

## Contributors

TokenHub 的演进离不开真实企业场景里的使用反馈、网关集成、文档完善、测试补充和持续维护。感谢每一位让项目变得更可靠的人。

<!-- readme: contributors -start -->

<table>
  <tr>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/astaxie">
        <img src="https://avatars.githubusercontent.com/u/233907?v=4" width="80px" alt="astaxie" />
        <br /><sub><b>astaxie</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/legendtkl">
        <img src="https://avatars.githubusercontent.com/u/2370761?v=4" width="80px" alt="legendtkl" />
        <br /><sub><b>legendtkl</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/Mr0bean">
        <img src="https://avatars.githubusercontent.com/u/19573968?v=4" width="80px" alt="Mr0bean" />
        <br /><sub><b>Mr0bean</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/deepjerry-ai">
        <img src="https://avatars.githubusercontent.com/u/262369278?v=4" width="80px" alt="deepjerry-ai" />
        <br /><sub><b>deepjerry-ai</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/cngump">
        <img src="https://avatars.githubusercontent.com/u/108251?v=4" width="80px" alt="cngump" />
        <br /><sub><b>cngump</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/bailu-ZZ">
        <img src="https://avatars.githubusercontent.com/u/311096537?v=4" width="80px" alt="bailu-ZZ" />
        <br /><sub><b>bailu-ZZ</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/coldbrewtea">
        <img src="https://avatars.githubusercontent.com/u/6879314?v=4" width="80px" alt="coldbrewtea" />
        <br /><sub><b>coldbrewtea</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/samz406">
        <img src="https://avatars.githubusercontent.com/u/3055810?v=4" width="80px" alt="samz406" />
        <br /><sub><b>samz406</b></sub>
      </a>
    </td>
  </tr>
  <tr>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/wangle201210">
        <img src="https://avatars.githubusercontent.com/u/19949348?v=4" width="80px" alt="wangle201210" />
        <br /><sub><b>wangle201210</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/CLukeLi">
        <img src="https://avatars.githubusercontent.com/u/252523101?v=4" width="80px" alt="CLukeLi" />
        <br /><sub><b>CLukeLi</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/imaben">
        <img src="https://avatars.githubusercontent.com/u/3390195?v=4" width="80px" alt="imaben" />
        <br /><sub><b>imaben</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/myssl">
        <img src="https://avatars.githubusercontent.com/u/27838738?v=4" width="80px" alt="myssl" />
        <br /><sub><b>myssl</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/exgliuzhi">
        <img src="https://avatars.githubusercontent.com/u/6261701?v=4" width="80px" alt="exgliuzhi" />
        <br /><sub><b>exgliuzhi</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/hoorayman">
        <img src="https://avatars.githubusercontent.com/u/73151874?v=4" width="80px" alt="hoorayman" />
        <br /><sub><b>hoorayman</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/debin-ge">
        <img src="https://avatars.githubusercontent.com/u/21329997?v=4" width="80px" alt="debin-ge" />
        <br /><sub><b>debin-ge</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/ocass-chen">
        <img src="https://avatars.githubusercontent.com/u/172055494?v=4" width="80px" alt="ocass-chen" />
        <br /><sub><b>ocass-chen</b></sub>
      </a>
    </td>
  </tr>
  <tr>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/AnxForever">
        <img src="https://avatars.githubusercontent.com/u/130662349?v=4" width="80px" alt="AnxForever" />
        <br /><sub><b>AnxForever</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/yujiewanwan">
        <img src="https://avatars.githubusercontent.com/u/268286250?v=4" width="80px" alt="yujiewanwan" />
        <br /><sub><b>yujiewanwan</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/lxm">
        <img src="https://avatars.githubusercontent.com/u/1918195?v=4" width="80px" alt="lxm" />
        <br /><sub><b>lxm</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/susunola">
        <img src="https://avatars.githubusercontent.com/u/38539169?v=4" width="80px" alt="susunola" />
        <br /><sub><b>susunola</b></sub>
      </a>
    </td>
  </tr>
</table>

<!-- readme: contributors -end -->

<p align="center">
  <a href="https://github.com/astaxie/TokenHub/graphs/contributors">查看全部贡献者</a>
  ·
  <a href="CONTRIBUTING.zh-CN.md">参与贡献</a>
</p>

## Star History

<a href="https://www.star-history.com/?repos=astaxie%2Ftokenhub&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=astaxie/tokenhub&type=date&theme=dark&legend=top-left&sealed_token=hWH3kDnssTf49CCLxzq3NVqEp0WTL-HFhsdpQJJz1DUuZt0D-nu1jgXLnhCxrUrMYujv6IJJk12B1wCp5qiU2bU_J03ECSYvb3Y9Pv-gqX7RuwS4SehRrQ" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=astaxie/tokenhub&type=date&legend=top-left&sealed_token=hWH3kDnssTf49CCLxzq3NVqEp0WTL-HFhsdpQJJz1DUuZt0D-nu1jgXLnhCxrUrMYujv6IJJk12B1wCp5qiU2bU_J03ECSYvb3Y9Pv-gqX7RuwS4SehRrQ" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=astaxie/tokenhub&type=date&legend=top-left&sealed_token=hWH3kDnssTf49CCLxzq3NVqEp0WTL-HFhsdpQJJz1DUuZt0D-nu1jgXLnhCxrUrMYujv6IJJk12B1wCp5qiU2bU_J03ECSYvb3Y9Pv-gqX7RuwS4SehRrQ" />
 </picture>
</a>

## License

TokenHub 采用 [Apache License 2.0](LICENSE) 协议。

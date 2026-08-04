# TokenHub 迁移框架

TokenHub 迁移框架提供可重复、幂等的工作流，将竞争 AI 网关迁入 TokenHub。

## 当前状态

当前分支已经提供可工作的 canonical bundle、同时支持基于 store 与远端 Admin API 的 TokenHub sink、LiteLLM 文件型适配器，以及可执行 `extract`、`plan`、`apply`、`verify`、`rollback` 的 CLI 流程。

## 文档

- [架构](./architecture.md)
- [Bundle 规范](./bundle-schema.md)
- [LiteLLM 适配器](./litellm.md)
- [CLI](./cli.md)
- [E2E](./e2e.md)

详细实现与完整命令说明以英文文档 `docs/migration/` 为准。

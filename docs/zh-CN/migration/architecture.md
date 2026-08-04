# 迁移框架架构

## 流水线概览

```
Source Gateway  ──extract──▶  CanonicalMigrationBundle  ──apply──▶  TokenHub
(LiteLLM, ...)               (版本化 JSON，无明文密钥)             (Admin API / DB)
```

- **Source Adapter** — 读取竞品网关配置，输出标准化 bundle。
- **Sink** — 消费 bundle 并通过 Admin API 或直连数据库执行幂等 upsert，同时写入 checkpoint 以支持回滚。
- **CLI** (`tokenhub-migrate`) — 暴露 `extract`、`plan`、`apply`、`verify`、`rollback` 子命令。

## 目录结构

```
backend/
  cmd/tokenhub-migrate/          # CLI 入口
  internal/migration/
    bundle/                      # 中间格式定义 + JSON Schema
    cli/                         # cobra 命令注册
    sink/tokenhub/               # StoreSink（直连 DB）与 HTTPSink（远端 API）
    source/litellm/              # LiteLLM adapter
```

## 扩展

添加新的 Source Adapter 需实现 `source.Extractor` 接口并通过 `source.Register()` 注册。

完整英文版请参阅 `docs/migration/architecture.md`。

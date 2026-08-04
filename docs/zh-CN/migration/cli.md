# CLI 参考

## 安装

```bash
cd backend && go build -o tokenhub-migrate ./cmd/tokenhub-migrate/
```

## 命令概览

| 命令 | 说明 |
|------|------|
| `extract litellm --from <yaml> --out <json>` | 从 LiteLLM 配置提取 bundle |
| `plan --bundle <json>` | Dry-run，显示将会创建/更新/跳过的资源数 |
| `apply --bundle <json>` | 执行迁移；将回滚 checkpoint 写入 `<bundle>.checkpoint.json`，新生成的 API key 明文写入 `<bundle>.new-keys.json`（均为 0600，可用 `--checkpoint-out` / `--new-keys-out` 覆盖路径） |
| `verify --bundle <json>` | 校验迁移后的一致性 |
| `rollback --checkpoint <json>` | 回滚已创建的资源 |

`plan` / `apply` / `verify` / `rollback` 必须指定远端 TokenHub 目标（`--to` 或 `TOKENHUB_API`），否则以退出码 5 拒绝执行。

> 注意：`apply` 通过 Admin CSV 导入接口创建用户，该接口要求目标实例已配置活跃的邮件通知通道。若未配置，包含新建用户的 bundle 会在 apply 时失败；每个新导入的用户会在 apply 过程中收到一封密码重置邮件。
>
> 远端 apply 不是事务操作。如果前面的资源已经变更、后续资源处理失败，命令会先保存部分回滚 checkpoint 和已生成的一次性 API key，再以退出码 5 结束。

## 通用参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `--secret-source` | 密钥解析来源：`env` 或 `file` | `env` |
| `--secret-file` | 配合 `--secret-source=file` 使用的 `key=value` 文件 | — |
| `--id-strategy` | ID 生成策略：`stable`、`prefixed` 或 `source` | `prefixed` |

## 环境变量

| 变量 | 说明 |
|------|------|
| `TOKENHUB_API` | TokenHub Admin API 地址（必需，或用 `--to` 指定） |
| `TOKENHUB_ADMIN_TOKEN` | Admin API 认证 token |

完整命令用法请参阅 `docs/migration/cli.md`。

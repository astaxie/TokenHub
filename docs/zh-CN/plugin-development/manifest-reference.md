# 插件 Manifest 参考

Language: [English](../../plugin-development/manifest-reference.md) | 简体中文 | [日本語](../../ja/plugin-development/manifest-reference.md)

每个包的根目录有且只有一个 `plugin.yaml`。TokenHub 会在注册或执行任何能力之前验证它。

| 字段 | 用途 |
| --- | --- |
| `schema_version` | Manifest Schema 版本；Plugin API v1 使用 `1` |
| `id` | 稳定且全局唯一的插件标识 |
| `version` | 插件包版本 |
| `tokenhub.plugin_api` | 运行时契约主版本，当前为 `v1` |
| `kinds` | `provider`、`extension`、`admin_ui` 或 `sim` |
| `placement` | `gateway_chain`、`background`、`presentation` 或过渡性 `management_action` |
| `entry` | 后端命令与协议，或前端 Schema 路径 |
| `capabilities` | Provider 类型、Hook、Job、Action 或 UI/模板声明 |
| `permissions` | 最小权限数据读写声明 |
| `distribution` | 仓库、主页、许可证、版本、checksum 和签名元数据 |

能力 ID 和插件 ID 都是兼容性契约。除非迁移会显式保留现有 Provider、路由、资源和配置引用，否则不要在更新中重命名。

后端命令使用包内相对路径和 `stdio-json-v1` 协议。路径不能逃离插件包；包含 symlink、多个 Manifest 或不兼容声明的包会被拒绝。

Schema、stage 策略和兼容性规则见[完整指南](guide.md)。

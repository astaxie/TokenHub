# 插件 Manifest 参考

Language: [English](../../plugin-development/manifest-reference.md) | 简体中文 | [日本語](../../ja/plugin-development/manifest-reference.md)

每个包的根目录有且只有一个 `plugin.yaml`。TokenHub 会在注册或执行任何能力之前验证它。

| 字段 | 用途 |
| --- | --- |
| `schema_version` | Manifest Schema 版本；Plugin API v2 使用 `2`，v1 适配器接受 `1` |
| `id` | 稳定且全局唯一的插件标识 |
| `version` | 插件包版本 |
| `summary` | v2 必填的普通用户用途说明 |
| `category` | 一个主分类：`provider_integration`、`request_pipeline`、`ui_template` 或 `automation` |
| `tokenhub.plugin_api` | Manifest/运行时契约主版本；当前为 `v2`，同时通过适配器兼容 `v1` |
| `dependencies` | 可选的插件 ID 与版本约束 |
| `settings.scopes` | 声明真实可编辑设置；没有设置时不要填写 |
| `kinds` | `provider`、`extension`、`admin_ui` 或 `sim` |
| `placement` | `gateway_chain`、`background`、`presentation` 或过渡性 `management_action` |
| `entry` | 后端命令与协议，或前端 Schema 路径 |
| `capabilities` | Provider 类型、Hook、Job、Action 或 UI/模板声明 |
| `permissions` | 最小权限数据读写声明 |
| `distribution` | 仓库、主页、许可证、版本、checksum 和签名元数据 |

能力 ID 和插件 ID 都是兼容性契约。除非迁移会显式保留现有 Provider、路由、资源和配置引用，否则不要在更新中重命名。

包内相对路径与 `stdio-json-v1` 传输协议定义了外部后端命令契约；传输协议名称与 Plugin API v2 相互独立。路径不能逃离插件包；包含 symlink、多个 Manifest 或不兼容声明的包会被拒绝。当前运行时无法强制执行所需的宿主级进程、网络和资源隔离，因此会采取失败关闭策略，在启动前拒绝所有动态加载的外部命令。该限制不影响进程内置插件或声明式界面贡献。

Schema、stage 策略和兼容性规则见[完整指南](guide.md)。

# Provider 插件

Language: [English](../../plugin-development/provider-plugins.md) | 简体中文 | [日本語](../../ja/plugin-development/provider-plugins.md)

Provider 插件把 TokenHub 连接到上游模型服务或订阅账户。最小契约可从 [`examples/provider-mock-go`](../../../plugin-devkit/examples/provider-mock-go) 开始，更完整的 operation 可参考 Kimi 和 GLM Example。

TokenHub 会把配置的 `provider-catalog.json` 中每个条目都表示为一个内置 Provider 插件。目录插件负责厂商身份、新增页元数据、详情页和启停状态；协议插件负责 `OpenAI-Compatible` 等可执行适配器。因此，多个目录插件可以共享同一个协议适配器，不需要复制运行代码。禁用目录插件会从新增 Provider 页面移除对应厂商，重新启用后立即恢复。

插件需声明 Provider 类型、资源类型、支持的 operation、协议策略、模型发现、凭证范围和必要的 Admin UI 贡献。`ServeProvider` 通过 `stdio-json-v1` 接收投影后的 Provider、资源、模型、请求和凭证数据，不能直接访问 Core 存储。

生产实现要求：

- 把所有固定 Example 响应替换为可取消的真实上游调用。
- 不在 stdout、错误、审计元数据或 fixture 中输出 secret。
- 测试 chat、streaming、错误、usage、timeout、取消、发现和协议转换。
- 将周期性配额刷新和账户同步放入[后台任务](background-jobs.md)。
- 兼容版本间保持 Provider 类型和能力 ID 稳定。

先用 `tokenhub-plugin-test provider` 验证，再通过真实 TokenHub 模型路由和 API Key 做集成测试。完整投影契约见[完整指南](guide.md)。

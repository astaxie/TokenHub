# TokenHub 插件开发

Language: [English](../../plugin-development/README.md) | 简体中文 | [日本語](../../ja/plugin-development/README.md)

这个目录是 TokenHub 插件开发的统一文档入口。可执行 SDK、契约测试工具和参考包位于 [`plugin-devkit`](../../../plugin-devkit/README.md)；安装后的插件位于 `TOKENHUB_PLUGIN_DIR`。线上 Marketplace 是独立的分发索引，不是这个 Devkit。

## 从这里开始

| 目标 | 文档 |
| --- | --- |
| 创建并测试第一个插件包 | [快速开始](getting-started.md) |
| 理解 `plugin.yaml` | [Manifest 参考](manifest-reference.md) |
| 接入上游模型服务 | [Provider 插件](provider-plugins.md) |
| 介入请求处理链 | [网关 Hook](gateway-hooks.md) |
| 添加主题、布局和声明式面板 | [界面模板](ui-templates.md) |
| 执行定时或运维工作 | [后台任务](background-jobs.md) |
| 打包 ZIP 并发布版本 | [打包与发布](packaging-and-release.md) |
| 阅读所有契约和迁移细节 | [完整架构与开发指南](guide.md) |

## 目录边界

```text
docs/plugin-development/     文档与导航
plugin-devkit/               SDK、契约工具、fixture 和 Examples
your-plugin-repository/      真实插件源码与发布流程
TOKENHUB_PLUGIN_DIR/         TokenHub 实例已安装的插件包
marketplace index            发布版本、URL、checksum 和签名
```

请用 Devkit 开发插件。在替换 fixture 行为并添加 Provider 专属测试前，不要把 `examples/` 当成生产集成。

# TokenHub 插件快速开始

Language: [English](../../plugin-development/getting-started.md) | 简体中文 | [日本語](../../ja/plugin-development/getting-started.md)

使用 [`plugin-devkit`](../../../plugin-devkit/README.md) 学习并验证 Plugin API v2。Devkit 仍可识别 v1 manifest 以保持兼容；其中的 `examples/` 是契约 fixture，不是生产集成。

当前 TokenHub 运行时不支持执行外部命令。下述流程用于开发和测试未来运行时所需的 `stdio-json-v1` 契约；安装带命令的包只会完成校验、保存和文件检查，随后记录为 `failed_startup` 生命周期状态。

## 1. 验证 Devkit

```bash
cd plugin-devkit
go test ./...
go run ./cmd/tokenhub-plugin-test provider \
  --package "$PWD/examples/provider-mock-go"
```

## 2. 创建真实插件

在独立目录或仓库中开发真实插件。复制最接近的 Example，然后同步替换插件 ID、能力 ID、实现、fixture 和分发元数据。只声明实现真正需要的权限。

Go 插件应初始化自己的 module，并固定经过审核的 Devkit 版本或 commit：

```bash
go mod init example.com/your-plugin
go get github.com/astaxie/TokenHub/plugin-devkit@<approved-version>
```

SDK 的 import 路径是 `github.com/astaxie/TokenHub/plugin-devkit/sdk/go/tokenhubplugin`。

```text
your-plugin/
├── plugin.yaml
├── go.mod              # Go module 与固定版本的 Devkit 依赖
├── bin/your-plugin
├── ui/                 # 可选 Schema 或资源
└── contract-tests/     # 插件自有测试
```

## 3. 验证契约

```bash
go run ./cmd/tokenhub-plugin-test provider --package /path/to/your-plugin
go run ./cmd/tokenhub-plugin-test hook --package /path/to/your-plugin
go run ./cmd/tokenhub-plugin-test background --package /path/to/your-plugin
go run ./cmd/tokenhub-plugin-test action --package /path/to/your-plugin
```

## 4. 打包并检查

构建可执行文件，创建只包含一个 `plugin.yaml` 的 ZIP 并计算 SHA-256。可以从「插件管理 > 浏览插件 > 手动安装」安装，以验证包是否可接受并检查 manifest 与文件。TokenHub 会把通过校验的包写入 `TOKENHUB_PLUGIN_DIR` 并重新加载生命周期状态；带 `entry.backend.command` 的包会显示为「启动失败」，且不会注册任何 Provider、Hook、任务或 Action 能力。命令行为必须通过 Devkit 契约命令验证，不能通过真实 TokenHub Provider 或网关请求验证。仅仅放在 `plugin-devkit/examples/` 下不会被发现。

继续阅读 [Manifest 参考](manifest-reference.md) 和[打包与发布](packaging-and-release.md)。

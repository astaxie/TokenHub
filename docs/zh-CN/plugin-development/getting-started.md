# TokenHub 插件快速开始

Language: [English](../../plugin-development/getting-started.md) | 简体中文 | [日本語](../../ja/plugin-development/getting-started.md)

使用 [`plugin-devkit`](../../../plugin-devkit/README.md) 学习并验证 Plugin API v1。其中的 `examples/` 是契约 fixture，不是生产集成。

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

## 4. 打包并安装

构建可执行文件，创建只包含一个 `plugin.yaml` 的 ZIP，计算 SHA-256，再从 TokenHub 的“安装插件”页面安装。TokenHub 会把包写入 `TOKENHUB_PLUGIN_DIR`；仅仅放在 `plugin-devkit/examples/` 下不会被运行时加载。

继续阅读 [Manifest 参考](manifest-reference.md) 和[打包与发布](packaging-and-release.md)。

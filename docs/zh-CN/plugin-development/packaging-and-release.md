# 打包与发布

语言：[English](../../plugin-development/packaging-and-release.md) | 简体中文 | [日本語](../../ja/plugin-development/packaging-and-release.md)

生产插件应放在独立的源码目录或仓库中。`plugin-devkit/examples/` 只包含用于开发和契约测试的示例；它既不是安装来源，也不是 TokenHub 插件市场。

## 构建插件包

发布 ZIP 必须只包含一个可发现的 `plugin.yaml`，并在需要时包含运行入口，以及使用包内相对路径引用的 Schema 或资源。manifest 可以位于压缩包根目录，也可以位于唯一的一层顶级目录中。

不要打包符号链接、凭据、`.env` 文件、本地数据库、日志、版本控制元数据或构建缓存。`entry.backend.command` 必须是相对于插件包根目录的路径，并保留可执行权限。

示例：

```bash
cd plugin-devkit
go build -o examples/background-heartbeat-go/bin/background-heartbeat-go \
  ./examples/background-heartbeat-go
cd examples/background-heartbeat-go
zip -r ../../../background-heartbeat-go.zip plugin.yaml bin
cd ../../..
shasum -a 256 background-heartbeat-go.zip
```

创建 ZIP 前，应对插件目录运行对应的 Devkit 契约命令。最终压缩包也必须通过 TokenHub 安装测试，因为目录布局和可执行权限同样属于发布契约。

## 版本与发布

每次制品内容发生变化，都应使用新的语义化插件版本。保持插件 ID 和已发布的能力 ID 稳定。发布信息应记录 TokenHub 与 Plugin API 兼容范围、权限变化、校验和、发布说明和下载 URL。

TokenHub Marketplace 是远程 HTTPS JSON 索引。它描述插件及其发布版本，并指向不可变的 ZIP 制品。它与 `plugin-devkit`、`TOKENHUB_PLUGIN_DIR` 都是不同的概念。Marketplace 记录至少应提供制品 URL 和小写 SHA-256 校验和；签名发布还可以提供 Ed25519 签名 URL 和密钥 ID。

不要让同一个发布 URL 对应不同的文件内容。校验和和签名保护的是确切的压缩包，因此重新构建已发布版本时，必须使用新的版本号和制品。

## 安装与验证

管理员可以从**插件管理 > 安装插件**选择 Marketplace 版本、填写直接 URL 或上传 ZIP。安装前应检查兼容性、校验和、信任信息和权限差异。使用直接 URL 安装时必须提供插件包校验和。

TokenHub 会将通过验证的插件包解压到 `TOKENHUB_PLUGIN_DIR`。新增或修改运行能力的插件可能进入 `pending_restart` 状态；确认新版本是否生效前，需要先重启后端。之后应验证：

- 在详情页检查版本、兼容性和信任状态
- 在文件页核对文件清单与预期包内容
- 在详情页核对已注册的任务、Hook 和 UI 贡献，并在文件页的 `plugin.yaml` 中核对权限
- 先使用非生产凭据验证真实 Provider 或网关行为

更新插件时也要重复同样的审查和验证。若更新会改变持久化数据，应先备份相关 TokenHub 状态，并保留上一版本的不可变 ZIP，以便受控回退。

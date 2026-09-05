# 后台任务插件

语言：[English](../../plugin-development/background-jobs.md) | 简体中文 | [日本語](../../ja/plugin-development/background-jobs.md)

配额刷新、账号同步、清理、报表和健康检查等不应进入模型请求链路的工作，适合通过后台任务插件实现。任务可以按照声明的计划运行，也可以由管理员从插件管理界面手动触发。

## 声明任务

将 `extension` 声明为类型、`background` 声明为挂载位置，并添加一个或多个 `capabilities.background_jobs` 条目：

```yaml
kinds: [extension]
placement: [background]
entry:
  backend:
    protocol: stdio-json-v1
    command: bin/your-plugin
capabilities:
  background_jobs:
    - id: quota.refresh
      title: Refresh quota
      capability: provider.quota.refresh
      subject: example-provider
      schedule: "@startup"
      timeout_millis: 5000
      max_concurrency: 1
      retry:
        max_attempts: 2
        backoff_millis: 1000
      input_schema:
        type: object
        required: [resource_id]
        properties:
          resource_id:
            type: string
      output_schema:
        type: object
```

插件 ID 和任务 ID 都是稳定的兼容性契约。输入应尽量小，使用明确的 JSON Schema，限制超时与并发，并确保任务部分完成后仍能安全重试。

## 实现处理器

`ServeBackgroundJob` 从标准输入读取一次 `stdio-json-v1` 调用，并将一个结构化结果写到标准输出。调用内容包含插件 ID、任务 ID、触发方式、操作者和负载。日志只能写到标准错误，结果必须脱敏，不能包含凭据或未经处理的 Provider 响应。

处理器应尽量保持幂等。如果任务会修改外部状态，请使用稳定的操作键，或持久化足够的状态来保证重试安全。调度计划不代表任务一定只执行一次。

## 测试任务

Devkit 提供了完整的示例和契约命令：

```bash
cd plugin-devkit
go test ./...
go run ./cmd/tokenhub-plugin-test background \
  --package "$PWD/examples/background-heartbeat-go"
```

将示例复制到独立的插件工作区，再同步修改 manifest、处理器校验、Schema 和测试。安装并重启后端后，可以在插件的**详情**页面检查已注册任务，并从后台任务扩展类型页面手动运行。

分发前请继续阅读[完整指南](guide.md)中的调用示例，以及[打包与发布](packaging-and-release.md)。

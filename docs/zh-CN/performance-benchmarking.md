# 性能基准测试

TokenHub 提供两层互补的基准测试：黑盒测试通过 OpenAI 兼容 HTTP 端点评估 TokenHub 或对比其他网关；进程内 Go 基准用于分离 TokenHub 路由与治理成本，并统计内存分配。两者都不访问真实模型 Provider。

## 指标含义

黑盒 Runner 输出实际 RPS、成功率、响应字节数、端到端延迟 P50/P95/P99，以及流式首字节时间 TTFT。“估算网关开销”按下式计算：

```text
max(0, 客户端端到端延迟 - 配置的 fake upstream 延迟)
```

该估算包含 HTTP 传输和调度噪声，不是网关内部计时。不能将它直接与排除 JSON 序列化或 HTTP 调用的数据对比。Bifrost 公开的微秒级开销有特定排除项；产品对比应让两个网关使用同一 Runner 和同一 upstream。

每个 JSON 结果会记录场景、commit、时间、Go 版本、操作系统、架构、CPU 型号与数量及系统内存，不保存 API Key、主机名、用户名或本地路径。

## 构建与启动可控 upstream

在仓库根目录构建：

```bash
mkdir -p .tmp
(cd backend && go build -o ../.tmp/tokenhub-benchmark ./cmd/tokenhub-benchmark)
```

CLI 包含 `mocker`、`gateway`、`run`、`check`、`summarize-go` 和 `check-go` 六个命令，分别用于启动确定性 upstream、启动零配置的内存 TokenHub 测试网关、发起带预热的定并发/定 RPS 负载、执行黑盒基线预算检查、汇总 Go 基准中位数，以及检查进程内 `ns/op`、`B/op`、`allocs/op`。Runner 为每个 prompt 添加唯一标识，避免响应缓存影响。

快速执行 TokenHub 单产品冒烟基准时，可在一个终端启动自包含网关。该 Key 只存在内存测试数据库中，但仍通过环境变量传入：

```bash
TOKENHUB_BENCHMARK_API_KEY=thk_benchmark_local \
./.tmp/tokenhub-benchmark gateway \
  --listen 127.0.0.1:18080 \
  --model benchmark-model \
  --upstream-latency 5ms
```

然后将 `run` 指向 `http://127.0.0.1:18080`。跟踪的 `benchmarks/baselines/tokenhub-local-smoke.json` 是在该方式下执行五次后选取吞吐中位数的结果。

```bash
./.tmp/tokenhub-benchmark mocker \
  --listen 127.0.0.1:18081 \
  --latency 5ms \
  --response-bytes 1024 \
  --stream-chunks 8 \
  --chunk-interval 1ms
```

在两个网关中都配置名为 `benchmark-model` 的路由，指向 `http://127.0.0.1:18081/v1`，并分别创建本地基准 API Key。`--failure-every` 可注入确定性故障，用于故障转移场景。

## 对比 TokenHub 与 Bifrost

应在同一台空闲机器上使用相同数据库类型和可观测性开关。如果两个网关会争抢 CPU，不要同时测量；多次交替顺序执行。

```bash
export TOKENHUB_BENCHMARK_TOKENHUB_URL=http://127.0.0.1:8080
export TOKENHUB_BENCHMARK_BIFROST_URL=http://127.0.0.1:8082
export TOKENHUB_BENCHMARK_TOKENHUB_API_KEY=YOUR_TOKENHUB_BENCHMARK_KEY
export TOKENHUB_BENCHMARK_BIFROST_API_KEY=YOUR_BIFROST_BENCHMARK_KEY
export TOKENHUB_BENCHMARK_MODEL=benchmark-model

./benchmarks/run-comparison.sh
```

脚本默认执行五次并交替两个目标的先后顺序，带序号的结果写入 Git 忽略的 `output/benchmarks/`。脚本顶部的 `TOKENHUB_BENCHMARK_*` 变量可调整重复次数、时长、预热、并发、输出目录、upstream 延迟和数据库/可观测性 profile。流式、Responses、Embeddings 或定 RPS 场景可直接调用 `run`。定 RPS 模式需设置 `--mode rate --rate N --concurrency 0`；`load_generator_saturated` 表示压测端达到 `--max-in-flight`，`load_generator_missed_schedule` 表示压测端未能按请求速率及时调度，两者都不是网关响应，出现时不能让延迟对比通过。流式场景的 `--upstream-latency` 应填写 upstream 总响应时间，`--upstream-ttft` 填写首字节时间；使用默认 mocker 时应设置 `--upstream-latency 13ms --upstream-ttft 5ms`。

## 运行进程内基准

```bash
./benchmarks/run-internal.sh
```

矩阵覆盖 Chat Completions、Responses、流式、故障转移、SQLite、使用相同 32 KiB 请求的 payload 审计持久化开关对照、直接的小/大 payload 审计渲染、metrics，以及不捕获/捕获 payload 的 tracing。基准使用 `ReportAllocs`，因此 Go 输出包含 `ns/op`、`B/op` 和 `allocs/op`。脚本按中位数汇总重复样本；任一指标超过 `benchmarks/internal-budget.json` 的阈值（耗时 25%、字节 15%、分配次数 10%）时会以非零状态退出。

PostgreSQL 是显式开启的，避免普通测试误访共享服务。每个基准场景都在最后回滚的事务中运行，不会保留路由或审计数据：

```bash
TOKENHUB_BENCHMARK_POSTGRES_URL='postgres://tokenhub:password@127.0.0.1:5432/tokenhub_benchmark?sslmode=disable' \
./benchmarks/run-internal.sh
```

请使用可丢弃的空数据库。建库与迁移不计入时间，请求持久化计入时间。

进程内基线按 profile 分开：默认使用 `benchmarks/baselines/internal/sqlite.json`；设置 `TOKENHUB_BENCHMARK_POSTGRES_URL` 时使用 `sqlite-postgresql.json`。只应在已提交且工作区干净的 revision 上生成或有意刷新基线：

```bash
TOKENHUB_BENCHMARK_UPDATE_BASELINE=1 ./benchmarks/run-internal.sh
```

更新 PostgreSQL profile 时需同时设置 PostgreSQL URL。若基准集合、Go 版本、操作系统、架构、CPU 型号/数量或系统内存不同，检查会拒绝对比。

## 基线与性能预算

`benchmarks/budget.json` 要求成功率不低于 99.9%、吞吐不低于基线的 90%、平均延迟回退不超过 15%、P99 回退不超过 20%。容差用于减少共享 Runner 抖动的误报，同时保留对明显回退的感知。跟踪的本地基线覆盖 Chat、Responses、流式和真实调用主候选的故障转移。

```bash
./.tmp/tokenhub-benchmark check \
  --baseline benchmarks/baselines/tokenhub-local-smoke.json \
  --current output/benchmarks/tokenhub-local-smoke.json \
  --budget benchmarks/budget.json
```

若 schema、协议、流式模式、负载模式/级别、模型、请求大小、upstream 延迟或运行时/硬件 profile 不同，检查会拒绝对比。只应在稳定机器上至少运行五次后，因预期性能变更更新基线，并保留中位数那次。

将四个当前结果以 `benchmarks/baselines/*.json` 相同文件名放入一个目录，可一次检查整套基线：

```bash
./benchmarks/check-suite.sh output/benchmarks/current
```

场景指纹还包含时长、预热、超时、最大在途请求、响应大小、流式形状和数据库/可观测性 profile。任一项不同或压测端丢弃请求时都会失败。

若成功率明显不同、压测端饱和、网关重试次数不同，或 CPU/内存压力不同，该轮对比应判为无效。需要分开看定并发吞吐、定 RPS 延迟、TTFT 和内存分配；单个最大 RPS 无法说明 TokenHub 持久化、审计、路由、metrics 和 tracing 的成本。

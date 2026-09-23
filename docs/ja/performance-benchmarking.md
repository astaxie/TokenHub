# パフォーマンスベンチマーク

TokenHub には 2 つの補完的なベンチマーク層があります。ブラックボックスベンチマークは OpenAI 互換 HTTP エンドポイントを介して TokenHub と他のゲートウェイを比較できます。プロセス内 Go ベンチマークは、TokenHub のルーティングとガバナンスのコストを分離し、メモリ割り当てを報告します。どちらも実際のモデル Provider には接続しません。

## 数値の意味

ブラックボックス Runner は、実効 RPS、成功率、レスポンスバイト数、エンドツーエンド遅延の P50/P95/P99、ストリーミング TTFT を報告します。「推定ゲートウェイオーバーヘッド」は次の式です。

```text
max(0, クライアントのエンドツーエンド遅延 - fake upstream の設定遅延)
```

この推定値には HTTP 転送とスケジューリングのノイズが含まれ、ゲートウェイ内部のタイマーではありません。JSON シリアライズや HTTP 呼び出しを除外したベンチマークと直接比較できません。Bifrost の公開されたマイクロ秒オーバーヘッドには特定の除外条件があるため、公平な製品比較では同じ Runner と upstream を使用します。

各 JSON 結果にはシナリオ、commit、時刻、Go バージョン、OS、アーキテクチャ、CPU のモデルと数、システムメモリが記録されます。API Key、ホスト名、ユーザー名、ローカルパスは保存されません。

## ツールと制御可能な upstream

リポジトリルートでビルドします。

```bash
mkdir -p .tmp
(cd backend && go build -o ../.tmp/tokenhub-benchmark ./cmd/tokenhub-benchmark)
```

CLI の `mocker`、`gateway`、`run`、`check`、`summarize-go`、`check-go` は、それぞれ決定的 upstream、ゼロ設定のインメモリ TokenHub ゲートウェイ、ウォームアップ付き固定並列数/固定 RPS 負荷、ブラックボックスのベースライン検査、Go ベンチマーク中央値の集計、プロセス内 `ns/op`・`B/op`・`allocs/op` の検査を提供します。Runner はレスポンスキャッシュを回避するため、プロンプトを一意にします。

TokenHub 単体のスモークベンチマークでは、別のターミナルで自己完結型ゲートウェイを起動します。Key はインメモリテスト DB にのみ存在しますが、引数ではなく環境変数で渡します。

```bash
TOKENHUB_BENCHMARK_API_KEY=thk_benchmark_local \
./.tmp/tokenhub-benchmark gateway \
  --listen 127.0.0.1:18080 \
  --model benchmark-model \
  --upstream-latency 5ms
```

次に `run` を `http://127.0.0.1:18080` に実行します。追跡対象の `benchmarks/baselines/tokenhub-local-smoke.json` は、この方法で 5 回実行したスループット中央値の結果です。

```bash
./.tmp/tokenhub-benchmark mocker \
  --listen 127.0.0.1:18081 \
  --latency 5ms \
  --response-bytes 1024 \
  --stream-chunks 8 \
  --chunk-interval 1ms
```

両方のゲートウェイに `benchmark-model` ルートを作成し、`http://127.0.0.1:18081/v1` に転送します。それぞれにローカルベンチマーク API Key を作成します。`--failure-every` はフェイルオーバー用の決定的障害を注入します。

## TokenHub と Bifrost の比較

同じアイドル状態のマシン上で、同じデータベースクラスとテレメトリ設定を使用します。CPU を競合する場合は両方を同時に測定せず、実行順序を入れ替えて複数回実行します。

```bash
export TOKENHUB_BENCHMARK_TOKENHUB_URL=http://127.0.0.1:8080
export TOKENHUB_BENCHMARK_BIFROST_URL=http://127.0.0.1:8082
export TOKENHUB_BENCHMARK_TOKENHUB_API_KEY=YOUR_TOKENHUB_BENCHMARK_KEY
export TOKENHUB_BENCHMARK_BIFROST_API_KEY=YOUR_BIFROST_BENCHMARK_KEY
export TOKENHUB_BENCHMARK_MODEL=benchmark-model

./benchmarks/run-comparison.sh
```

スクリプトはデフォルトで 5 回、ターゲット順を交互に実行し、番号付き結果を Git で無視される `output/benchmarks/` に保存します。反復数、期間、ウォームアップ、並列数、出力先、upstream 遅延、DB/テレメトリ profile は `TOKENHUB_BENCHMARK_*` 変数で変更できます。ストリーミング、Responses、Embeddings、固定 RPS は `run` を直接使用します。固定 RPS では `--mode rate --rate N --concurrency 0` を指定します。`load_generator_saturated` はクライアントが `--max-in-flight` に達したこと、`load_generator_missed_schedule` は要求された頻度で送信できなかったことを示します。どちらもゲートウェイのレスポンスではなく、発生した実行を合格した遅延比較として扱うことはできません。ストリーミングでは `--upstream-latency` を upstream 全体の応答時間、`--upstream-ttft` を最初のバイトまでの時間とします。デフォルト mocker では `--upstream-latency 13ms --upstream-ttft 5ms` を指定します。

## プロセス内マトリクス

```bash
./benchmarks/run-internal.sh
```

マトリクスは Chat Completions、Responses、ストリーミング、フェイルオーバー、SQLite、同一の 32 KiB リクエストによる payload 監査永続化のオン/オフ比較、小/大 payload の直接監査レンダリング、metrics、payload 取得なし/ありの tracing を含みます。`ReportAllocs` により、Go 出力に `ns/op`、`B/op`、`allocs/op` が含まれます。スクリプトは反復サンプルの中央値を集計し、`benchmarks/internal-budget.json` の閾値（時間 25%、バイト 15%、割り当て回数 10%）を超えると非ゼロで終了します。

PostgreSQL は共有サービスへの意図しない接続を防ぐため、明示的に有効化します。各ケースは最後にロールバックされるトランザクション内で実行され、ルートや監査データを残しません。

```bash
TOKENHUB_BENCHMARK_POSTGRES_URL='postgres://tokenhub:password@127.0.0.1:5432/tokenhub_benchmark?sslmode=disable' \
./benchmarks/run-internal.sh
```

使い捨ての空データベースを使用してください。セットアップとマイグレーションは計測外、リクエストの永続化は計測内です。

プロセス内ベースラインは profile ごとに分かれます。既定は `benchmarks/baselines/internal/sqlite.json`、`TOKENHUB_BENCHMARK_POSTGRES_URL` を設定した実行では `sqlite-postgresql.json` です。commit 済みでクリーンな revision からのみ生成または意図的に更新します。

```bash
TOKENHUB_BENCHMARK_UPDATE_BASELINE=1 ./benchmarks/run-internal.sh
```

PostgreSQL profile の更新時は PostgreSQL URL も設定します。ベンチマーク集合、Go バージョン、OS、アーキテクチャ、CPU のモデル/数、システムメモリが異なる場合、検査は比較を拒否します。

## ベースラインとバジェット

`benchmarks/budget.json` は、成功率 99.9% 以上、ベースラインの 90% 以上のスループット、平均遅延の回帰 15% 以内、P99 回帰 20% 以内を要求します。共有 Runner のジッタによる誤検出を減らしつつ、大きな回帰を検出します。追跡されるローカルベースラインは Chat、Responses、ストリーミング、実際にプライマリを呼び出すフェイルオーバーをカバーします。

```bash
./.tmp/tokenhub-benchmark check \
  --baseline benchmarks/baselines/tokenhub-local-smoke.json \
  --current output/benchmarks/tokenhub-local-smoke.json \
  --budget benchmarks/budget.json
```

schema、プロトコル、ストリームモード、負荷モード/レベル、モデル、リクエストサイズ、upstream 遅延、ランタイム/ハードウェア profile が異なる結果は比較されません。安定したマシンで 5 回以上実行し、意図した性能変更がある場合のみ中央値の実行をベースラインとして更新します。

4 つの現在結果を `benchmarks/baselines/*.json` と同じファイル名で 1 つのディレクトリに配置し、スイート全体を検査できます。

```bash
./benchmarks/check-suite.sh output/benchmarks/current
```

シナリオフィンガープリントには、期間、ウォームアップ、タイムアウト、最大実行中数、レスポンスサイズ、ストリーム形状、DB/テレメトリ profile も含まれます。いずれかが異なるか、ロードジェネレータがリクエストをドロップすると失敗します。

成功率、ロードジェネレータの飽和、リトライ回数、CPU/メモリ圧力が異なる実行は無効です。固定並列数のスループット、固定 RPS の遅延、TTFT、割り当てを別々に比較します。最大 RPS だけでは、TokenHub の永続化、監査、ルーティング、metrics、tracing のコストを説明できません。

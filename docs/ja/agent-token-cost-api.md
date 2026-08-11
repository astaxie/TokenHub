# Agent Token コスト API

Language: [English](../agent-token-cost-api.md) | [简体中文](../zh-CN/agent-token-cost-api.md) | 日本語

バージョン化された Agent Token コスト API を使うと、ローカルのレポート/監視 Agent は、管理者セッション、モデル呼び出し用 API Key、Provider 認証情報、手動エクスポートなしで TokenHub の利用量を読み取れます。この API は読み取り専用で、管理者の利用量画面と同じリクエスト数、Token 数、エラー数、推定顧客コストを使用します。

## エンドポイント

| エンドポイント | 認証 | 用途 |
| --- | --- | --- |
| `GET /api/v1/analytics/token-costs` | 分析 Credential | リクエスト単位または集計済み Token コストを JSON/CSV で取得 |
| `GET /api/admin/analytics/credentials` | プラットフォーム管理者 | 分析 Credential のメタデータを一覧表示 |
| `POST /api/admin/analytics/credentials` | プラットフォーム管理者 | 分析 Credential を作成し、Token を一度だけ表示 |
| `DELETE /api/admin/analytics/credentials/{id}` | プラットフォーム管理者 | 分析 Credential を即時失効 |

分析 Credential は `tha_` で始まり、`/v1/models`、モデル推論エンドポイント、管理者エンドポイントの認証には使えません。

## 最小権限 Credential の作成

管理者セッションまたは設定済み管理者 Token で Project スコープの Credential を作成します。

```bash
curl -sS https://tokenhub.example.com/api/admin/analytics/credentials \
  -H "Authorization: Bearer $TOKENHUB_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "payments-cost-agent",
    "scope_type": "project",
    "project_id": "prj_payments",
    "expires_at": "2026-12-31T00:00:00Z"
  }'
```

レスポンスには `credential` メタデータと `token` が含まれます。Token は直ちにコピーしてください。以後の一覧には prefix と suffix だけが表示されます。Agent が TokenHub インスタンス全体を読む必要がある場合に限り、`scope_type` を `organization` にして `project_id` を省略します。有効期限は任意ですが、設定を推奨します。

Token をローカル Agent の Secret ストアに保存します。

```bash
export TOKENHUB_ANALYTICS_TOKEN='tha_REPLACE_ME'
```

Agent を廃止した場合や Token 漏えいの可能性がある場合は失効させます。

```bash
curl -sS -X DELETE \
  https://tokenhub.example.com/api/admin/analytics/credentials/acred_REPLACE_ME \
  -H "Authorization: Bearer $TOKENHUB_ADMIN_TOKEN"
```

## リクエスト単位コストの取得

`from` は含み、`to` は含まない RFC 3339 時刻です。省略した場合は、クエリ開始時点までの直近 24 時間を取得します。

```bash
curl -sS -G https://tokenhub.example.com/api/v1/analytics/token-costs \
  -H "Authorization: Bearer $TOKENHUB_ANALYTICS_TOKEN" \
  --data-urlencode 'from=2026-08-01T00:00:00Z' \
  --data-urlencode 'to=2026-08-02T00:00:00Z' \
  --data-urlencode 'provider_id=prv_openai' \
  --data-urlencode 'model=gpt-4.1-mini' \
  --data-urlencode 'status=success' \
  --data-urlencode 'limit=100'
```

既定の `granularity=request` は、ゲートウェイリクエストごとにサニタイズ済みの 1 行を返します。安定した ID とメトリクスは含まれますが、提示された分析 Token、API Key Secret、Provider 認証情報、Provider コスト、クライアント IP、リクエスト/レスポンス Payload、User-Agent は含まれません。

## フィルターと集計

次の例は、Project、Provider、モデル、成功/エラー状態ごとの日次合計を返します。

```bash
curl -sS -G https://tokenhub.example.com/api/v1/analytics/token-costs \
  -H "Authorization: Bearer $TOKENHUB_ANALYTICS_TOKEN" \
  --data-urlencode 'from=2026-07-01T00:00:00Z' \
  --data-urlencode 'to=2026-08-01T00:00:00Z' \
  --data-urlencode 'granularity=day' \
  --data-urlencode 'group_by=project,provider,model,status'
```

時間バケットには `hour`、`day`、`month` を使います。時間バケットなしの合計には `granularity=none` を使います。`group_by` だけを指定した場合も `none` になります。`request` と `group_by` は併用できません。

| パラメーター | 値と動作 |
| --- | --- |
| `from`、`to` | RFC 3339 の `[from, to)` 区間。既定は直近 24 時間 |
| `project_id` | 完全一致する Project ID。Project Credential は常に自 Project に限定され、別 Project の指定には `403` を返す |
| `user_id` | 完全一致する利用帰属ユーザー ID |
| `api_key_id` | 保存済み API Key ID。API Key Secret ではない |
| `provider_id` | 完全一致する Provider ID |
| `model` | 完全一致する外部モデル名 |
| `status` | HTTP status が 400 未満なら `success`、400 以上なら `error` |
| `granularity` | `request`（既定）、`none`、`hour`、`day`、`month` |
| `group_by` | カンマ区切りまたは繰り返し指定する `project`、`user`、`api_key`、`provider`、`model`、`status` |
| `limit` | 1～1000 行。既定は 100 |
| `cursor` | 直前ページの不透明な `next_cursor` |
| `after` | Commit Sequence 差分取得に使う、コミット済みの不透明な `watermark`。`from`、`cursor` と併用不可 |
| `format` | `json`（既定）または `csv`。`Accept: text/csv` でも CSV を選択 |

初回の Request 単位 Snapshot は最大 31 日、初回の集計 Snapshot は最大 366 日です。差分 Change Pull は Commit Sequence で範囲を限定するため、元の `from` を保持したまま `to` を進めても、元の履歴を再走査しません。

## JSON Schema

すべての JSON レスポンスは `schema_version: "1.0"` を宣言し、次の形になります。

```json
{
  "schema_version": "1.0",
  "object": "token_cost.list",
  "generated_at": "2026-08-02T00:00:01Z",
  "query": {
    "from": "2026-08-01T00:00:00Z",
    "to": "2026-08-02T00:00:00Z",
    "granularity": "day",
    "group_by": ["project", "model"],
    "filters": {"project_id": "prj_payments"},
    "format": "json",
    "limit": 100,
    "dedupe_by": "dedupe_key",
    "checkpoint_by": "commit_sequence",
    "incremental_mode": "snapshot"
  },
  "data": [
    {
      "dedupe_key": "aggregate_f6d6...",
      "bucket": "2026-08-01",
      "project_id": "prj_payments",
      "model": "gpt-4.1-mini",
      "metrics": {
        "request_count": 42,
        "error_count": 2,
        "input_tokens": 120000,
        "cached_input_tokens": 35000,
        "cache_write_input_tokens": 4000,
        "output_tokens": 18000,
        "reasoning_output_tokens": 2500,
        "total_tokens": 138000,
        "estimated_cost_usd": 1.73
      }
    }
  ],
  "has_more": false,
  "watermark": "OPAQUE_WATERMARK"
}
```

`request_count` と `error_count` はゲートウェイのリクエストログから得られるため、利用量行を持たない失敗リクエストも含みます。Token とコストは利用量レコードから得ます。キャッシュと推論 Token は入力/出力合計に含まれている明細なので、すべての明細を `total_tokens` に再加算しないでください。`estimated_cost_usd` は管理者の利用量画面と同じ外部顧客向け推定課金額であり、機密の Provider コストではありません。

## ページングと差分取得

`has_more` が true の場合は `cursor=next_cursor` で再度呼び出します。Cursor は元のフィルター、`granularity`、`group_by`、スナップショット期間を保持するため、これらのパラメーターは省略できます。再指定する場合は Cursor と一致する必要があります。スナップショット上限は固定されるため、ページング中に到着したリクエストで後続ページがずれることはありません。

レスポンスの `watermark` は完了済み Database Snapshot を識別します。PostgreSQL では新しい Request Log に Transaction ID と永続 Offset の組み合わせを保存し、Upgrade 時には固定済みの履歴へ Event Time 順の重複しない Checkpoint 値を割り当てます。履歴の並べ替えは MVCC を使用し、新しい Request Log の Insert を止める Table Lock を取得しません。watermark は可視 Request Log の最大 Sequence を超えず、`pg_restore` 後の起動処理が永続 Offset を復元済み最大値より上へ Rebase するため、Agent が保存済みの watermark は新 Cluster でも有効です。Checkpoint は Snapshot 内の最古の Active Transaction より前でも停止するため、後続 Transaction の Commit が共有 Analytics Row Lock を待つことはありません。SQLite では Database 既存の Single-writer Model 内で Transactional Sequence を使用します。Checkpoint は `occurred_at` が `to` 以上である最初の一致済み Commit Request より前でも停止するため、後から `to` を進めても、すでに Commit 済みの Future Event を欠落させません。Snapshot に一致する Row がなくても watermark は返されます。`has_more` が false になるまですべてのページを処理し、成功後にだけ watermark を Agent の永続状態へコミットしてください。次回は `after=<committed watermark>` を使います。

```bash
curl -sS -G https://tokenhub.example.com/api/v1/analytics/token-costs \
  -H "Authorization: Bearer $TOKENHUB_ANALYTICS_TOKEN" \
  --data-urlencode "after=$TOKENHUB_COST_WATERMARK"
```

`after` を使う Pull では `query.incremental_mode` が `changes` となり、Commit 済み watermark より大きく、新しい Snapshot 以下の Commit Sequence を持つ Request Log だけを返します。元の Filter と Event-time `from` は保持され、`to` は継続的に進められます。したがって、遅れて Commit された Request の `occurred_at` が前回 Snapshot の最新 Event より早くても欠落しません。

Request 単位 Change の `dedupe_key` は `request_id` と同じです。集計 Change Row は新しく Commit された Request の Delta であり、その Key は Commit 済み開始 Checkpoint、Query Shape、Bucket、Dimension を識別します。全 Page を Key で Stage または Upsert し、Pull 完了後に Delta を一度だけ適用して watermark を Atomically に進めてください。同じ Commit 済み watermark から再試行すると、新しい Snapshot が先へ進んでも Key は安定し、Stage 済みの値を置換できます。Watermark の再利用時に Filter または集計形状を変更すると拒否されるため、異なる形状の Report は新しい `from` と `to` で開始してください。

## CSV エクスポート

```bash
curl -sS -G https://tokenhub.example.com/api/v1/analytics/token-costs \
  -H "Authorization: Bearer $TOKENHUB_ANALYTICS_TOKEN" \
  -H 'Accept: text/csv' \
  --data-urlencode 'from=2026-08-01T00:00:00Z' \
  --data-urlencode 'to=2026-08-02T00:00:00Z' \
  --data-urlencode 'granularity=hour' \
  --data-urlencode 'group_by=project,provider,model' \
  -o token-costs.csv
```

CSV は JSON と同じ Filter、上限、Metric、Pagination、`dedupe_key` を使います。Metadata は `X-TokenHub-Schema-Version`、`X-TokenHub-Has-More`、`X-TokenHub-Next-Cursor`、`X-TokenHub-Watermark`、`X-TokenHub-Dedupe-By`、`X-TokenHub-Checkpoint-By`、`X-TokenHub-Incremental-Mode` Header で返します。Spreadsheet Formula と解釈される可能性のある Text Cell には Apostrophe Prefix を付けます。

## CLI と MCP の評価

| 選択肢 | 判断 | 理由 |
| --- | --- | --- |
| バージョン化 HTTP + CSV | 現在サポート | 任意の言語、Cron、Agent Runtime から利用でき、追加 Binary の配布がなく、認証とチェックポイントが明示的 |
| 専用 CLI | 保留 | CLI は主に HTTP の Wrapper であり、インストール、更新、ローカル Secret 設定が増える。対話的な Credential 設定や定期レポートの Packaging が必要になった時点で再評価 |
| MCP Server/Tool | 保留 | MCP は長時間稼働する新たな信頼境界と Host 固有のデプロイを追加する。直接 HTTP ではなく、Tool Discovery や共有チェックポイント管理が Agent Host に必要になった時点で再評価 |

将来の CLI/MCP Adapter は分析 Credential だけを受け取り、Cursor/watermark の意味と同じ Schema version を維持し、管理者セッションやモデル呼び出し用 API Key を要求してはなりません。

## セキュリティと運用

- 分析 Credential を作成、一覧表示、失効できるのはプラットフォーム管理者だけです。
- 成功したクエリ、スコープ違反、不正なクエリ、無効 Credential の試行は、すべて `token_cost_analytics` 監査イベントとして記録されます。
- 分析 Credential とその Hash はクエリレスポンスおよび監査スナップショットから除外されます。一度だけ表示される Token は Secret として保存してください。
- 分析読み取りは、ゲートウェイの Core Pool とは別の小さな専用 Connection Pool を使います。ファイル型 SQLite は WAL を有効にして読み取りが Gateway Write を妨げないようにし、PostgreSQL は独立した分析 Pool を使います。`created_at` と `(project_id, created_at)` の Index、期間、ページサイズに加え、各クエリには 10 秒の実行期限があります。
- 失効は次回リクエストから有効です。ローテーションは、代替 Credential の作成、Agent 更新、旧 Credential の失効の順で行います。
- 無制限の履歴取得を並列実行せず、ページングしてください。タイムアウトは `503 analytics_query_timeout` を返します。再試行前に期間または Grouping Cardinality を減らしてください。

# A2A 1.0 Agent ゲートウェイ

Language: [English](../a2a-agent-gateway.md) | [简体中文](../zh-CN/a2a-agent-gateway.md) | 日本語

TokenHub は、審査済みの上流 Agent を A2A 1.0 JSON-RPC ゲートウェイとして公開し、SSE ストリーミングを提供します。このインターフェースは A2A 0.3、HTTP+JSON、gRPC を受け付けません。

## 有効化とロールバック

`TOKENHUB_A2A_ENABLED=true` を設定してバックエンドを再起動します。既定値は `false` です。`false` に戻すと、公開 Agent Card、A2A ゲートウェイ、MCP admission API、`agent/<slug>` Responses ブリッジが停止します。レジストリと Task データは削除されません。

本番環境の上流は HTTPS を使用し、ループバック、リンクローカル、プライベートアドレスへ解決されてはいけません。`TOKENHUB_A2A_ALLOW_PRIVATE_UPSTREAMS=true` は、管理されたローカル開発環境専用です。

## Agent の登録と審査

管理コンソールで **Agent Gateway** を開き、小文字の slug と Agent Card URL を入力します。TokenHub は Card を取得・検証し、A2A 1.0 `JSONRPC` インターフェースを必須とします。公開 Card から上流のセキュリティ宣言を除去し、静的ヘッダーを暗号化して、不変のリビジョンを作成します。データベース管理の過去リビジョンは同じ画面から復元できます。`data/agent-catalog.yaml` から同期された項目はコンソールで読み取り専用です。

`POST /api/admin/agents` でも登録できます。`card_url` またはインライン `card` を指定し、`upstream_url` で審査済み JSON-RPC URL を上書きできます。認証情報を `data/agent-catalog.yaml` にコミットしないでください。このファイル内の slug は一意でなければなりません。設定エントリは、同じ slug のデータベース Agent を引き継いで以前のインスタンスを無効化し、コンソールでは読み取り専用になります。管理者は設定管理 Agent を上書きできません。

```yaml
agents:
  - slug: research
    card_url: https://research.example/.well-known/agent-card.json
    status: active
    max_concurrency: 8
    allowed_forward_headers: [X-Request-ID, traceparent]
```

## アクセス制御

Agent 呼び出しはデフォルト拒否です。コンソールまたは `POST /api/admin/agent-access-bindings` で、有効な許可ルールを 1 件以上作成します。対象は `global`、`team`、`project`、`api_key`、`end_user`、`agent`、`access_group` です。一致する有効な拒否ルールは常に許可ルールより優先されます。Agent Card の skill ID で許可範囲を限定することもできます。

`X-TokenHub-End-User-ID` は、API Key メタデータに `allow_end_user_identity=true` がある場合だけ受け付けます。

## A2A 呼び出し

すべての JSON-RPC リクエストに `A2A-Version: 1.0` と TokenHub API Key が必要です。Agent Card の取得にも API Key が必要で、許可された Skill だけを返します。プロトコルバージョンがない、または異なる場合は A2A エラー `VERSION_NOT_SUPPORTED` を返します。未承認の Discovery は、未知の Agent と同じ Not Found 応答になります。

```bash
curl "$TOKENHUB_URL/a2a/research" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H "A2A-Version: 1.0" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc":"2.0",
    "id":"request-1",
    "method":"SendMessage",
    "params":{"message":{"messageId":"message-1","role":"ROLE_USER","parts":[{"text":"リリース内容を要約"}]}}
  }'
```

`SendMessage`、`SendStreamingMessage`、`GetTask`、`ListTasks`、`CancelTask`、`SubscribeToTask` をサポートします。Push Notification メソッドはサポートしません。公開 Card は `/a2a/<slug>/.well-known/agent-card.json` または `/.well-known/agent-card.json?agent=<slug>` です。

TokenHub は上流 Task ID をゲートウェイ ID に置き換え、最初に選択したインスタンスと API Key へ Task を固定します。継続、取得、キャンセル、購読は同じインスタンスを使います。上流へ到達した可能性があるメッセージを別インスタンスへ自動再試行しません。

設定した静的上流ヘッダーは同名のクライアント値より優先されます。クライアントヘッダーは、名前が `allowed_forward_headers` に含まれる場合だけ転送されます。認証情報、Cookie、Hop-by-Hop ヘッダー、`A2A-Version`、TokenHub Delegation ヘッダーは、この許可リストへ追加できません。静的認証情報は管理 API またはシークレットから生成したデプロイ設定で指定し、バージョン管理対象カタログにはコミットしません。

各上流呼び出しには 5 分間有効な `X-TokenHub-Delegation-Token` が付きます。対応済み Agent は、TokenHub のモデル API または Agent API を呼ぶ際に Bearer Token として利用できます。署名済み ID には Project、Key、End User、呼び出し元 Agent、Execution、親 Step、深度、Agent の並び、期限が含まれます。同じ Agent ID の再出現はループとして拒否されます。既存の Provider 呼び出し前コンテンツセキュリティポリシーは、上流へ送信する前に直接 A2A のテキスト部分と Responses ブリッジ入力も評価します。

## Responses ブリッジ

OpenAI Responses API のみを実装したアプリケーションは、モデル名 `agent/<slug>` で Agent を呼び出せます。

```json
{"model":"agent/research","input":"リリース内容を要約","stream":true}
```

TokenHub は入力を A2A User Message に変換し、Agent のテキスト、Status Message、テキスト Artifact を Responses 出力へ変換します。その他の `/v1/responses` の動作は変わりません。

## 実行ガバナンスと MCP 計測

ルート呼び出しごとに、データベース管理の Execution と Step Graph を作成します。Agent Hop、モデル呼び出し、計測対応済み MCP 呼び出し、実行時間、Token、コスト、Agent Step の並行数に既定上限があります。`TOKENHUB_A2A_MAX_*` の詳細はデプロイガイドを参照してください。

対応済み Agent は、MCP 呼び出し前に admission を実行し、完了後に実使用量を報告します。

```bash
curl "$TOKENHUB_URL/api/a2a/executions/mcp" \
  -H "Authorization: Bearer $TOKENHUB_DELEGATION_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"phase":"admit","step_id":"mcp-step-1"}'
```

完了時は同じ `step_id`、`phase: "complete"`、0 以上の `tokens` と `cost_usd` を送信します。上流 Agent 内だけで動作し、この admission に対応していない不透明な MCP 呼び出しは TokenHub で計測できません。MCP 制限が必須の場合、上流 Agent にこの連携を要求してください。

Registry Revision、Instance Health、Task Snapshot/Event、Execution Edge、Model/MCP Counter、Token、Cost は SQLite または PostgreSQL に永続化されます。管理 API は静的認証情報や Delegation Token の署名材料を返しません。

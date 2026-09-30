# テキスト再ランキング

`POST /v1/rerank` はクエリに基づいて候補文書を順位付けし、API キー、モデル権限、制限、適用される安全ポリシー、プラグイン、ルーティング、監査を利用します。公開モデルは rerank 型で、対応プロトコルとテナント料金の設定が必要です。新規ルートの公開では上流原価も必要です。

```json
{"model":"public-reranker","query":"証明書を更新するには？","documents":["更新手順","無関係な文書"],"top_n":1,"return_documents":true}
```

応答は model と results に元の index、relevance_score、任意の document.text を返します。重複文書も索引で区別し、スコアを変換しません。listwise 入力を分割しません。空入力、不正な top_n、未対応パラメータ、上流の欠落や不正索引はエラーです。テキストのみ対応し、画像、動画、非同期ジョブは対象外です。

`documents` は空でないテキストを 1–2048 件含めます。`top_n` を指定する場合は 1 以上、文書数以下にします。省略時はすべての文書を返します。

## 設定

Provider の詳細設定で実際の再ランキングプロトコルを選択します。パスは Base URL に追加し、rerank_path で変更できます。

| プロトコル | Base URL 末尾 | 既定パス | 対象 |
| --- | --- | --- | --- |
| jina | `/v1` | `/rerank` | Jina、SiliconFlow、互換 vLLM/Xinference。BGE は API を提供するサーバーが必要 |
| cohere | `/v2` | `/rerank` | Cohere |
| voyage | `/v1` | `/rerank` | top_n を top_k に変換 |
| qwen | リージョン別互換 API | `/reranks` | qwen3-rerank |
| dashscope | `/api/v1` | `/services/rerank/text-rerank/text-rerank` | gte-rerank-v2 |
| tei | サーバールート | `/rerank` | documents を texts に変換 |

モデル名だけではプロトコルを判定できません。Xinference は配備された MODEL_UID を使います。Alibaba の資料には URL 接頭辞の相違があるため、モデルとリージョンの実際の URL を検証してください。qwen-rerank の自動別名変換は行いません。

適用モデルの jina/qwen/dashscope は instruction、voyage/tei は truncation を扱います。未対応の指定は拒否されます。

## 料金と既存設定

Token 計量は入力料金を使用し、無料の場合も明示的に確認します。Cohere の search_units は別の検索単位料金を上下流で設定します。API の設定は metadata.search_unit_price_usd、無料 Token 料金の確認は metadata.retrieval_pricing_confirmed="true" です。上流コストとテナント料金は独立し、未報告使用量と実測ゼロを区別します。

呼び出せないモデルもカタログに残し、未対応と表示して新規ルートから除外します。新規公開では能力と料金を検証します。更新時に既存の動作するルートを自動停止しません。モデルや Provider の変更・再公開には新しい検証を適用します。起動時に reranker の別名と、旧版で chat として保存された明確な reranker 在庫を冪等に補正します。公開 chat 別名は、設定済みの全上流が明確な reranker の場合に限り補正し、混在用途の別名は維持します。料金、ルート設定、過去の請求は変更しません。

管理者は公開前に `POST /api/admin/playground/rerank` を管理セッションで呼び出せます。

```json
{"provider_id":"configured-provider","request":{"model":"actual-upstream-model-or-uid","query":"example query","documents":["candidate document"]}}
```

resource_id は任意です。response、usage_evidence、pricing_status を確認してください。管理者の上流テストであり、テナント権限・請求の受け入れ検証とは異なります。監査はモデルと計量証拠を記録し、文書全文を保存しません。

## 検証範囲

ローカルテストは代表プロトコル、変換、認証、索引、検索単位の料金を検証します。設定画面にはブラウザーシナリオを使用します。実アカウント権限、地域 URL、サーバーバージョンは別途実上流検証が必要です。Dify、LangChain、LlamaIndex は対応 HTTP/再ランキング統合を使用してください。OpenAI SDK に標準 rerank メソッドはありません。カタログ掲載だけでは受け入れ完了を意味しません。

最終応答の検証では後処理・匿名化の結果を保持し、元の入力文書を復元しません。管理者テストは通常の実行設定経路から復号済み認証情報と選択リソースの上書きを読み込みます。ネイティブ単位の請求にも非負使用量の保護を適用し、テナント無料リクエストにも上流コストを記録します。

主計量の矛盾（total_tokens=0 と正の prompt_tokens など）や不正なネイティブ数量は 502 invalid_provider_usage とします。有効な検索単位は異常な補助 Token と独立に計価し、Token は非負に保って不整合を記録します。検索単位の Provider プラグインは rerank_protocol=cohere を宣言し、usage.retrieval_evidence に unit、nullable quantity、source（upstream/plugin/unreported）を返します。旧プラグインの全ゼロ Token オブジェクトは実測ゼロとはみなしません。

組み込み qwen/local は設定された rerank プロトコルに対応します。公開時は候補リソースの有効な上書き設定と、対応する Provider フォールバックを検証し、在庫の可用性にはリソースのみの能力も含めます。スコープが一致する provider_call フックも利用できます。TPM 予約には instruction を含め、検索単位のみまたは token 未報告の場合は割当制御用の受付時推定量を維持し、課金単位は変換しません。

在庫では検索料金と保存操作をまとめています。検索単位料金と token 料金の両方が設定されている場合、モデル一覧はそれぞれの単価を表示し、実際の課金はルートのプロトコルに従います。リクエスト・レスポンス切替とメタデータ展開で長い結果を読みやすくし、監査証跡は維持します。

旧カタログの汎用 chat 型でも明確な reranker ID は取り込み時に補正します。実行時に上流の操作種別、テキスト能力、テナント料金を再検証します。フックやキャッシュの最終結果は relevance_score 降順が必須で、順序不正は編集済み文書を復元せず拒否します。検索モデルの USD 単価はコンソール言語に従って表示します。

Rerank キャッシュフックは、ゲートウェイが提供する `cache_key`（`rerank:v1:`）を使用して返す必要があります。このキーは呼び出し元の範囲、プライバシー処理後のリクエスト、選択ルートの有効な設定に対応します。キーの欠落や不一致はキャッシュミスになります。キャッシュ検索は料金設定済みの有効なルートを計画順に確認し、ヒットしたルートのプロトコルと後処理フックで検証します。フォールバック応答は実際の実行ルートのキーで保存するため、後続リクエストでは上流呼び出しを繰り返さず再利用できます。無効になったルートのキャッシュは検索しません。キャッシュキー確定後のルート別変換ではランキング入力を変更できません。内容のマスキングはルーティング前に実施してください。キャッシュとプラグイン応答の計量単位は選択ルートのプロトコルで検証します。プラグインは `top_n` と `return_documents` を変更できません。`return_documents` が false の場合は最終応答から `document` を省略し、true の場合は既存の省略やマスキングを維持します。

## ローカル互換サービスとアップグレード

カスタム `openai-compatible` と `local` Provider で、ベンダーカタログ未指定または `local` / `openai-compatible` の場合、自動選択は Jina 互換の `/rerank` 契約を使います。明示的なプロトコルと既知のベンダー既定値を優先します。未知のベンダーカタログでは対応プロトコルの明示が必要で、モデル名だけからネイティブ API を推測しません。

実際のエンドポイントが `http://model-host:8000/v1/rerank` なら、Base URL は `http://model-host:8000/v1`、パスは空欄または `/rerank` です。サービスがルートの `/rerank` を提供する場合は Base URL に `/v1` を付けません。実際の配備先とプロトコルを確認してください。

公開済みルートは未確認のゼロ原価でも呼び出せます。欠落した原価は未知のままで無料と見なさず、使用量とテナント料金を保持します。新規公開には双方の料金設定が必要で、実行時はテナント料金、能力、在庫を引き続き検証します。v0.8 にはネイティブ `/v1/rerank` はありません。この変更はローカルサービス設定の修正であり、両バージョンの再ランキング入口が同一だったことを意味しません。

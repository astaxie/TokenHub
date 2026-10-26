# サブスクリプションとアカウント Provider

Language: [English](../subscription-providers.md) | [简体中文](../zh-CN/subscription-providers.md) | 日本語

TokenHub は、固定版 Magpie の有効な組み込みアカウント Provider 6 件と、コミュニティ一覧に含まれる全 29 件のアカウント Provider ID を統合しています。管理者はアカウントを Provider リソースとして接続し、モデルを読み込み、既存の認証・認可・使用量計測を通じてリクエストをルーティングします。Magpie サービスの実行は不要です。

アカウントアダプターと API Key プリセットは異なる接続方式です。アプリの有料契約が、API プラン、すべてのモデル、無制限の利用を自動的に提供するわけではありません。この統合は、上流がアカウント共有や自動アクセスを許可することを示すものではありません。適用されるプランの条件に従って利用してください。

## 実行環境

- コミュニティアダプターには、バックエンドホスト上の **Node.js 22.23.1 以降**が必要です。サポート対象の Docker イメージとネイティブ配布物には Node が含まれます。ソースから実行する場合はバックエンドプロセスの `PATH` に追加してください。`start.sh` がバージョンを確認します。実行時に npm パッケージをインストールしません。
- Claude Code には、バックエンドプロセスの `PATH` 上の公式 `claude` CLI が必要です。TokenHub は現在、この CLI を同梱せず、バージョンも固定しません。アカウントを接続する前に、Docker の場合はコンテナー内を含むバックエンド実行環境にインストールしてください。実行ファイルがない場合は `claude_cli_unavailable` を返します。
- Codex、Copilot、ChatGPT API、Gemini CLI、Antigravity は Go で統合しています。残りの 29 件は、固定ソースを同梱したアプリ管理の Node worker で実行します。Magpie デーモンや任意の JavaScript プラグインは実行しません。

サポート対象の構成は[デプロイ](deployment.md)を参照してください。

## アカウント Provider 一覧

6 件のネイティブ統合では、次の TokenHub Provider 型を使用します。「Chat」は `/v1/chat/completions`、「Responses」は `/v1/responses` を表します。ストリーミングと呼び出し元が定義するツールは、記載したプロトコルと上流モデルの対応範囲に従います。

| Provider | TokenHub Provider 型 | 接続方法 | 公開テキスト API の範囲 |
| --- | --- | --- | --- |
| Claude Code | `claude_subscription` | ブラウザー OAuth、setup token、アカウントセッション | 公式 Claude Code CLI 経由の Chat |
| OpenAI Codex | `openai_codex` | 既存の Codex OAuth または明示的なアカウントインポート | Responses と既存の互換変換 |
| GitHub Copilot | `github-copilot-subscription` | GitHub デバイスコード | Chat と Responses |
| ChatGPT API | `chatgpt-subscription` | Sign in with ChatGPT、または明示的な token インポート | 直接 API token を使う Responses。Codex バックエンドとは別経路 |
| Gemini CLI | `gemini_cli_subscription` | 管理者がデスクトップクライアント設定を指定する Google ブラウザー OAuth | Code Assist 経由の Chat |
| Antigravity | `antigravity_subscription` | 管理者がデスクトップクライアント設定を指定する Google ブラウザー OAuth | Code Assist 経由の Chat |

コミュニティ統合の全 29 件は **Chat** を公開し、上流モデルが対応する場合はストリーミングと呼び出し元定義のツールを利用できます。内部の Chat、Anthropic、Responses、Gemini 形式は、公開 Responses、埋め込み、メディア API の追加を意味しません。全項目で明示的なセッション JSON インポートも利用できます。Kimi Work はインポートのみ対応します。

| Provider | 一覧 ID | 利用できる接続方法 |
| --- | --- | --- |
| Cursor | `cursor` | ブラウザー、API Key |
| Kiro | `kiro` | Google、GitHub、AWS Builder ID、IAM Identity Center のブラウザー認証、`ksk_` API Key |
| Qoder | `qoder` | ブラウザー/デバイス認証、個人アクセストークン |
| Qoder China | `qoder-cn` | ブラウザー/デバイス認証、個人アクセストークン、公式 Qoder 企業 VPC |
| QwenWork | `qwenwork` | ブラウザー/デバイス認証 |
| QwenWork Enterprise | `qwenwork-enterprise` | 企業ドメインを明示するブラウザー/デバイス認証 |
| GCP AI | `gcp-ai` | Google デスクトップ OAuth、プロジェクトとリージョンを指定した authorized-user ADC JSON の明示的インポート |
| WorkBuddy | `workbuddy` | ブラウザー |
| WorkBuddy AI | `workbuddy-ai` | ブラウザー |
| Xiaomi MiMo | `mimo-app` | Xiaomi アカウントのブラウザー/ポーリング認証。シードモデルのみ |
| ZCode | `zcode` | Z.ai、BigModel、チームのブラウザー認証、サイト選択付き GLM Coding Plan API Key |
| Factory | `factory` | デバイスコード、`fk-` API Key |
| Grok | `grok` | デバイスコード |
| Devin | `devin` | ブラウザー |
| Command Code Go | `commandcode-plan` | ブラウザー、API Key |
| Zed | `zed` | ループバックコールバックを使うブラウザー認証 |
| Kimi Work | `kimi-work` | 明示的なセッション JSON インポートのみ |
| MiniMax Coding Plan | `minimax-code` | アカウントのデバイス/ブラウザー認証 |
| MiniMax Coding Plan Global | `minimax-code-global` | アカウントのデバイス/ブラウザー認証 |
| Cline | `cline` | ブラウザー/デバイス認証、API Key |
| Trae China | `trae-cn` | ブラウザー。上流アダプターは実験段階 |
| Trae Global | `trae-global` | ブラウザー。上流アダプターは実験段階 |
| OpenDesign | `opendesign` | アカウントのデバイス認証、API Key |
| Nous Portal | `nous-portal` | デバイスコード |
| Raccoon | `raccoon` | ブラウザー、手動コールバック/token フロー |
| AutoClaw | `autoclaw` | ローカルループバックの開始ページを使う Google または z.ai 認証 |
| AutoClaw China | `autoclaw-cn` | 電話番号と SMS コード |
| CodeArts | `codearts` | Huawei Cloud のブラウザー認証 |
| JoyCode | `joycode` | JD ブラウザー認証、`ptKey` と任意の企業テナント |

コミュニティ項目の Provider 型は、一覧 ID の `-` を `_` に置き換えて `_subscription` を付けます。リソース型は `_account` を付けます。例：`kimi-work` は `kimi_work_subscription` と `kimi_work_account` に対応します。プラットフォームプラグイン ID は `tokenhub.provider.<一覧ID>-subscription` です。

## アカウントを接続する

1. 管理者の Provider エディターで対象のアカウント Provider を選択します。
2. アカウントリソースを追加し、接続方法を選び、上流のブラウザー、デバイスコード、token の指示に従います。既存セッションは JSON で明示的にインポートできます。TokenHub はホストの CLI 認証情報やデスクトップアカウントデータベースを検索しません。
3. 認証またはインポートを完了した後、**リソースを保存します**。エディターはリソースへ入力するため新しい認証情報を一時的に受け取ります。認証完了だけではリソースの永続保存は保証されません。
4. アカウントのモデルを読み込み、リソースのプローブで対象モデルを確認し、ルーティングと価格を設定してからアプリのトラフィックを有効にします。プローブは利用枠を消費する場合があります。

認証情報は、暗号化セッション JSON を含むリソースの保護された認証フィールドに保存します。Provider Options、通常のヘッダー、ログ、ソース管理には置かないでください。進行中の認証セッションは開始した管理者と Provider に紐付き、ランダムな state と有効期限を持ち、一度だけ使用できます。多くのフローは 10 分、Claude setup-token の受け渡しは 5 分で失効します。不要なフローはエディターからキャンセルできます。認証情報の交換中にキャンセルやタイムアウトで worker が停止した場合、その再開不能なセッションを直ちに解放します。

`PATCH /api/admin/provider-resources/{id}` で既存アカウントを置き換えたり、セッション JSON と token を切り替えたりする場合は、`credentials.replace` を `true` にし、新しい `access_token` または完全な `session_json` を指定してください。認証情報全体が置き換わり、省略した古い token、OAuth クライアント設定、アカウントメタデータは削除されます。必要な新しいクライアント設定やアカウントメタデータも `credentials` に含めてください。このフラグのない通常の認証情報更新では、指定した認証項目のみを従来どおりマージします。新しい token もセッションもない置き換え要求は拒否されます。

認証情報を明示的に置き換える際、省略した接続上書き設定、正常性の状態、レート制限、同時実行設定は現在の値を維持します。空の値やゼロの制限を明示した場合は、既存のリセットとデフォルト値の規則を適用します。置き換えの保存時に、以前のアカウントの利用枠キャッシュを無効化します。

既存のサブスクリプションアカウントを更新するには、Provider エディターの一覧でアカウントを 1 つ選び、「接続」タブで「アカウントを再認可」を選択します。ログインまたはインポートの完了後に「新しい認証情報を保存」を選択してください。対象アカウントの認証情報のみが置き換わり、スケジューリング設定は維持されます。別のアカウントを選ぶと進行中の認可がキャンセルされ、未保存の認証情報は破棄されます。

ネイティブの Copilot と ChatGPT の HTTP エラーでは、ルーティング用に上流のステータスと有効な `Retry-After` 待機時間を保持します。認証情報とモデルのエラーには、通常のアカウントおよびモデルのフェイルオーバーが適用されます。

Copilot とコミュニティの Chat リクエストを Responses モデルへ送る場合、明示的なゼロ値を含む `temperature`、`top_p`、`user`、`service_tier` を保持します。利用可否は選択したモデルに依存します。Factory では、明示的な `service_tier: "priority"` によりモデルの Fast バリアントを選択できます。Gemini CLI と Antigravity は終了エラーイベントで報告された使用量を保持し、上流の認証、クォータ、一時的な障害を分類してルーティングに渡します。

Gemini CLI と Antigravity は、プロキシ認証の失敗と接続タイムアウトを出口通信の障害として扱い、有効なアカウントにペナルティを与えません。GCP の token 更新とモデル取得では、一時的な上流障害やレート制限を認証情報の拒否と区別します。ChatGPT、Gemini CLI、Antigravity の token 更新でも、アカウントに固定したリクエストのレート制限を含め、一時的な上流ステータスと有効な再試行待機時間を保持します。

Gemini CLI と Antigravity では、選択した Provider が承認したデスクトップアプリの **OAuth client ID と client secret** をログインフォームに明示的に入力してください。環境変数ではなくアカウントのログイン項目です。TokenHub は共有の Google OAuth クライアント認証情報を同梱しません。クライアント設定は token 更新のためアカウント認証情報とともに暗号化して保存します。任意の Google OAuth デスクトップクライアントで Google ユーザーを認証できても、対象の Code Assist や Antigravity サービスの利用資格がない場合があります。承認された設定を取得し、アカウントの上流サービス利用資格を確認してから使用してください。

Google アカウントで Cloud プロジェクトの明示が必要な場合、接続時または再認可時の任意ログイン項目にプロジェクト ID を入力してください。選択したプロジェクトは新しい認証情報とともに保存します。再認可時に以前のアカウントのプロジェクトを自動では引き継ぎません。

Antigravity のツール引数は、対応する OpenAPI schema のサブセットへ変換されます。ローカル参照と nullable フィールドに対応し、再帰参照、外部参照、未解決の参照、過大な展開は推論前に HTTP 400 で拒否します。Gemini CLI の既存の JSON Schema 処理は維持されます。

Kimi Work は `type: "oauth"`、`access`、`webToken`、`uid` を含むオブジェクトをインポートします。`deviceId` は任意です。値は権限のある自身のアカウントから明示的に取得してください。TokenHub はデスクトップのデータベースを読みません。他のセッション形式は Provider ごとに異なり、選択した Provider と一致する必要があります。

Claude Code はエディターから公式の `claude auth login --claudeai` ブラウザーログインを開始し、ループバックコールバックまたは認証コードの貼り付けで完了できます。専用の設定ディレクトリを使い、そのログインの認証ファイルまたは対応する macOS キーチェーン項目だけを読みます。また、`claude setup-token` で取得した長期 token、または `claudeAiOauth.accessToken` と利用可能な更新トークンのメタデータを含むセッション JSON をインポートします。カタログの `sonnet`、`opus`、`haiku`、`fable` はリクエスト時に CLI が確認する別名であり、利用権限のライブ一覧ではありません。

### リモートブラウザーとコールバック

デバイスコードは別のマシンのブラウザーで認証できます。単純なループバック GET コールバックでは、エディターの案内に従って完全な URL を貼り付けます。TokenHub は、その認証セッションが所有する正確なリスナーだけに転送し、上流アダプターがパス、state、PKCE を検証します。

一部の上流フローは認証情報の POST やローカルページのアセットに依存するため、URL のコピーだけでは完了しません。AutoClaw グローバル版の Google/z.ai フローは、バックエンドホスト上の HTTP ループバック開始ページを開きます。そのホストのブラウザー、または該当プロセスのループバックリスナーに到達する信頼できるトンネルを使用してください。コンテナーのループバックは独立しています。通常の TokenHub リバースプロキシは一時リスナーを公開しません。Command Code のブラウザー認証がリモート環境に適さない場合は API Key を利用できます。

マルチインスタンス構成では、認証の開始、ポーリング、交換、キャンセルを同じ TokenHub バックエンドに送信してください。コミュニティ認証 worker とコールバックリスナーはプロセス内の状態を保持します。保存済み認証情報はデータベースで永続化・共有できますが、進行中の認証や会話プロセスはインスタンス間で移動できません。これらの操作にはセッションアフィニティを設定してください。PostgreSQL の共有だけでは実現できません。

## アカウント設定と利用枠

Copilot は通常のモデル一覧が空のプランでも `auto` を利用できます。上流の選択 API が推論前にモデルとプロトコルを決定し、明示的な Responses リクエストには Responses 対応モデルが必要です。企業管理ユーザーはデバイス認証で `github_host`（`<tenant>.ghe.com`）を指定できます。ホストはアカウントと暗号化保存され、認証、セッション交換、API の宛先を決定します。

Copilot のモデルプロトコルと Auto のキャッシュはアカウント認証情報に紐付きます。認証情報を置き換えると新しいアカウントのルートを取得し、以前のアカウントで進行中だったモデル取得の応答は新しいキャッシュを上書きしません。各推論リクエストはモデル選択時のアカウントを維持します。

Gemini CLI はアカウントの実験フラグを読み、表示モデル ID を推論時の上流 ID に変換します。プロジェクトの指定は Code Assist の資格確認やオンボーディングを省略しません。Antigravity のツール schema では、上流が要求する配列要素の空 schema も補います。

GCP AI には Google デスクトップ OAuth client ID/secret、プロジェクト ID または番号、Vertex リージョン（既定 `global`）を指定します。専用のインポート方法で `client_id`、`client_secret`、`refresh_token` を含む `authorized_user` ADC JSON を貼り付けることもできます。プロジェクトのアクセス権を確認し、更新可能な認証情報を保存して Vertex の Gemini テキストモデルを取得します。サービスアカウント JSON、ファイルパス、ホストの gcloud 設定、ブラウザーセッションは取り込みません。Vertex AI を有効にして利用者にプロジェクトのアクセス権を与えてください。GCP Cloud Console のプロモーション残高スクレイピングは含まず、架空の残高は表示しません。

Cursor のチームアカウントは、インポートするセッション JSON に正の整数 `teamId` を明示できます。チームヘッダーと使用量照会は当該リソースに紐付き、ホストで選択中の Cursor チームは読みません。

QwenWork Enterprise は認証時に企業ドメインを求めます。認証情報、更新、推論は指定した正確なドメインに紐付き、Go トランスポートの DNS/IP 制限も適用します。token 更新後もアカウントごとのドメインを保持します。

Provider エディターの詳細設定には Grok X 検索（`x_search`）、Factory Standard のみ（`standard_only`）、Command Code ゼロデータ保持（`zdr`）の真偽値設定があります。リソース設定は Provider の既定値を上書きします。Factory Standard のみを有効にして Standard 枠が尽きた場合、再試行可能な利用枠エラーを返して既存のルートフォールバックを利用します。Standard 使用量が欠落または不正な場合、追加使用の残高があっても、枠を確認できるまで推論を停止します。効果は上流の対応とプラン資格に依存します。

Nous Portal と GCP AI を除くコミュニティアカウントで利用枠を照会できます。対応アダプターの手動チェックインは `claimed`、`done`、`inactive`、`ineligible` を返し、自動定期実行はしません。Qoder 国際版は TokenHub が収集しないネイティブのデバイス識別情報を要求する場合があります。モデル固有の枠や表示専用の枠は、アカウント全体の枯渇と区別します。 上流が残高または不完全なウィンドウだけを返した場合、利用枠の割合は不明のままとし、監視では `-` を表示します。明示的に枯渇した枠は `0%` と表示し、手動更新後もこの区別を保持します。

保存済みのコミュニティ OAuth アカウントに更新トークンがあり、アダプターがトークン更新に対応している場合、アカウントリソース一覧に「Token を更新」を表示します。API Key アカウントやトークン更新に未対応のアダプターでは、この操作を表示しません。

利用枠の照会やチェックインでサブスクリプションのログイン失効が報告された場合、HTTP 409 と `provider_resource_reauthorization_required` を返します。管理者のログインを維持したまま対象リソースを再認可できます。Factory Standard のみのモードでも、この状態をアカウント認証情報の失効と認識し、利用可能な別ルートへの通常のフォールバックを許可します。

固定版の一覧では全 35 件のアカウント Provider ID を網羅します。Cloudflare Workers AI と OpenCode Zen Free は API/公開サービス、Kimi Desktop は agent コネクターであり、追加のサブスクリプションアカウント Provider ではありません。アカウントの網羅は Magpie 全機能の複製を意味せず、ホストのアカウント検索、agent 切り替え、ブラウザー残高取得、新規メディア生成 API は対象外です。


## 追加 API Key プリセット

既存の API Key Provider に次の 12 件のプリセットを追加しています。`openai_compatible` リソースとそれぞれのプラン/API Key を使用し、前述のアカウント worker のログインは使用しません。

| カタログ ID | プランまたは API | 初期モデルの取得元 |
| --- | --- | --- |
| `baidu-qianfan-personal` | Baidu Qianfan Token Plan Personal | 固定版のシード ID |
| `baidu-qianfan-team` | Baidu Qianfan Token Plan Enterprise | 固定版のシード ID |
| `huawei-maas-token-plan` | Huawei Cloud MaaS Token Plan | 固定版のシード ID |
| `volcengine-coding-plan` | Volcengine Ark Coding Plan | 固定版のシード ID |
| `volcengine-agent-plan` | Volcengine Ark Agent Plan | 固定版のシード ID |
| `qwen-token-plan` | Qwen AI Token Plan | 固定版のシード ID |
| `kimi-code-global` | Kimi Code Global | アカウントからモデルを取得 |
| `tencent-token-plan-enterprise-intl` | Tencent Cloud Token Plan Enterprise Global | 固定版のシード ID |
| `clinepass` | ClinePass | 固定版のシード ID |
| `ollama-cloud` | Ollama Cloud | アカウントからモデルを取得 |
| `commandcode` | Command Code API | アカウントからモデルを取得 |
| `nous` | Nous Portal API | アカウントからモデルを取得 |

アカウントから取得する 4 件は、初期状態に候補モデルを含めません。既存の StepFun Global Step Plan は `stepfun-ai` のままです。既存の `ollama` カタログモデルを、Ollama Cloud のホスト済みモデルの利用権限として扱いません。更新と出典は[モデルカタログの更新](model-catalog-updates.md)を参照してください。

## 認証情報、隔離、制限

保存済みの認証情報は TokenHub の暗号化リソースストレージを使用します。認証情報を返す前に選択したアダプターの種類を検証します。アカウントを別の Provider 種類へ移した場合、古いリクエストは HTTP 409 で失敗し、新しい Provider の認証情報を使用しません。コミュニティ worker はアカウントと認証済み呼び出し元ごとに隔離され、専用の home、作業ディレクトリ、環境を持ちます。アカウントのゲートとクラスターリースが token 更新を伴う操作を直列化します。認証情報の更新は compare-and-swap で保存し、永続保存の確認を受けてから新しい認証情報を使用します。管理者による同時編集や保存失敗時は、新しい情報を上書きせず操作を失敗させます。保存中に同じリソース ID でアカウントが置換または削除・再作成された場合も、古いセッションの更新で新しいアカウントを上書きしません。モデルと利用枠の結果はリソースの識別情報と置換世代に結び付けます。削除・再作成や別 Provider への移動後は、モデル未変更の応答も含め、進行中の取得結果を無効とします。保存成功の確認が worker の待機タイムアウト後に届いた場合、対応する保存済みリビジョンだけを復元し、次のリクエストで使用します。失敗した確認や無関係な確認では、新しい認証情報の使用を許可しません。

バックグラウンド更新を含むネイティブ token 更新では、結果の返却や保存前に元のアカウント識別情報と認証情報の置換世代も検証します。token の値が同じでも、アカウントの移動、置換、削除・再作成で進行中の更新を無効にします。古い更新の失敗によって新しいアカウントを再認証待ちにすることもありません。認証情報や状態の最終書き込みでも現在の Provider 種類を原子的に検証し、ネイティブ更新、プラグイン更新、セッション更新に適用します。

同梱 worker は信頼されたアプリコードであり、任意コード用のサンドボックスではありません。直接 HTTP/HTTPS 呼び出しとサブプロセス生成を無効化し、対応する fetch は Go トランスポートの宛先・ネットワーク保護を通します。任意の Factory 企業ホスト名の拒否など、Provider ごとのドメイン制限も適用されます。Cursor は上流の HTTP/1 RunSSE フォールバックを使用します。エラー応答を秘匿化した後も、有効な上流の再試行待機時間をルーティングに保持します。非公開のエラーヘッダーや認証情報は転送しません。

コミュニティ worker はプロキシの設定、接続、認証、タイムアウトの障害を出口通信エラーとして保持します。共有トランスポートの障害ではアカウントにペナルティを与えず、別アカウントへの切り替えも行いません。非公開の通信診断情報は引き続き秘匿化します。

中継する上流レスポンスはストリーミングを含め累計 64 MiB、キュー内データは別途 32 MiB を上限とし、超過時に上流リクエストをキャンセルします。プールは最大 32 件の Node worker を保持し、それぞれ V8 old-space を 256 MiB に制限して起動します。これはプロセス RSS の上限ではありません。全 worker、ネイティブバッファー、TokenHub の他のコンポーネントを合わせたメモリーを確保してください。

Claude Code は home/設定を分離して実行し、ホストのファイル/shell ツール、設定ソース、hooks、skills、ブラウザー統合を無効化します。呼び出し元が宣言したツールだけを、非公開のローカル MCP 接続で公開します。公式 CLI の上流通信は Go worker 中継を通らないため、ホストの外向き通信ポリシーを適用してください。実行ファイルはアプリが制御し、Provider オプションから指定できません。これらの隔離は OS サンドボックスではありません。

Claude はアカウントごとに実行中またはツール結果待ちのリクエストを 1 件、バックエンドプロセスごとに最大 16 件保持します。ツール結果は同じ認証済みテナント/API 認証情報と同じバックエンドプロセスに返す必要があります。ツール結果待ちは 5 分、1 回の実行は最大 30 分です。再起動やバックエンド切り替えで待機中の継続状態は失われます。

呼び出し元のツールが有効な場合、CLI が直列のツール呼び出しを保証できないため、Claude は `parallel_tool_calls: false` を未対応の機能として拒否します。`tool_choice: "none"` を含む、ツールが有効でないリクエストは引き続きサポートします。Claude Code は Chat とストリーミング応答で、token／コンテキスト上限や拒否を含む上流の終了理由を保持します。

Claude は CLI が適用できない生成制御も拒否します。サンプリングのペナルティ、`min_p`、`top_k`、複数候補出力、seed／logit bias／log probability 設定、API 専用の出力・ツール制御などが対象です。ペナルティのゼロ値、`min_p: 0`、`top_k: 0`、`n: 1`、`logprobs: false` などの対応する既定値、null の制御項目、呼び出し元のメタデータは引き続き受け付けます。ツール継続では最初の実効 token 上限と `reasoning_effort` を維持する必要があります。設定変更が拒否されても待機中の継続状態は消費されません。

更新可能なアカウントリースが、Claude の認証情報再読込、実行、ツール結果待ち、認証情報の保存、プロセス終了処理をインスタンス間で保護します。キャンセルやタイムアウト後も更新済み認証情報の保存を試みます。一時的なストレージ障害が 3 回の試行後も続く場合は、非公開の一時復旧ディレクトリを保持し、認証情報の値を含めずパスをログに記録します。復旧は手動です。認証情報を永続ストレージに復元するまでディレクトリを保持してください。古いセッションが管理者の同時編集を上書きすることはありません。

モデル取得時に固定シード ID へフォールバックする場合があります。カタログの `subscription-account-live`、`subscription-account-fallback`、`subscription-account-empty` で区別し、フォールバックの利用権限と料金は未確認とします。上流の生の variant はメタデータに保持し、明示的な reasoning-effort 値だけを選択可能にします。OpenDesign GLM を含め、Messages プロトコルで提供されるモデルは、明示的に対応を宣言した推論強度をネイティブリクエストに保持します。掲載モデルはアクセス権、価格、コンテキストサイズ、残り利用枠を保証しません。対応する worker の利用枠 hook は `quota.read`、チェックイン hook は `checkin.run` で公開します。特定モデルの利用枠だけではアカウント全体を枯渇と判定しません。

テストは合成 HTTP フロー、プロトコル変換、ストリーム、ツール呼び出し、暗号化認証情報の更新、キャンセル、プロセス隔離、コールバック制限を対象としています。**実際の有料アカウント、現在の利用枠、すべての上流ログインフローはライブ検証していません。** Trae は実験段階で、上流の非公開インターフェースは TokenHub と独立して変わる場合があります。既存の Codex 画像生成は変更せず、今回の追加は新しい埋め込み、画像、音声、動画機能を保証しません。

## 固定ソースとライセンス

- [yetone/magpie](https://github.com/yetone/magpie)、コミット `d580537201938b9f510070a213e09b46479e53ad` を、確認済みの組み込み一覧、ネイティブプロトコルの参照元としています。MIT ライセンスは `tools/licenses/magpie-LICENSE` に保持します。`backend/internal/server/SUBSCRIPTION_THIRD_PARTY_NOTICES.md` に Copilot、ChatGPT、Claude、Gemini CLI、Antigravity のネイティブ実装の出典を示し、対応するプラットフォームパッケージと生成した API プリセットに第三者通知を含めます。
- [magpie-community/plugins](https://github.com/magpie-community/plugins)、コミット `5c9b7a413756c2b4f139290047085075c83eb3e8` のコミュニティアダプターを MIT で静的に同梱しています。`backend/internal/subscriptionworker/vendor/PROVENANCE.json` にパッケージ版と SHA256 を記録し、隣にライセンスを保持しています。

Vendored ソースは固定され、出典マニフェストとバイト単位で一致します。TokenHub の適応コードは vendor 外に置き、実行時の更新やパッケージインストールは行いません。

API プリセット情報は別途 `d7ad3f2a3eea4fadc6f7131bf8b0957024892ba1` に固定しています。

再現可能なアカウント一覧と明示的な除外項目は [`tools/magpie-subscription-coverage.json`](../../tools/magpie-subscription-coverage.json) に記録し、同梱レジストリーとプラグインパッケージに照合します。

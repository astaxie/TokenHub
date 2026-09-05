# Issue tracker: GitHub

このリポジトリの要望、問題、仕様は astaxie/TokenHub の GitHub Issues に記録します。
gh CLI を使用し、コマンドには --repo astaxie/TokenHub を指定します。

## Conventions

- 作成前に open と closed の Issue を検索し、重複を避けます。
- 最も適切な .github/ISSUE_TEMPLATE/ のフォームに従い、必須セクションを保持します。
- Issue と PR のタイトルおよび本文は英語で記述します。
- 複数行の本文は一時ファイルに保存し、--body-file で送信します。
- 認証情報、トークン、Cookie、未処理の環境ファイルなどの秘密情報を含めません。
- 公開、コメントなどの外部への書き込みは、ユーザーが許可した範囲で実行します。

主なコマンド：

- 閲覧：gh issue view <number> --repo astaxie/TokenHub --comments
- 検索：gh issue list --repo astaxie/TokenHub --state all --search "<query>"
- 一覧：gh issue list --repo astaxie/TokenHub --state open --json number,title,body,labels,comments
- 作成：gh issue create --repo astaxie/TokenHub --title "<title>" --body-file <file>
- コメント：gh issue comment <number> --repo astaxie/TokenHub --body-file <file>
- ラベル追加：gh issue edit <number> --repo astaxie/TokenHub --add-label "<label>"
- ラベル削除：gh issue edit <number> --repo astaxie/TokenHub --remove-label "<label>"
- クローズ：gh issue close <number> --repo astaxie/TokenHub

## Pull requests as a triage surface

**PRs as a request surface: no.**

GitHub Issues と PR は番号空間を共有します。種類が不明な番号は、
まず gh pr view で判別し、見つからない場合は gh issue view を使用します。
対象が存在しない場合と、認証やネットワークの障害を区別してください。

## Skill operations

スキルが「publish to the issue tracker」を要求した場合は GitHub Issue を作成します。
「fetch the relevant ticket」の場合は Issue の本文、ラベル、コメントを読みます。

## Wayfinding operations

- Map：wayfinder:map ラベル付きの Issue に Notes、Decisions-so-far、Fog を記録します。
- 子タスク：GitHub sub-issues を優先します。利用できない場合は Map 本文にタスクリストを設け、子タスク本文に Part of #<map> と記載します。
- 種類ラベル：wayfinder:research、wayfinder:prototype、wayfinder:grilling、wayfinder:task。
- ブロック関係：GitHub の issue dependencies を優先し、利用できない場合は子タスク本文に Blocked by: #<number> を記録します。
- 実行可能なタスク：Map の順序で、未完了のブロッカーがなく、未割り当てで開いている子タスクを選びます。
- 担当：実際に担当する開発者に割り当てます。
- 完了：結果を記録してタスクを閉じ、Map の Decisions-so-far に要約とリンクを追加します。

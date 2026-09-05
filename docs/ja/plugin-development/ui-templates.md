# UI テンプレートプラグイン

Language: [English](../../plugin-development/ui-templates.md) | [简体中文](../../zh-CN/plugin-development/ui-templates.md) | 日本語

UI テンプレートプラグインは宣言的な presentation package です。Plugin API v1 は `theme_tokens`、`shell_layouts`、`page_templates`、`dashboard_compositions` をサポートします。Admin UI contribution は許可された slot に Schema-driven panel、tab、card、field、action を追加します。

settings 画面は template を navigation、top bar、global search、account、content、page region、dashboard、contribution block に展開します。ユーザーは allowlist 内で宣言済みの theme token だけを調整し、既定値に戻せます。

template は任意の JavaScript、CSS、stylesheet URL、remote script、`@import`、`url(...)` を挿入できません。action は Core 経由で実行され、RBAC と audit の対象であり、raw admin credential を受け取りません。browser-local token override は team-wide server configuration ではありません。

各 contribution を対象 renderer でテストします。Schema が有効でも、すべての slot が各 control をサポートするとは限りません。詳細は[ガイド](guide.md) を参照してください。

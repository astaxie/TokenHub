# 界面模板插件

Language: [English](../../plugin-development/ui-templates.md) | 简体中文 | [日本語](../../ja/plugin-development/ui-templates.md)

界面模板插件是声明式展示包。Plugin API v1 支持 `theme_tokens`、`shell_layouts`、`page_templates` 和 `dashboard_compositions`。Admin UI 贡献可以向允许的 slot 添加 Schema 驱动的面板、Tab、卡片、字段和动作。

只有模板声明了可编辑主题 Token 时才显示设置页。页面用易懂名称、简短说明、分组设置行和主题方案 Tab 展示这些值，并提供恢复默认操作。布局声明和原始 Schema 属于实现细节，不作为设置展示。

安全边界是显式的：模板不能注入任意 JavaScript、CSS、stylesheet URL、远程脚本、`@import` 或 `url(...)`。动作经 Core 执行，继续受 RBAC 和审计约束，且不会获得原始管理员凭证。浏览器本地 Token 覆盖不是团队级服务端配置。

每个贡献都必须在目标 renderer 中测试；Schema 通过不代表所有 slot 都支持其中的每种控件。完整布局、控件和 slot 见[完整指南](guide.md)。

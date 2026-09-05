# 界面模板插件

Language: [English](../../plugin-development/ui-templates.md) | 简体中文 | [日本語](../../ja/plugin-development/ui-templates.md)

界面模板插件是声明式展示包。Plugin API v1 支持 `theme_tokens`、`shell_layouts`、`page_templates` 和 `dashboard_compositions`。Admin UI 贡献可以向允许的 slot 添加 Schema 驱动的面板、Tab、卡片、字段和动作。

设置页会将模板展开为可检查的导航栏、顶部栏、全局搜索、账号区、内容区、页面区域、仪表盘和贡献块。用户只能调整 allowlist 内已声明的主题 Token，并可恢复默认值。

安全边界是显式的：模板不能注入任意 JavaScript、CSS、stylesheet URL、远程脚本、`@import` 或 `url(...)`。动作经 Core 执行，继续受 RBAC 和审计约束，且不会获得原始管理员凭证。浏览器本地 Token 覆盖不是团队级服务端配置。

每个贡献都必须在目标 renderer 中测试；Schema 通过不代表所有 slot 都支持其中的每种控件。完整布局、控件和 slot 见[完整指南](guide.md)。

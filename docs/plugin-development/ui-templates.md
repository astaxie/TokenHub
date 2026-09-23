# UI Template Plugins

Language: English | [简体中文](../zh-CN/plugin-development/ui-templates.md) | [日本語](../ja/plugin-development/ui-templates.md)

UI template plugins are declarative presentation packages. Plugin API v2 supports `theme_tokens`, `shell_layouts`, `page_templates`, and `dashboard_compositions`. Admin UI contributions add schema-driven panels, tabs, cards, fields, and actions to approved slots.

The settings page appears only when a template declares editable theme tokens. It presents those values with human-readable labels, short descriptions, grouped setting rows, theme-variant tabs, and a restore-default action. Layout declarations and raw schemas remain implementation details rather than settings.

Security boundaries are deliberate: templates cannot inject arbitrary JavaScript, CSS, stylesheet URLs, remote scripts, `@import`, or `url(...)`. Actions execute through Core, remain subject to RBAC and audit, and never receive raw administrator credentials. Browser-local token overrides are not team-wide server configuration.

Test every contribution in its target renderer; a valid schema does not guarantee that every control is supported by every slot. See the [complete guide](guide.md) for supported layouts, controls, and slots.

Built-in plugin ecosystem panels and core provider/Codex settings follow the console language (Simplified Chinese, English, or Japanese). Translation applies only to known built-in titles, descriptions, labels, and help text. Plugin identifiers, configuration values, and third-party or unknown metadata remain unchanged.

Disabling a built-in template or Admin UI plugin removes its active presentation contributions immediately and preserves the disabled state after restart. Its declarations remain inspectable. A disabled Provider plugin also stops exposing its management actions and scheduled jobs. Re-enabling the plugin restores its supported contributions.

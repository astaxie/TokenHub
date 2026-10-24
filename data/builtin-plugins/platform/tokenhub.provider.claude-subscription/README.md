# Claude Code Subscription

Runs the genuine Claude Code CLI with an imported account token or `claudeAiOauth` session JSON. The CLI must be installed on the backend host. Account directories are private, built-in tools and inherited settings are disabled, and caller tools are exposed through a strict local MCP bridge.

Tool continuations are bound to the authenticated project, API key, account, model, and conversation. Each account supports one active conversation; unanswered tool calls expire after five minutes. CLI model aliases are listed locally; account entitlement is checked when the CLI invokes a model. Session rotations are encrypted back into the account resource.

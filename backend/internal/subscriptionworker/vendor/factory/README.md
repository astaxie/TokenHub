# @magpie-community/opencode-factory-auth

Signs in to a [Factory](https://factory.ai) (Droid) subscription and sends
model requests to Factory's API the way `droid` sends them. Provider id:
`factory`.

## Sign-in

- **Sign in with Factory (device code).** This is WorkOS's device flow
  under droid's own client. The browser opens Factory's page with the code
  filled in. Confirm it there, and the plugin picks up the tokens.
- **Factory API key.** A key (`fk-…`), as droid takes one from
  `FACTORY_API_KEY`. It is sent as the bearer token with droid's headers
  and never renewed. A key carries no organization header; `whoami`, asked
  once with the key, says whose it is and where its organization is served.
- **Organization.** A token that isn't in an organization yet is put in
  the first one your account belongs to.
- **Whoami.** The plugin asks `whoami` for your active organization, its
  region (EU orgs go to `api.eu.factory.ai`) and any host of the org's
  own. Every request then carries them, as droid's do.

## Where the sign-in is kept

The sign-in is kept wherever the host keeps provider sign-ins:

- OpenCode: `~/.local/share/opencode/auth.json`
- magpie: `plugin-auth.json`

It holds WorkOS's access and refresh tokens, with the organization,
region and host.

**Refreshing.**
- magpie renews the access token three minutes before it lapses, through
  the plugin's `auth.refresh`, once for the account and before its
  requests and usage need it; magpie saves the new tokens. OpenCode
  doesn't call that hook: there the token is renewed two minutes before it
  lapses, before a request, and the plugin saves it.
- WorkOS rotates the refresh token, so the plugin never runs two refreshes
  at once, and never spends one refresh token twice: a request that comes
  before magpie has saved a renewal goes on with the new tokens.
- The account counts as signed out only when WorkOS refuses the refresh
  token (a 4xx other than 429). Any other failure keeps the sign-in, and
  the current token is used while it lasts.

**Refusals.** If Factory refuses the organization a request names, the
plugin asks `whoami` again and resends the request once. If the refusal
stands, the error says what to check.

## Requests from other agents

Factory serves a subscription's model requests to Droid. Every request
droid sends opens its system prompt with "You are Droid, an AI software
engineering agent built by Factory.", so another agent's request to
Factory's OpenAI-shaped API (`/api/llm/o`: GPT and Grok on Responses, the
open models on chat completions) opens with that line too, the agent's own
prompt after it. On Anthropic's Messages (`/api/llm/a`), the plugin adapts
fixed client metadata while preserving the task instructions and history;
see [Claude Code through magpie](#claude-code-through-magpie) below.

## Models

Each model is served on the one API droid uses for it:

| API | Models |
|---|---|
| Anthropic Messages (`/api/llm/a`) | Fable 5.1, Fable 5, Opus 5.5, Opus 5, Opus 4.8 (each also in Fast Mode), Sonnet 5.5, Sonnet 5, Sonnet 4.6, Haiku 5.5, Haiku 4.5, MiniMax M2.7 |
| OpenAI Responses (`/api/llm/o/v1`) | GPT-6.1 Sol, GPT-6 Sol/Astra/Luna, GPT-5.6 Sol/Terra/Luna, GPT-5.5, GPT-5.4, GPT-5.3-Codex (each also in Fast Mode), Grok 4.7, Grok 4.6 |
| Chat completions (`/api/llm/o/v1`) | GLM-5.3, GLM-5.3-Flash, GLM-5.2, Kimi K3, DeepSeek V4.1 Flash, Qwen3.8 Max, MiniMax M3, Mistral Large 4, Mistral Medium 3.5, Nemotron 3 Ultra |
| Gemini generate (`/api/llm/g/v1/generate`) | Gemini 4 Argon, Gemini 3.1 Pro, Gemini 3.8/3.7/3.6/3.5 Flash, Gemini 3 Flash |

Reasoning efforts are the variants droid offers for each model. DeepSeek V4.1
Flash, Kimi K3 and GLM-5.2 have `none`, droid's "off": they stop thinking.
Reasoning is mandatory on GLM-5.3 and GLM-5.3-Flash (Fireworks turns `none`
away), so `low` is their least.

A model's price is its token multiplier in droid's registry
(`cost.tokenMultiplier`), which magpie shows beside the model as its rate
(`0.8×`), so a cheap one is picked without opening droid. A promotion running
comes off it, with the multiplier before the discount struck through beside
the price (`rateWas`): Mistral Large 4's launch discount, Gemini 4 Argon's,
Gemini 3.8/3.7 Flash's and GPT-5.6 Sol's, each while droid's own dates for it
hold. A promotion that has ended is no discount at all.

Gemini's requests go to Factory's generate route with the model in the
body, droid's line opening the system instruction. Gemini 4 Argon is behind
a Factory feature flag in droid 0.241.1, so an account it isn't on for may
be refused it.

A Fast Mode model (`claude-opus-5-5-fast`, `gpt-5.5-fast`, …) is its base
model served faster, for more of the plan's credits. It goes out as droid
sends it: its own id, with `speed: "fast"` and the `fast-mode-2026-02-01`
beta on Anthropic's API, at `service_tier: "priority"` on OpenAI's. A request
that asks the base model for fast (Claude Code's `speed: "fast"`, a
Responses request's `service_tier: "priority"`) is sent to its Fast Mode
model, as droid's own fast toggle does: asked of the base model, Factory
answered at standard speed (#119). Opus 4.6/4.7 Fast, GPT-5.4 Mini Fast, GPT-5.6 Sol Fast (unreleased)
and GLM-5.2 Fast (behind a feature flag) are not included.

auto is not included: droid picks it on the client side.

## Requests too large

Factory refuses a request body over its size limit with 413 "Request
Entity Too Large". The limit is on bytes, not on the model's context, and
images reach it first. On a 413 the plugin sends the request once more with
every image and file but the newest replaced by a line saying it was left
out, each tool result kept beside its call, as droid does. If that is still
too large, or there was nothing to leave out, the error says it is
Factory's byte limit: compact the conversation or start a new one.

## Use

```sh
magpie plugin add @magpie-community/opencode-factory-auth
magpie plugin login factory
```

## Standard usage only (optional)

In magpie, open the Factory provider, turn on **Standard usage only** under
**Plugin options**, and save (or set `"pluginOptions": {"factory":
{"standard_only": true}}` in magpie's settings.json). It is off by default,
applies to every Factory account, and takes effect at each account's next
usage reading.

When on, the plugin reports the Standard 5 hours, 7 days and 30 days
windows as counting every model, Droid Core's open models too, whether or
not the account has Extra usage. Droid Core's windows and Extra usage are
still shown but never keep an account in routing.

magpie leaves an account out before sending only when it is held at a
usage cap, so pair the option with a cap on each account, for example
`magpie provider account-cap factory <account> 99`. An account is then
skipped once any Standard window reaches the cap, the request goes to the
next account with the same model, and when every account is held magpie
answers 429 without sending. Without a cap, an account whose Standard
usage is gone is still tried, and Factory may serve a Droid Core model
from Core or Extra usage. Readings lag, so the last requests before a
window fills still go out; to never spend Extra usage, turn it off in
Factory.

## Claude Code through magpie

Factory's Anthropic route requires Droid's fixed client preamble and
refuses some of Claude Code's fixed environment, model and built-in skill
metadata. The plugin adapts that metadata while preserving the coding
instructions, environment values, tool definitions, images and reasoning
options. Ordinary Droid requests remain unchanged, and OpenAI routes keep
their existing request adapter. Quoted fixed metadata in tool results is
handled as described below.
The fixed Claude identity is recognized as a complete first line too when
magpie joins it with the following system instructions into one text block.

For the built-in `update-config` skill, the adapter changes only the known
self-reference "not Claude" to "not the assistant" inside that skill's entry
in a generated skill list. Claude Code renders the entry two ways: as a
system message (`- update-config: …`) and, in the `claude-code-guide` agent's
own system prompt under `# User's Current Configuration`, with the slash that
invokes it (`- /update-config: …`). Both spellings are adapted, and only the
sentence Factory refuses changes, so the entry's name and the rest of the list
stay. Its configuration instructions, other skill descriptions, tools and
caller text are preserved.

Claude 5 can send runtime metadata in string-content `system` messages
instead of user-message reminders. The plugin adapts the known environment,
model and configuration-skill metadata in that shape too, preserving the
message role, environment values and session instructions.
Startup hooks (`SessionStart` or `SubagentStart`, including additional
context) and deferred-tool announcements may precede that environment
block. The adapter preserves the prefix verbatim and adapts the generated
runtime context after it, including when folded into user text.
After `/model` switches, Claude Code can send model-only system updates
without the initial environment paragraph. Updates beginning with the known
model paragraph and containing complete generated token metadata are
adapted too, including accumulated switches when resuming a conversation.
Token budgets, permission instructions and conversation history are preserved.
The same model-update adaptation covers updates that start with the complete
generated `# Environment update` block after a working-directory change.

Claude Code's generated compaction summaries also have a fixed opening
that Factory refuses. The adapter recognizes the two generated opening
sentences and rephrases only the first in user strings and text blocks,
including summaries without a `Summary:` label. The generated artifact
provenance marker and note stay verbatim. If the summary itself quotes any
of the refused client phrases handled in tool output, the context after the
two opening sentences is encoded as a JSON string with explicit decoding
instructions. Decoding restores the exact summary, transcript path and
continuation instructions; context without those phrases stays verbatim.
Complete generated reminders for restored Read calls and omitted files are
adapted too. Read arguments and file paths are preserved; quoted or incomplete
reminders are left alone.
Claude Code (2.1.291 and later) also restores skills invoked before compaction
as one generated reminder. Factory refuses that fixed opening, and the restored
skill text can quote the same client phrases as tool output. A complete
reminder has its opening sentence rephrased. When its skill text quotes a
refused phrase, that text is encoded as a JSON string with explicit skill-text
decoding instructions; decoding restores the skill text exactly. Skill text
without those phrases stays verbatim inside the rephrased reminder. Pasted
fragments and incomplete reminders are left untouched.
Claude Code can also combine reminders into a token-prefixed `system` turn
without the reminder wrappers. The adapter handles the known metadata
paragraphs in that form, including skill-list updates and complete
invoked-skills reminders. A complete reminder is the fixed opening sentence
followed by the complete `IMPORTANT:` paragraph; skill text then runs until
the next known attachment (a token marker, hook, restored Read, omitted-file
note, Read result, environment, model line, skill-list header, changed-file
note, agent-type list or MCP server instructions) or the end of the turn.
Numbered file contents, token markers and SessionStart hook output stay.
The same adaptation applies when a translating gateway folds that complete
token-prefixed block into a user message.
Standalone skill updates can instead start with the generated skill-list
header and end with a token marker, for example when leaving auto mode.
That complete form receives the same adaptation while retaining the skill
descriptions and mode-change instructions.
The skill list can also come alone as a `system` message, with only its
entries: no header, no reminder wrapper and no token marker, as text blocks
or a string. A `system` message that opens with an entry gets the same
change to the known `update-config` line; everything else in it stays.
When runtime notifications precede the skill update, the adapter recognizes
the complete skill-header and bullet-list paragraphs inside a token-terminated
bundle. It preserves MCP connection errors, other notifications, mode updates
and custom skill descriptions while adapting the known built-in description.
After `/compact`, Claude Code also sends the files it restores and the
runtime context as one turn that opens with an unwrapped restored-file note,
Read call or complete invoked-skills reminder, with no token marker first.
A complete skills reminder is recognized even when no files were restored
and no environment or token paragraph follows it. A restored-file opening
requires the generated environment paragraph, a token marker or a complete
skills reminder before any hook output. Each fixed paragraph (file notes,
Read calls and results, skills, environment, model line) is adapted as in a
token-prefixed bundle; everything else stays byte-for-byte. Quoted,
incomplete and hook-owned forms are left alone.

Factory can also refuse fixed client phrases quoted in tool results, such as
the identity/environment definitions printed when inspecting this plugin's
source, the compaction opening embedded in a historical session JSONL
record, or pi's opening sentence in a model snapshot of its own system
prompt. Those text results are represented as JSON strings with explicit
decoding instructions and Unicode escapes for the fixed phrases. Decoding
the string recovers the exact original output, including quotes, backslashes
and Unicode; the plugin does not delete the output or replace its identities
with different ones. Tool ids, cache/error markers and non-text blocks stay
unchanged. Ordinary tool results are forwarded as before.
The same lossless encoding applies to restored or `@`-attached Read file text
after the exact `Result of calling the Read tool:` header, including complete
reminder wrappers and announced runtime bundles. The generated header and
wrapper stay intact. File text without a known refused phrase is unchanged.
Claude Code's complete changed-on-disk notifications receive the same handling:
only the numbered file snippet is encoded, retaining its TAB or colon line
separators, hunk separators and truncation notice. The path and instructions
about respecting the current file stay verbatim. This covers wrapped user
attachments and generated system bundles, including notifications before a
token marker. Changed-file text supplied by hooks, quoted or incomplete
notifications, and notices that omit the snippet are left alone. Model and
environment metadata after non-startup hooks retain their existing adaptation.

Claude Code's instructions reminder names `~/.claude/CLAUDE.md` "(user's
private global instructions for all projects)", which Factory refuses; in
the generated reminder it becomes "(global instructions)". Claude Code
renders that reminder two ways: the first copy opens with "Codebase and user
instructions are shown below", and a copy re-read after the files changed
(after compaction, a settings sync and the like) opens with "Instruction
files were re-read …; these differ from their earlier copies" or "These
instruction files changed". Both are adapted, in text blocks, string user
turns and chat completions' joined user text; text after the reminder and
a user's own mention of the phrase stay as written. A tool result that
quotes the phrase (reading Claude Code's renderer, say) gets the lossless
JSON encoding above.

When Factory still answers 403, the error magpie and the agent see keeps
Factory's request id (from `x-request-id`, `request-id` or `cf-ray`, or the
body) and its error code and type, and the Anthropic-route body carries
`request_id`, so the refusal can be named to Factory.

Use magpie's Claude Code integration to select the Factory provider.
magpie manages the provider-specific client settings, including capability
and permission configuration. Fields such as `safeguards` and
`context_management` are forwarded for Factory to validate; the request
adapter preserves them along with the other request options. One exception:
Factory's streaming route refuses `context_management` ("Extra inputs are
not permitted") while its non-streaming route takes it, so a streamed
request is sent without it. The server then clears nothing from the
context; Claude Code's own compaction still runs.

The same metadata adaptation applies to `/messages` and
`/messages/count_tokens`, so token counting sees the prompt used for
inference. Serving the counting endpoint still depends on the host and
upstream. Model and feature availability depend on the Factory account,
organization region and upstream API. Connectivity has been verified with
Sonnet 4.6, Sonnet 5.5 and Opus 5.5. Two-turn Read tool calls with Claude
Code's default tool set have been verified with Sonnet 4.6 and Sonnet 5.5,
including Sonnet 5.5 selected through a routing group.

## Tool schema compatibility

Factory's Anthropic route rejects tool schemas with root-level `anyOf`,
`oneOf` or `allOf`. The plugin nests those schemas under an internal
`arguments` property instead of removing their branch constraints. It
wraps examples and previous tool inputs too, then unwraps tool replies
before returning them to OpenCode or magpie. Ordinary object schemas and
OpenAI routes keep their existing behavior.

Local schema pointers are rebased to the nested schema; `$id` resources
and anchors retain their resolution scope, and literal data is unchanged.
Recursive schema resources
without `$id` fail explicitly because nesting would change their scope.
Affected streaming tool arguments are buffered until their content block
is complete. Other events, tool ids, usage and upstream errors pass through;
an incomplete or invalid argument envelope fails instead of being executed.

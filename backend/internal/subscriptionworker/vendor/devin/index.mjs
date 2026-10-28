// Devin subscriptions for OpenCode and magpie: the devin CLI's own sign-in
// (a PKCE round through app.devin.ai to a callback on 127.0.0.1), and each
// chat completion sent whole to the API the CLI talks to, Windsurf's
// GetChatMessage (a Connect RPC in protobuf), its reply streamed back as
// chat completion chunks. Ported from magpie's built-in Devin account
// (internal/provider/devin*.go, internal/gateway/devin.go).

import { Buffer } from "node:buffer"
import { createHash, randomBytes, randomUUID } from "node:crypto"
import { createServer, STATUS_CODES } from "node:http"
import { gunzipSync, gzipSync } from "node:zlib"
import { spawn } from "node:child_process"
import { existsSync, readFileSync, realpathSync, statSync } from "node:fs"
import { mkdir, readFile, writeFile } from "node:fs/promises"
import { homedir } from "node:os"
import { isAbsolute, join, relative, resolve } from "node:path"

const ID = "devin"
const CHAT = "@ai-sdk/openai-compatible"

const SERVER = "https://server.codeium.com"
const AUTHORIZE = "https://app.devin.ai/auth/cli/continue"
const EXCHANGE = "/exa.seat_management_pb.SeatManagementService/ExchangeDevinCLIPKCECode"
const CHAT_RPC = "/exa.api_server_pb.ApiServerService/GetChatMessage"
// the CLI the requests say they come from
const CLI_VERSION = "3000.11.3"
// the AI SDK is given a chat completions URL; the loader's fetch answers
// every request itself, so nothing is ever sent here
const BASE = SERVER + "/devin/v1"
const SIGN_IN_TIMEOUT = 10 * 60 * 1000
// how long the CLI's model list is kept, and how long a failed read waits
const FRESH = 5 * 60 * 1000
const RETRY = 60 * 1000

// ---- models ---------------------------------------------------------------------

// Devin's list as `devin models list --format json` gave it (2026-10-14),
// less Adaptive and Fusion, which route between models inside Devin's own
// agent and aren't served by the API: family id, label, aliases, and each
// variant's id, label, context and output. Inkling and Claude Sonnet 4.5,
// gone from that list, stay for the accounts still offered them. The
// account's own list, from Devin (GetCliModelConfigs) or else the CLI,
// replaces it.
const SNAPSHOT = [
  ["swe-2","SWE-2",["swe"],[["swe-2-high","SWE-2 High",262000,128000],["swe-2-medium","SWE-2 Medium",262000,128000],["swe-2-max","SWE-2 Max",262000,128000]]],
  ["swe-1.7-lightning","SWE-1.7 Lightning",[],[["swe-1-7-lightning","SWE-1.7 Lightning Max",202752,96000],["swe-1-7-lightning-medium","SWE-1.7 Lightning Medium",202752,96000]]],
  ["claude-fable-5-1","Claude Fable 5.1",["fable"],[["claude-fable-5-1-medium","Claude Fable 5.1 Medium",1000000,128000],["claude-fable-5-1-low","Claude Fable 5.1 Low",1000000,128000],["claude-fable-5-1-high","Claude Fable 5.1 High",1000000,128000],["claude-fable-5-1-xhigh","Claude Fable 5.1 XHigh",1000000,128000],["claude-fable-5-1-max","Claude Fable 5.1 Max",1000000,128000]]],
  ["claude-opus-5-5","Claude Opus 5.5",[],[["claude-opus-5-5-medium","Claude Opus 5.5 Medium",1000000,128000],["claude-opus-5-5-low","Claude Opus 5.5 Low",1000000,128000],["claude-opus-5-5-high","Claude Opus 5.5 High",1000000,128000],["claude-opus-5-5-xhigh","Claude Opus 5.5 XHigh",1000000,128000],["claude-opus-5-5-max","Claude Opus 5.5 Max",1000000,128000],["claude-opus-5-5-low-fast","Claude Opus 5.5 Low Fast",1000000,128000],["claude-opus-5-5-medium-fast","Claude Opus 5.5 Medium Fast",1000000,128000],["claude-opus-5-5-high-fast","Claude Opus 5.5 High Fast",1000000,128000],["claude-opus-5-5-xhigh-fast","Claude Opus 5.5 XHigh Fast",1000000,128000],["claude-opus-5-5-max-fast","Claude Opus 5.5 Max Fast",1000000,128000]]],
  ["gpt-6-astra","GPT-6 Astra",[],[["gpt-6-astra-medium","GPT-6 Astra Medium Thinking",1000000,128000],["gpt-6-astra-low","GPT-6 Astra Low Thinking",1000000,128000],["gpt-6-astra-high","GPT-6 Astra High Thinking",1000000,128000],["gpt-6-astra-xhigh","GPT-6 Astra XHigh Thinking",1000000,128000],["gpt-6-astra-max","GPT-6 Astra Max Thinking",1000000,128000],["gpt-6-astra-low-priority","GPT-6 Astra Low Thinking Fast",1000000,128000],["gpt-6-astra-medium-priority","GPT-6 Astra Medium Thinking Fast",1000000,128000],["gpt-6-astra-high-priority","GPT-6 Astra High Thinking Fast",1000000,128000],["gpt-6-astra-xhigh-priority","GPT-6 Astra XHigh Thinking Fast",1000000,128000],["gpt-6-astra-max-priority","GPT-6 Astra Max Thinking Fast",1000000,128000]]],
  ["gpt-6-sol","GPT-6 Sol",[],[["gpt-6-sol-medium","GPT-6 Sol Medium Thinking",1000000,128000],["gpt-6-sol-none","GPT-6 Sol No Thinking",1000000,128000],["gpt-6-sol-low","GPT-6 Sol Low Thinking",1000000,128000],["gpt-6-sol-high","GPT-6 Sol High Thinking",1000000,128000],["gpt-6-sol-xhigh","GPT-6 Sol XHigh Thinking",1000000,128000],["gpt-6-sol-max","GPT-6 Sol Max Thinking",1000000,128000],["gpt-6-sol-none-priority","GPT-6 Sol No Thinking Fast",1000000,128000],["gpt-6-sol-low-priority","GPT-6 Sol Low Thinking Fast",1000000,128000],["gpt-6-sol-medium-priority","GPT-6 Sol Medium Thinking Fast",1000000,128000],["gpt-6-sol-high-priority","GPT-6 Sol High Thinking Fast",1000000,128000],["gpt-6-sol-xhigh-priority","GPT-6 Sol XHigh Thinking Fast",1000000,128000],["gpt-6-sol-max-priority","GPT-6 Sol Max Thinking Fast",1000000,128000]]],
  ["gpt-6-luna","GPT-6 Luna",[],[["gpt-6-luna-medium","GPT-6 Luna Medium Thinking",1000000,128000],["gpt-6-luna-none","GPT-6 Luna No Thinking",1000000,128000],["gpt-6-luna-low","GPT-6 Luna Low Thinking",1000000,128000],["gpt-6-luna-high","GPT-6 Luna High Thinking",1000000,128000],["gpt-6-luna-xhigh","GPT-6 Luna XHigh Thinking",1000000,128000],["gpt-6-luna-max","GPT-6 Luna Max Thinking",1000000,128000],["gpt-6-luna-none-priority","GPT-6 Luna No Thinking Fast",1000000,128000],["gpt-6-luna-low-priority","GPT-6 Luna Low Thinking Fast",1000000,128000],["gpt-6-luna-medium-priority","GPT-6 Luna Medium Thinking Fast",1000000,128000],["gpt-6-luna-high-priority","GPT-6 Luna High Thinking Fast",1000000,128000],["gpt-6-luna-xhigh-priority","GPT-6 Luna XHigh Thinking Fast",1000000,128000],["gpt-6-luna-max-priority","GPT-6 Luna Max Thinking Fast",1000000,128000]]],
  ["gemini-4-argon","Gemini 4 Argon",[],[["gemini-4-argon-medium","Gemini 4 Argon Medium",1048576,1000000],["gemini-4-argon-low","Gemini 4 Argon Low",1048576,1000000],["gemini-4-argon-high","Gemini 4 Argon High",1048576,1000000]]],
  ["kimi-k3","Kimi K3",[],[["kimi-k3-high","Kimi K3 High",1048576,131072],["kimi-k3-low","Kimi K3 Low",1048576,131072],["kimi-k3-max","Kimi K3 Max",1048576,131072]]],
  ["glm-5.2","GLM-5.2",[],[["glm-5-2","GLM-5.2 High",200000,128000],["glm-5-2-max","GLM-5.2 Max",200000,128000],["glm-5-2-1m","GLM-5.2 High 1M",1000000,128000],["glm-5-2-max-1m","GLM-5.2 Max 1M",1000000,128000],["glm-5-2-none","GLM-5.2 No Thinking",200000,128000],["glm-5-2-none-1m","GLM-5.2 No Thinking 1M",1000000,128000]]],
  ["glm-5-3","GLM-5.3",[],[["glm-5-3-low","GLM-5.3 Low",1048576,128000],["glm-5-3-high","GLM-5.3 High",1048576,128000],["glm-5-3-max","GLM-5.3 Max",1048576,128000]]],
  ["claude-sonnet-5-5","Claude Sonnet 5.5",[],[["claude-sonnet-5-5-medium","Claude Sonnet 5.5 Medium",1000000,128000],["claude-sonnet-5-5-low","Claude Sonnet 5.5 Low",1000000,128000],["claude-sonnet-5-5-high","Claude Sonnet 5.5 High",1000000,128000],["claude-sonnet-5-5-xhigh","Claude Sonnet 5.5 XHigh",1000000,128000],["claude-sonnet-5-5-max","Claude Sonnet 5.5 Max",1000000,128000]]],
  ["gemini-3-8-flash","Gemini 3.8 Flash",["gemini"],[["gemini-3-8-flash-medium","Gemini 3.8 Flash Medium",1048576,65535],["gemini-3-8-flash-low","Gemini 3.8 Flash Low",1048576,65535],["gemini-3-8-flash-high","Gemini 3.8 Flash High",1048576,65535]]],
  ["claude-opus-4.7","Claude Opus 4.7",[],[["claude-opus-4-7-medium","Claude Opus 4.7 Medium",1000000,128000],["claude-opus-4-7-low","Claude Opus 4.7 Low",1000000,128000],["claude-opus-4-7-high","Claude Opus 4.7 High",1000000,128000],["claude-opus-4-7-xhigh","Claude Opus 4.7 XHigh",1000000,128000],["claude-opus-4-7-max","Claude Opus 4.7 Max",1000000,128000]]],
  ["claude-opus-4.8","Claude Opus 4.8",[],[["claude-opus-4-8-medium","Claude Opus 4.8 Medium",1000000,128000],["claude-opus-4-8-low","Claude Opus 4.8 Low",1000000,128000],["claude-opus-4-8-high","Claude Opus 4.8 High",1000000,128000],["claude-opus-4-8-xhigh","Claude Opus 4.8 XHigh",1000000,128000],["claude-opus-4-8-max","Claude Opus 4.8 Max",1000000,128000],["claude-opus-4-8-low-fast","Claude Opus 4.8 Low Fast",1000000,128000],["claude-opus-4-8-medium-fast","Claude Opus 4.8 Medium Fast",1000000,128000],["claude-opus-4-8-high-fast","Claude Opus 4.8 High Fast",1000000,128000],["claude-opus-4-8-xhigh-fast","Claude Opus 4.8 XHigh Fast",1000000,128000],["claude-opus-4-8-max-fast","Claude Opus 4.8 Max Fast",1000000,128000]]],
  ["claude-opus-5","Claude Opus 5",["opus"],[["claude-opus-5-medium","Claude Opus 5 Medium",1000000,128000],["claude-opus-5-low","Claude Opus 5 Low",1000000,128000],["claude-opus-5-high","Claude Opus 5 High",1000000,128000],["claude-opus-5-xhigh","Claude Opus 5 XHigh",1000000,128000],["claude-opus-5-max","Claude Opus 5 Max",1000000,128000],["claude-opus-5-low-fast","Claude Opus 5 Low Fast",1000000,128000],["claude-opus-5-medium-fast","Claude Opus 5 Medium Fast",1000000,128000],["claude-opus-5-high-fast","Claude Opus 5 High Fast",1000000,128000],["claude-opus-5-xhigh-fast","Claude Opus 5 XHigh Fast",1000000,128000],["claude-opus-5-max-fast","Claude Opus 5 Max Fast",1000000,128000]]],
  ["claude-5-fable","Claude Fable 5",[],[["claude-5-fable-low","Claude Fable 5 Low",1000000,128000],["claude-5-fable-medium","Claude Fable 5 Medium",1000000,128000],["claude-5-fable-high","Claude Fable 5 High",1000000,128000],["claude-5-fable-xhigh","Claude Fable 5 XHigh",1000000,128000],["claude-5-fable-max","Claude Fable 5 Max",1000000,128000]]],
  ["claude-sonnet-5","Claude Sonnet 5",["claude","sonnet"],[["claude-sonnet-5-low","Claude Sonnet 5 Low",1000000,128000],["claude-sonnet-5-medium","Claude Sonnet 5 Medium",1000000,128000],["claude-sonnet-5-high","Claude Sonnet 5 High",1000000,128000],["claude-sonnet-5-xhigh","Claude Sonnet 5 XHigh",1000000,128000],["claude-sonnet-5-max","Claude Sonnet 5 Max",1000000,128000]]],
  ["claude-haiku-5-5","Claude Haiku 5.5",[],[["claude-haiku-5-5-low","Claude Haiku 5.5 Low",1000000,128000],["claude-haiku-5-5-medium","Claude Haiku 5.5 Medium",1000000,128000],["claude-haiku-5-5-high","Claude Haiku 5.5 High",1000000,128000],["claude-haiku-5-5-xhigh","Claude Haiku 5.5 XHigh",1000000,128000],["claude-haiku-5-5-max","Claude Haiku 5.5 Max",1000000,128000]]],
  ["gemini-3.5-flash","Gemini 3.5 Flash",[],[["gemini-3-5-flash-minimal","Gemini 3.5 Flash Minimal",1048576,65535],["gemini-3-5-flash-low","Gemini 3.5 Flash Low",1048576,65535],["gemini-3-5-flash-medium","Gemini 3.5 Flash Medium",1048576,65535],["gemini-3-5-flash-high","Gemini 3.5 Flash High",1048576,65535]]],
  ["gemini-3.6-flash","Gemini 3.6 Flash",[],[["gemini-3-6-flash-minimal","Gemini 3.6 Flash Minimal",1048576,65535],["gemini-3-6-flash-low","Gemini 3.6 Flash Low",1048576,65535],["gemini-3-6-flash-medium","Gemini 3.6 Flash Medium",1048576,65535],["gemini-3-6-flash-high","Gemini 3.6 Flash High",1048576,65535]]],
  ["gemini-3-7-flash","Gemini 3.7 Flash",[],[["gemini-3-7-flash-low","Gemini 3.7 Flash Low",1048576,65535],["gemini-3-7-flash-medium","Gemini 3.7 Flash Medium",1048576,65535],["gemini-3-7-flash-high","Gemini 3.7 Flash High",1048576,65535]]],
  ["gpt-5.6-sol","GPT-5.6 Sol",[],[["gpt-5-6-sol-none","GPT-5.6 Sol No Thinking",1000000,128000],["gpt-5-6-sol-low","GPT-5.6 Sol Low Thinking",1000000,128000],["gpt-5-6-sol-medium","GPT-5.6 Sol Medium Thinking",1000000,128000],["gpt-5-6-sol-high","GPT-5.6 Sol High Thinking",1000000,128000],["gpt-5-6-sol-xhigh","GPT-5.6 Sol XHigh Thinking",1000000,128000],["gpt-5-6-sol-max","GPT-5.6 Sol Max Thinking",1000000,128000],["gpt-5-6-sol-none-priority","GPT-5.6 Sol No Thinking Fast",1000000,128000],["gpt-5-6-sol-low-priority","GPT-5.6 Sol Low Thinking Fast",1000000,128000],["gpt-5-6-sol-medium-priority","GPT-5.6 Sol Medium Thinking Fast",1000000,128000],["gpt-5-6-sol-high-priority","GPT-5.6 Sol High Thinking Fast",1000000,128000],["gpt-5-6-sol-xhigh-priority","GPT-5.6 Sol XHigh Thinking Fast",1000000,128000],["gpt-5-6-sol-max-priority","GPT-5.6 Sol Max Thinking Fast",1000000,128000]]],
  ["gpt-5.6-terra","GPT-5.6 Terra",[],[["gpt-5-6-terra-none","GPT-5.6 Terra No Thinking",1000000,128000],["gpt-5-6-terra-low","GPT-5.6 Terra Low Thinking",1000000,128000],["gpt-5-6-terra-medium","GPT-5.6 Terra Medium Thinking",1000000,128000],["gpt-5-6-terra-high","GPT-5.6 Terra High Thinking",1000000,128000],["gpt-5-6-terra-xhigh","GPT-5.6 Terra XHigh Thinking",1000000,128000],["gpt-5-6-terra-max","GPT-5.6 Terra Max Thinking",1000000,128000],["gpt-5-6-terra-none-priority","GPT-5.6 Terra No Thinking Fast",1000000,128000],["gpt-5-6-terra-low-priority","GPT-5.6 Terra Low Thinking Fast",1000000,128000],["gpt-5-6-terra-medium-priority","GPT-5.6 Terra Medium Thinking Fast",1000000,128000],["gpt-5-6-terra-high-priority","GPT-5.6 Terra High Thinking Fast",1000000,128000],["gpt-5-6-terra-xhigh-priority","GPT-5.6 Terra XHigh Thinking Fast",1000000,128000],["gpt-5-6-terra-max-priority","GPT-5.6 Terra Max Thinking Fast",1000000,128000]]],
  ["gpt-5.6-luna","GPT-5.6 Luna",[],[["gpt-5-6-luna-none","GPT-5.6 Luna No Thinking",1000000,128000],["gpt-5-6-luna-low","GPT-5.6 Luna Low Thinking",1000000,128000],["gpt-5-6-luna-medium","GPT-5.6 Luna Medium Thinking",1000000,128000],["gpt-5-6-luna-high","GPT-5.6 Luna High Thinking",1000000,128000],["gpt-5-6-luna-xhigh","GPT-5.6 Luna XHigh Thinking",1000000,128000],["gpt-5-6-luna-max","GPT-5.6 Luna Max Thinking",1000000,128000],["gpt-5-6-luna-none-priority","GPT-5.6 Luna No Thinking Fast",1000000,128000],["gpt-5-6-luna-low-priority","GPT-5.6 Luna Low Thinking Fast",1000000,128000],["gpt-5-6-luna-medium-priority","GPT-5.6 Luna Medium Thinking Fast",1000000,128000],["gpt-5-6-luna-high-priority","GPT-5.6 Luna High Thinking Fast",1000000,128000],["gpt-5-6-luna-xhigh-priority","GPT-5.6 Luna XHigh Thinking Fast",1000000,128000],["gpt-5-6-luna-max-priority","GPT-5.6 Luna Max Thinking Fast",1000000,128000]]],
  ["gpt-6-1-sol","GPT-6.1 Sol",[],[["gpt-6-1-sol-low","GPT-6.1 Sol Low Thinking",1000000,128000],["gpt-6-1-sol-medium","GPT-6.1 Sol Medium Thinking",1000000,128000],["gpt-6-1-sol-high","GPT-6.1 Sol High Thinking",1000000,128000],["gpt-6-1-sol-xhigh","GPT-6.1 Sol XHigh Thinking",1000000,128000],["gpt-6-1-sol-max","GPT-6.1 Sol Max Thinking",1000000,128000],["gpt-6-1-sol-low-priority","GPT-6.1 Sol Low Thinking Fast",1000000,128000],["gpt-6-1-sol-medium-priority","GPT-6.1 Sol Medium Thinking Fast",1000000,128000],["gpt-6-1-sol-high-priority","GPT-6.1 Sol High Thinking Fast",1000000,128000],["gpt-6-1-sol-xhigh-priority","GPT-6.1 Sol XHigh Thinking Fast",1000000,128000],["gpt-6-1-sol-max-priority","GPT-6.1 Sol Max Thinking Fast",1000000,128000]]],
  ["grok-4.5","Grok 4.5",[],[["grok-4-5-low","Grok 4.5 Low",500000,100000],["grok-4-5-medium","Grok 4.5 Medium",500000,100000],["grok-4-5-high","Grok 4.5 High",500000,100000]]],
  ["grok-4-6","Grok 4.6",[],[["grok-4-6-low","Grok 4.6 Low",500000,100000],["grok-4-6-medium","Grok 4.6 Medium",500000,100000],["grok-4-6-high","Grok 4.6 High",500000,100000],["grok-4-6-xhigh","Grok 4.6 XHigh",500000,100000]]],
  ["grok-4-7","Grok 4.7",[],[["grok-4-7-low","Grok 4.7 Low",500000,100000],["grok-4-7-medium","Grok 4.7 Medium",500000,100000],["grok-4-7-high","Grok 4.7 High",500000,100000],["grok-4-7-xhigh","Grok 4.7 XHigh",500000,100000]]],
  ["inkling","Inkling",[],[["inkling-none","Inkling None",1048576,131072],["inkling-low","Inkling Low",1048576,131072],["inkling-medium","Inkling Medium",1048576,131072],["inkling-high","Inkling High",1048576,131072],["inkling-xhigh","Inkling X-High",1048576,131072],["inkling-max","Inkling Max",1048576,131072]]],
  ["glm-5-3-flash","GLM-5.3 Flash",[],[["glm-5-3-flash-low","GLM-5.3 Flash Low",1000000,128000],["glm-5-3-flash-high","GLM-5.3 Flash High",1000000,128000],["glm-5-3-flash-max","GLM-5.3 Flash Max",1000000,128000]]],
  ["deepseek-v4-flash","DeepSeek V4 Flash",[],[["deepseek-v4-flash-high","DeepSeek V4 Flash High",1048576,384000],["deepseek-v4-flash-max","DeepSeek V4 Flash Max",1048576,384000]]],
  ["deepseek-v4-1-flash","DeepSeek V4.1 Flash",[],[["deepseek-v4-1-flash-high","DeepSeek V4.1 Flash High",1048576,384000],["deepseek-v4-1-flash-max","DeepSeek V4.1 Flash Max",1048576,384000]]],
  ["swe-1.7","SWE-1.7",[],[["swe-1-7","SWE-1.7 Max",262000,128000],["swe-1-7-medium","SWE-1.7 Medium",262000,128000]]],
  ["claude-opus-4.6","Claude Opus 4.6",[],[["claude-opus-4-6","Claude Opus 4.6",200000,128000],["claude-opus-4-6-thinking","Claude Opus 4.6 Thinking",200000,128000],["claude-opus-4-6-1m","Claude Opus 4.6 1M",1000000,128000],["claude-opus-4-6-thinking-1m","Claude Opus 4.6 Thinking 1M",1000000,128000]]],
  ["gpt-5.4","GPT-5.4",[],[["gpt-5-4-none","GPT-5.4 No Thinking",272000,128000],["gpt-5-4-low","GPT-5.4 Low Thinking",272000,128000],["gpt-5-4-medium","GPT-5.4 Medium Thinking",272000,128000],["gpt-5-4-high","GPT-5.4 High Thinking",272000,128000],["gpt-5-4-xhigh","GPT-5.4 XHigh Thinking",272000,128000],["gpt-5-4-none-priority","GPT-5.4 No Thinking Fast",272000,128000],["gpt-5-4-low-priority","GPT-5.4 Low Thinking Fast",272000,128000],["gpt-5-4-medium-priority","GPT-5.4 Medium Thinking Fast",272000,128000],["gpt-5-4-high-priority","GPT-5.4 High Thinking Fast",272000,128000],["gpt-5-4-xhigh-priority","GPT-5.4 XHigh Thinking Fast",272000,128000]]],
  ["gpt-5.5","GPT-5.5",["gpt"],[["gpt-5-5-none","GPT-5.5 No Thinking",272000,128000],["gpt-5-5-low","GPT-5.5 Low Thinking",272000,128000],["gpt-5-5-medium","GPT-5.5 Medium Thinking",272000,128000],["gpt-5-5-high","GPT-5.5 High Thinking",272000,128000],["gpt-5-5-xhigh","GPT-5.5 XHigh Thinking",272000,128000],["gpt-5-5-none-priority","GPT-5.5 No Thinking Fast",272000,128000],["gpt-5-5-low-priority","GPT-5.5 Low Thinking Fast",272000,128000],["gpt-5-5-medium-priority","GPT-5.5 Medium Thinking Fast",272000,128000],["gpt-5-5-high-priority","GPT-5.5 High Thinking Fast",272000,128000],["gpt-5-5-xhigh-priority","GPT-5.5 XHigh Thinking Fast",272000,128000]]],
  ["gpt-5.4-mini","GPT-5.4 Mini",[],[["gpt-5-4-mini-low","GPT-5.4 Mini Low Thinking",400000,128000],["gpt-5-4-mini-medium","GPT-5.4 Mini Medium Thinking",400000,128000],["gpt-5-4-mini-high","GPT-5.4 Mini High Thinking",400000,128000],["gpt-5-4-mini-xhigh","GPT-5.4 Mini XHigh Thinking",400000,128000]]],
  ["claude-sonnet-4.6","Claude Sonnet 4.6",[],[["claude-sonnet-4-6","Claude Sonnet 4.6",200000,128000],["claude-sonnet-4-6-thinking","Claude Sonnet 4.6 Thinking",200000,128000],["claude-sonnet-4-6-1m","Claude Sonnet 4.6 1M",1000000,128000],["claude-sonnet-4-6-thinking-1m","Claude Sonnet 4.6 Thinking 1M",1000000,128000]]],
  ["gpt-5.2","GPT-5.2",[],[["MODEL_GPT_5_2_LOW","GPT-5.2 Low Thinking",384000,128000],["MODEL_GPT_5_2_MEDIUM","GPT-5.2 Medium Thinking",384000,128000],["MODEL_GPT_5_2_NONE","GPT-5.2 No Thinking",384000,128000],["MODEL_GPT_5_2_HIGH","GPT-5.2 High Thinking",384000,128000],["MODEL_GPT_5_2_XHIGH","GPT-5.2 XHigh Thinking",384000,128000]]],
  ["claude-opus-4.5","Claude Opus 4.5",[],[["MODEL_CLAUDE_4_5_OPUS","Claude Opus 4.5",200000,64000],["MODEL_CLAUDE_4_5_OPUS_THINKING","Claude Opus 4.5 Thinking",200000,64000]]],
  ["Claude Haiku 4.5","Claude Haiku 4.5",[],[["MODEL_PRIVATE_11","Claude Haiku 4.5",200000,64000]]],
  ["claude-sonnet-4.5","Claude Sonnet 4.5",[],[["MODEL_PRIVATE_2","Claude Sonnet 4.5",200000,64000],["MODEL_PRIVATE_3","Claude Sonnet 4.5 Thinking",200000,64000]]],
  ["GPT-4.1","GPT-4.1",[],[["MODEL_CHAT_GPT_4_1_2025_04_14","GPT-4.1",1047576,32768]]],
  ["gpt-5.1","GPT-5.1",[],[["MODEL_PRIVATE_12","GPT-5.1 No Thinking",272000,128000],["MODEL_PRIVATE_13","GPT-5.1 Low Thinking",272000,128000],["MODEL_PRIVATE_14","GPT-5.1 Medium Thinking",272000,128000],["MODEL_PRIVATE_15","GPT-5.1 High Thinking",272000,128000]]],
  ["gpt-5.3-codex","GPT-5.3-Codex",["codex"],[["gpt-5-3-codex-low","GPT-5.3-Codex Low",400000,128000],["gpt-5-3-codex-medium","GPT-5.3-Codex Medium",400000,128000],["gpt-5-3-codex-high","GPT-5.3-Codex High",400000,128000],["gpt-5-3-codex-xhigh","GPT-5.3-Codex X-High",400000,128000],["gpt-5-3-codex-low-priority","GPT-5.3-Codex Low Fast",400000,128000],["gpt-5-3-codex-medium-priority","GPT-5.3-Codex Medium Fast",400000,128000],["gpt-5-3-codex-high-priority","GPT-5.3-Codex High Fast",400000,128000],["gpt-5-3-codex-xhigh-priority","GPT-5.3-Codex XHigh Fast",400000,128000]]],
  ["kimi-k2.6","Kimi K2.6",[],[["kimi-k2-6","Kimi K2.6",262144,8192]]],
  ["kimi-k2.7","Kimi K2.7",[],[["kimi-k2-7","Kimi K2.7",262144,16000]]],
  ["nemotron-3-ultra","Nemotron 3 Ultra",[],[["nemotron-3-ultra-none","Nemotron 3 Ultra None",1000000,32768],["nemotron-3-ultra-medium","Nemotron 3 Ultra Medium",1000000,32768],["nemotron-3-ultra-high","Nemotron 3 Ultra High",1000000,32768]]],
  ["swe-1.6","SWE-1.6",[],[["swe-1-6","SWE-1.6",200000,128000]]],
  ["swe-1.6-fast","SWE-1.6 Fast",[],[["swe-1-6-fast","SWE-1.6 Fast",200000,128000]]],
  ["gemini-3.1-pro","Gemini 3.1 Pro",[],[["gemini-3-1-pro-low","Gemini 3.1 Pro Low Thinking",1048576,65535],["gemini-3-1-pro-high","Gemini 3.1 Pro High Thinking",1048576,65535]]],
  ["gemini-3.0-flash","Gemini 3 Flash",[],[["MODEL_GOOGLE_GEMINI_3_0_FLASH_MINIMAL","Gemini 3 Flash Minimal",1048576,65535],["MODEL_GOOGLE_GEMINI_3_0_FLASH_LOW","Gemini 3 Flash Low",1048576,65535],["MODEL_GOOGLE_GEMINI_3_0_FLASH_MEDIUM","Gemini 3 Flash Medium",1048576,65535],["MODEL_GOOGLE_GEMINI_3_0_FLASH_HIGH","Gemini 3 Flash High",1048576,65535]]],
  ["deepseek-v4-pro","DeepSeek V4 Pro",[],[["deepseek-v4-pro-high","DeepSeek V4 Pro High",1048576,384000],["deepseek-v4-pro-max","DeepSeek V4 Pro Max",1048576,384000]]],
]

const familiesOf = (rows) => rows.map(([uid, label, aliases, vs]) => ({ uid, label, aliases, models: vs.map(([id, name, context, output]) => ({ id, name, context, output })) }))

// parseFamilies reads `devin models list --format json`.
function parseFamilies(text) {
  let list
  try {
    list = JSON.parse(text)
  } catch {
    return []
  }
  const out = []
  for (const f of list?.families ?? []) {
    if (!f?.family_uid) continue
    const low = String(f.family_uid).toLowerCase()
    if (low === "adaptive" || low === "fusion") continue
    out.push({
      uid: f.family_uid,
      label: f.family_label || f.family_uid,
      aliases: f.aliases ?? [],
      models: (f.variants ?? [])
        .filter((v) => v?.model_uid)
        .map((v) => ({ id: v.model_uid, name: v.label || v.model_uid, context: v.max_context_tokens || 0, output: v.max_output_tokens || 0 })),
    })
  }
  return out
}

const LEVEL_RANK = ["none", "minimal", "low", "medium", "high", "xhigh", "max"]
// the effort words Devin's variant ids end in (swe-2-high, …_HIGH,
// gpt-6-sol-none), and the level each is
const LEVELS = [["none", "none"], ["xhigh", "xhigh"], ["minimal", "minimal"], ["min", "minimal"], ["low", "low"], ["medium", "medium"], ["high", "high"], ["max", "max"]]
// the words a variant id may end in after its effort, for a quicker run of
// the same model at it (claude-opus-5-5-high-fast)
const TIERS = [["fast", "Fast"], ["priority", "Priority"]]

// levelOf is the effort an id says it runs at, "" when it says none.
function levelOf(id) {
  const low = id.toLowerCase()
  for (const [word, level] of LEVELS) if (low.endsWith("-" + word) || low.endsWith("_" + word)) return level
  return ""
}

// tierOf is the tier an id is in and its effort: claude-opus-5-5-high-fast
// is ["fast", "high"], swe-2-high ["", "high"]; a tier word with no effort
// before it (swe-1-6-fast) is no tier.
function tierOf(id) {
  const low = id.toLowerCase()
  for (const [word] of TIERS)
    for (const sep of ["-", "_"])
      if (low.endsWith(sep + word)) {
        const l = levelOf(low.slice(0, -(sep + word).length))
        if (l) return [word, l]
      }
  return ["", levelOf(id)]
}

const named = (f, id) => f.uid === id || f.aliases.includes(id)

// byLevel is the family's variant at each effort, in the tier, the first
// at each.
function byLevel(f, tier) {
  const out = {}
  for (const m of f.models) {
    const [t, l] = tierOf(m.id)
    if (t === tier && l && m.id !== f.uid && !out[l]) out[l] = m.id
  }
  return out
}

const efforts = (f, tier) => {
  const by = byLevel(f, tier)
  return LEVEL_RANK.filter((l) => by[l])
}

const tiers = (f) => TIERS.map(([w]) => w).filter((t) => Object.keys(byLevel(f, t)).length > 0)

// defaultLevel is the effort of the variant the family's id asks for when
// none is asked (its first), medium when that names none.
function defaultLevel(f) {
  if (f.models.length) {
    const [t, l] = tierOf(f.models[0].id)
    if (!t && l) return l
  }
  return "medium"
}

// nearest is the one of levels nearest the one asked for, a tie going up.
function nearest(want, levels) {
  const at = LEVEL_RANK.indexOf(want)
  if (at < 0 || !levels.length || levels.includes(want)) return want
  let best = want
  let dist = LEVEL_RANK.length
  for (const l of levels) {
    const i = LEVEL_RANK.indexOf(l)
    const d = Math.abs(i - at)
    if (d < dist || (d === dist && i > at)) [best, dist] = [l, d]
  }
  return best
}

// variantFor is the model to ask the API for: a family's id follows its
// newest model, which the API takes only as one of its variants — the one
// at the effort asked for, or the nearest the family has; with none asked,
// the family's default. A family's fast run is its fast variant at that
// effort. Any other id (a variant's own) goes as it is.
function variantFor(families, model, effort) {
  for (const f of families) {
    if (!named(f, model) || !f.models.length) continue
    if (effort) {
      const id = byLevel(f, "")[nearest(effort, efforts(f, ""))]
      if (id) return id
    }
    return f.models[0].id
  }
  for (const f of families)
    for (const tier of tiers(f)) {
      if (!model.endsWith("-" + tier) || !named(f, model.slice(0, -(tier.length + 1)))) continue
      const by = byLevel(f, tier)
      const levels = efforts(f, tier)
      return by[nearest(effort || defaultLevel(f), levels)] || by[levels[0]]
    }
  return model
}

// listed is Devin's list as it is offered: each family one model, with
// the efforts its variants are at (picked as any model's effort is, and
// turned into the variant by variantFor), its fast run likewise, and the
// variants at no effort (glm-5-2-1m); a family's only variant is its id.
// A variant in keep (one the user picked: swe-2-medium) stays, at the one
// effort its id is at, and goes to Devin as it is, as magpie's built-in
// keeps it (devinCollapse).
// Each entry says whether it takes images (image) as images, Devin's word
// for each variant id, gives it: a variant its own, a family (and its fast
// run) what all its variants Devin named say; undefined when Devin said
// nothing of it or its variants disagree. An entry is free (free: true) when
// every variant of Devin's it stands for is in free, the ids Devin says its
// quota doesn't count (parseFree). A family stands for the variants
// variantFor sends it as, those at an effort and its default, not every
// model Devin files under it: Devin gives SWE-2 three -lightning variants
// at the High cost tier (2026-10-15), which no effort of swe-2 is sent as,
// and with them in, swe-2 was no longer free. An entry's price (cost) is
// what Devin lists for the variants it stands for when they all list the
// same (parsePrices); none when they differ or Devin lists none.
function listed(families, keep = [], images = {}, free = new Set(), prices = new Map()) {
  const all = (ids) => ids.length > 0 && ids.every((id) => free.has(id))
  const priced = (ids) => {
    const ps = ids.map((id) => prices.get(id))
    if (!ps.length || !ps[0] || ps.some((p) => JSON.stringify(p) !== JSON.stringify(ps[0]))) return undefined
    return ps[0]
  }
  const own = (f, t) => {
    const ids = Object.values(byLevel(f, t))
    if (!t && f.models.length && !ids.includes(f.models[0].id)) ids.push(f.models[0].id)
    return ids
  }
  const out = []
  const seen = new Set()
  const add = ({ free, cost, ...m }) => {
    if (seen.has(m.id)) return
    seen.add(m.id)
    out.push({ ...m, ...(free ? { free } : cost ? { cost } : {}) })
  }
  for (const f of families) {
    const context = f.models.find((m) => m.context)?.context ?? 0
    const output = f.models.find((m) => m.output)?.output ?? 0
    const image = familyImages(f, images)
    add({ id: f.uid, name: f.label, context, output, efforts: efforts(f, ""), image, free: all(own(f, "")), cost: priced(own(f, "")) })
    for (const t of tiers(f)) {
      const id = f.uid + "-" + t
      if (families.some((g) => named(g, id))) continue
      add({ id, name: f.label + " " + TIERS.find(([w]) => w === t)[1], context, output, efforts: efforts(f, t), image, free: all(own(f, t)), cost: priced(own(f, t)) })
    }
    if (f.models.length === 1) continue
    for (const m of f.models) {
      if (m.id === f.uid || tierOf(m.id)[1]) continue
      add({ id: m.id, name: m.name, context: m.context || context, output: m.output || output, efforts: [], image: images[m.id], free: free.has(m.id), cost: prices.get(m.id) })
    }
  }
  for (const id of keep) {
    if (seen.has(id)) continue
    const f = families.find((g) => g.models.some((m) => m.id === id))
    const m = f?.models.find((m) => m.id === id)
    const level = tierOf(id)[1]
    if (!m && !level) continue
    add({ id, name: m?.name || id, context: m?.context ?? 0, output: m?.output ?? 0, efforts: level ? [level] : [], image: images[id], free: free.has(id), cost: prices.get(id) })
  }
  return out
}

// familyImages is what a family's variants Devin named all say of images.
function familyImages(f, images) {
  let said
  for (const m of f.models) {
    const v = images[m.id]
    if (typeof v !== "boolean") continue
    if (said !== undefined && said !== v) return undefined
    said = v
  }
  return said
}

// Which of Devin's models take images is what Devin tells its CLI's model
// picker (GetCliModelConfigs: each ClientModelConfig's supports_images), as
// magpie's built-in asks it (devin_images.go): `devin models list` says
// nothing of images and models.dev doesn't know Devin's own ids, so swe-2
// was said to take none and agents dropped its images (面条 on magpie's
// Discord). NO_IMAGES are the ids Devin said take none, as of 2026-10-05;
// every other id in SNAPSHOT takes them, which is what a list is told
// before Devin is asked, or when it can't be.
const NO_IMAGES = new Set([
  "glm-5-2", "glm-5-2-max", "glm-5-2-1m", "glm-5-2-max-1m", "glm-5-2-none", "glm-5-2-none-1m",
  "glm-5-3-low", "glm-5-3-high", "glm-5-3-max",
  "inkling-none", "inkling-low", "inkling-medium", "inkling-high", "inkling-xhigh", "inkling-max",
  "deepseek-v4-flash-high", "deepseek-v4-flash-max", "deepseek-v4-pro-high", "deepseek-v4-pro-max",
  "nemotron-3-ultra-none", "nemotron-3-ultra-medium", "nemotron-3-ultra-high",
])
const SNAPSHOT_IMAGES = Object.fromEntries(SNAPSHOT.flatMap(([, , , vs]) => vs.map(([id]) => [id, !NO_IMAGES.has(id)])))

const MODEL_CONFIGS_RPC = "/exa.api_server_pb.ApiServerService/GetCliModelConfigs"

// parseModelConfigs reads GetCliModelConfigsResponse: its
// client_model_configs (1), each with its model_uid (22) and
// supports_images (5), a bool proto3 leaves out when false.
function parseModelConfigs(b) {
  const out = {}
  for (const f of fields(b)) {
    if (f.num !== 1 || f.wire !== 2) continue
    let uid = ""
    let images = false
    for (const g of fields(f.data)) {
      if (g.num === 22 && g.wire === 2) uid = dec.decode(g.data)
      else if (g.num === 5 && g.wire === 0) images = g.n !== 0
    }
    if (uid) out[uid] = images
  }
  return Object.keys(out).length ? out : null
}

// FREE_TIER is the model_cost_tier (ClientModelConfig's 24) of a model the
// account's quota doesn't count: the CLI's picker labels its tiers "Low
// cost", "Med cost", "High cost" and "Free", 1 to 4. On 2026-10-14 Devin
// gave SWE-2's three variants 4 and no other model of its 262 (太陽と向日葵
// on magpie's Discord: with the quota used up, SWE-2 still answered, but
// magpie held the account for it as for every other model).
const FREE_TIER = 4

// parseFree reads the same response for the ids of the models that are
// free: each config's model_uid (22) whose model_cost_tier (24) is
// FREE_TIER. A config that says no tier is not free.
function parseFree(b) {
  const out = new Set()
  for (const f of fields(b)) {
    if (f.num !== 1 || f.wire !== 2) continue
    let uid = ""
    let tier = 0
    for (const g of fields(f.data)) {
      if (g.num === 22 && g.wire === 2) uid = dec.decode(g.data)
      else if (g.num === 24 && g.wire === 0) tier = g.n
    }
    if (uid && tier === FREE_TIER) out.add(uid)
  }
  return out
}

// parsePrices reads the same response for what Devin lists each model at,
// in USD per 1M tokens: a config's model_uid (22) and its prices (32, one
// each), each a label (1: "Input", "Cached input", "Output"), a fixed32
// float (2) and its unit (3: "1M tokens"). A model in the Free tier lists
// none. Only a price per 1M tokens is taken, and a model is priced only
// when it lists input or output above 0.
function parsePrices(b) {
  const out = new Map()
  const words = { Input: "input", "Cached input": "read", Output: "output" }
  for (const f of fields(b)) {
    if (f.num !== 1 || f.wire !== 2) continue
    let uid = ""
    const p = {}
    for (const g of fields(f.data)) {
      if (g.num === 22 && g.wire === 2) uid = dec.decode(g.data)
      else if (g.num === 32 && g.wire === 2) {
        let label = ""
        let unit = ""
        let v = NaN
        for (const h of fields(g.data)) {
          if (h.num === 1 && h.wire === 2) label = dec.decode(h.data)
          else if (h.num === 3 && h.wire === 2) unit = dec.decode(h.data)
          else if (h.num === 2 && h.wire === 5) v = new DataView(h.data.buffer, h.data.byteOffset, 4).getFloat32(0, true)
        }
        // a float32's 1.4 is 1.39999997…: the price as Devin wrote it
        if (words[label] && unit === "1M tokens" && Number.isFinite(v) && v >= 0) p[words[label]] = +v.toPrecision(6)
      }
    }
    if (uid && (p.input > 0 || p.output > 0)) out.set(uid, { input: p.input ?? 0, output: p.output ?? 0, cache: { read: p.read ?? 0 } })
  }
  return out
}

// parseConfigFamilies reads the same response as the families `devin
// models list` makes of it, in its order: each config's label (1), context
// (18), and model_info (23) with its output (13) and family uid (23); the
// family's label is its family_metadata's (30) first field. A config that
// names no family is a family of its own under its label, as the CLI makes
// MODEL_PRIVATE_11 "Claude Haiku 4.5". The aliases are the CLI's own, so
// they are taken from SNAPSHOT's family of the same id.
function parseConfigFamilies(b) {
  const fams = new Map()
  for (const f of fields(b)) {
    if (f.num !== 1 || f.wire !== 2) continue
    let uid = ""
    let name = ""
    let context = 0
    let output = 0
    let fam = ""
    let famLabel = ""
    for (const g of fields(f.data)) {
      if (g.num === 22 && g.wire === 2) uid = dec.decode(g.data)
      else if (g.num === 1 && g.wire === 2) name = dec.decode(g.data)
      else if (g.num === 18 && g.wire === 0) context = g.n
      else if (g.num === 23 && g.wire === 2) {
        for (const h of fields(g.data)) {
          if (h.num === 13 && h.wire === 0) output = h.n
          else if (h.num === 23 && h.wire === 2) fam = dec.decode(h.data)
        }
      } else if (g.num === 30 && g.wire === 2) {
        for (const h of fields(g.data)) if (h.num === 1 && h.wire === 2) famLabel = dec.decode(h.data)
      }
    }
    if (!uid) continue
    const id = fam || famLabel || name || uid
    const low = id.toLowerCase()
    if (low === "adaptive" || low === "fusion") continue
    let e = fams.get(id)
    if (!e) {
      const known = SNAPSHOT.find(([u]) => u === id)
      fams.set(id, (e = { uid: id, label: famLabel || name || id, aliases: known ? known[2] : [], models: [] }))
    }
    e.models.push({ id: uid, name: name || uid, context, output })
  }
  return [...fams.values()]
}

// Each account's word on images and its list, kept and asked again as the
// CLI's list is.
const said = new Map()

// configsFor is what Devin last said of key's account's models
// ({images, families}, each null until it has said); with wait false, a
// first ask isn't waited for.
async function configsFor(key, server, wait = true) {
  let c = said.get(key)
  if (!c) said.set(key, (c = { images: null, families: null, free: new Set(), prices: new Map(), at: 0, failed: 0, asking: null }))
  const ask = () =>
    (c.asking ??= (async () => {
      try {
        const res = await fetch(server + MODEL_CONFIGS_RPC, {
          method: "POST",
          headers: { "Content-Type": "application/proto", "Connect-Protocol-Version": "1" },
          body: new PB().bytes(1, metadata(key)).done(),
          signal: AbortSignal.timeout(15_000),
        })
        if (!res.ok) throw new Error(`GetCliModelConfigs: ${res.status}`)
        const b = new Uint8Array(await res.arrayBuffer())
        const images = parseModelConfigs(b)
        if (!images) throw new Error("GetCliModelConfigs: no models")
        const families = parseConfigFamilies(b)
        Object.assign(c, { images, families: families.length ? families : null, free: parseFree(b), prices: parsePrices(b), at: Date.now(), failed: 0 })
      } catch {
        c.failed = Date.now()
      } finally {
        c.asking = null
      }
    })())
  if (!c.images || Date.now() - c.at >= FRESH) {
    if (Date.now() - c.failed >= RETRY) {
      const p = ask()
      if (wait && !c.images) await p
    }
  }
  return c
}

// imagesFor is what Devin says of each model id's images for key's
// account, SNAPSHOT_IMAGES under it; with wait false, a first ask isn't
// waited for.
async function imagesFor(key, server, wait = true) {
  const c = await configsFor(key, server, wait)
  return { ...SNAPSHOT_IMAGES, ...(c.images ?? {}) }
}

// seesImages is whether a model Devin said nothing of takes images, as
// magpie's built-in tells it (models.go: m.Images || catalog.SeesImages):
// models.dev's word for the id — most of the providers listing it taking
// images — from the catalog magpie's `magpie sync` or OpenCode keeps, and
// no when neither has it.
let seen = null
function seesImages(id) {
  if (!seen) {
    seen = new Set()
    const cache = process.env.XDG_CACHE_HOME || join(homedir(), ".cache")
    for (const p of [join(cache, "magpie", "models.json"), join(homedir(), ".cache", "opencode", "models.json")]) {
      let m
      try {
        m = JSON.parse(readFileSync(p, "utf8"))
      } catch {
        continue
      }
      if (!m || typeof m !== "object" || !Object.keys(m).length) continue
      const votes = new Map()
      for (const pr of Object.values(m))
        for (const [mid, x] of Object.entries(pr?.models ?? {})) {
          const b = bare(mid)
          votes.set(b, (votes.get(b) ?? 0) + ((x?.modalities?.input ?? []).includes("image") ? 1 : -1))
        }
      for (const [b, v] of votes) if (v > 0) seen.add(b)
      break
    }
  }
  return seen.has(bare(id))
}
const bare = (id) => String(id).toLowerCase().split("/").pop()

function configModel(m) {
  const image = m.image ?? seesImages(m.id)
  return {
    id: m.id,
    name: m.name,
    tool_call: true,
    reasoning: m.efforts.length > 0,
    attachment: image,
    temperature: true,
    modalities: { input: image ? ["text", "image"] : ["text"], output: ["text"] },
    limit: { context: m.context, output: m.output },
    ...(m.efforts.length ? { variants: Object.fromEntries(m.efforts.map((e) => [e, { reasoningEffort: e }])) } : {}),
  }
}

function runtimeModel(m) {
  const image = m.image ?? seesImages(m.id)
  return {
    id: m.id,
    providerID: ID,
    name: m.name,
    api: { id: m.id, url: BASE, npm: CHAT },
    status: "active",
    headers: {},
    options: {},
    cost: { input: m.cost?.input ?? 0, output: m.cost?.output ?? 0, cache: { read: m.cost?.cache?.read ?? 0, write: 0 } },
    limit: { context: m.context, output: m.output },
    capabilities: {
      temperature: true,
      reasoning: m.efforts.length > 0,
      attachment: image,
      toolcall: true,
      input: { text: true, image, audio: false, video: false, pdf: false },
      output: { text: true, image: false, audio: false, video: false, pdf: false },
      interleaved: false,
    },
    release_date: "",
    variants: Object.fromEntries(m.efforts.map((e) => [e, { reasoningEffort: e }])),
    // magpie's: a model the account's quota doesn't count
    ...(m.free ? { free: true } : {}),
  }
}

// withImages is a list of models with what entries (listed's) say of their
// images on them; a model they say nothing of is left as it was.
function withImages(models, entries) {
  const by = new Map(entries.map((m) => [m.id, m.image]))
  return Object.fromEntries(
    Object.entries(models ?? {}).map(([id, m]) => {
      const image = by.get(id)
      if (typeof image !== "boolean") return [id, m]
      const caps = m?.capabilities ?? {}
      return [id, { ...m, capabilities: { ...caps, attachment: image, input: { ...(caps.input ?? { text: true }), image } } }]
    }),
  )
}

// ---- the devin CLI -----------------------------------------------------------------

// cliPath finds the devin CLI, "" without one.
function cliPath() {
  const exe = process.platform === "win32" ? "devin.exe" : "devin"
  for (const dir of (process.env.PATH ?? "").split(process.platform === "win32" ? ";" : ":"))
    if (dir && isFile(join(dir, exe))) return join(dir, exe)
  for (const p of [join(homedir(), ".local", "bin", "devin"), "/usr/local/bin/devin", "/opt/homebrew/bin/devin"]) if (isFile(p)) return p
  return ""
}

function isFile(p) {
  try {
    return statSync(p).isFile()
  } catch {
    return false
  }
}

// cliCredentialsPath is where the CLI keeps its own sign-in.
function cliCredentialsPath() {
  if (process.platform === "win32" && process.env.APPDATA) return join(process.env.APPDATA, "devin", "credentials.toml")
  return join(process.env.XDG_DATA_HOME || join(homedir(), ".local", "share"), "devin", "credentials.toml")
}

const tomlString = (s) => '"' + String(s).replaceAll("\\", "\\\\").replaceAll('"', '\\"') + '"'

// credentials is credentials.toml as `devin auth login` writes it.
function credentials(key, server, webapp, api) {
  return [
    ["windsurf_api_key", key],
    ["api_server_url", server || SERVER],
    ["devin_webapp_host", webapp || "app.devin.ai"],
    ["devin_api_url", api || "https://api.devin.ai"],
  ]
    .map(([k, v]) => `${k} = ${tomlString(v)}\n`)
    .join("")
}

// readCredentials reads the keys of a credentials.toml the CLI wrote.
function readCredentials(text) {
  const out = {}
  for (const line of text.split(/\r?\n/)) {
    const m = /^\s*([A-Za-z0-9_]+)\s*=\s*(?:"((?:[^"\\]|\\.)*)"|'([^']*)')\s*$/.exec(line)
    if (m) out[m[1]] = m[2] !== undefined ? m[2].replace(/\\(.)/g, "$1") : m[3]
  }
  return out
}

// homeFor is a data folder of this plugin's own, laid out as the CLI's,
// with the account's credentials in it: the CLI run with it as its data
// folder answers for that account, and its own sign-in is never touched.
async function homeFor(key, server) {
  const base = join(process.env.XDG_CACHE_HOME || join(homedir(), ".cache"), "opencode-devin-auth")
  const home = join(base, createHash("sha256").update(key).digest("hex").slice(0, 16))
  const file = join(home, "devin", "credentials.toml")
  if (!existsSync(file)) {
    await mkdir(join(home, "devin"), { recursive: true, mode: 0o700 })
    await writeFile(file, credentials(key, server), { mode: 0o600 })
  }
  return home
}

// runCLI runs the devin CLI, for the account in home ("" for its own),
// with its output; it asks Devin's servers, so it has 30 seconds.
function runCLI(home, ...args) {
  const path = cliPath()
  if (!path) return Promise.reject(new Error("devin is not installed"))
  const env = { ...process.env }
  if (home) {
    for (const k of Object.keys(env)) if (["XDG_DATA_HOME", "APPDATA"].includes(k.toUpperCase())) delete env[k]
    env.XDG_DATA_HOME = home
    env.APPDATA = home
  }
  return new Promise((resolve, reject) => {
    const p = spawn(path, args, { env, stdio: ["ignore", "pipe", "ignore"] })
    let out = ""
    p.stdout.on("data", (b) => (out += b))
    const timer = setTimeout(() => p.kill(), 30_000)
    p.on("error", (e) => {
      clearTimeout(timer)
      reject(e)
    })
    p.on("close", (code) => {
      clearTimeout(timer)
      resolve({ out, code })
    })
  })
}

// signedOut says the CLI's report is one of nobody signed in: it says so,
// or Devin's servers refused the account's token.
const signedOut = (out) => ["Not logged in", "Authentication required", "Invalid token", "try logging out and logging in again"].some((s) => out.includes(s))

// statusField is a field of `devin auth status`'s report, "" without it.
function statusField(out, key) {
  for (const l of out.split(/\r?\n/)) {
    const t = l.trim()
    if (t.startsWith(key + ":")) return t.slice(key.length + 1).trim()
  }
  return ""
}

// parseStatus reads `devin auth status`'s report: its User's Email (or
// Name), its Account's Tier (or Plan), and its Team ID when one is shown.
function parseStatus(out) {
  if (!out.includes("Logged in")) return null
  const field = (key) => statusField(out, key)
  const teamId = field("Team ID")
  return { user: field("Email") || field("Name"), plan: field("Tier") || field("Plan"), ...(teamId ? { teamId } : {}) }
}

// inside says file is in dir, whichever way either is spelled.
function inside(file, dir) {
  const real = (p) => {
    let r
    try {
      r = realpathSync(p)
    } catch {
      r = resolve(p)
    }
    return process.platform === "win32" ? r.toLowerCase() : r
  }
  const rel = relative(real(dir), real(file))
  return rel !== "" && !rel.startsWith("..") && !isAbsolute(rel)
}

// readsHome says the CLI's report is of the credentials.toml in home: the
// devin CLI on Windows reads %APPDATA%\devin\ whatever APPDATA it is run
// with (#17), and so answers for its own account, not home's. A report that
// names no file is taken at its word.
function readsHome(out, home) {
  const file = statusField(out, "File")
  return !home || !file || inside(file, home)
}

// identity is who the CLI says is signed in in home: {user, plan}, null
// when it says nobody is, undefined when it can't tell (no CLI, no answer,
// a CLI that read another credentials.toml than home's).
async function identity(home) {
  try {
    const { out } = await runCLI(home, "auth", "status")
    if (!readsHome(out, home)) return undefined
    const st = parseStatus(out)
    if (st?.user) return st
    if (signedOut(out)) return null
  } catch {}
  return undefined
}

// tierName words a teamsTier as `devin auth status` says it:
// TEAMS_TIER_DEVIN_PRO is "Devin Pro", TEAMS_TIER_DEVIN_TEAMS_V2 "Devin
// Teams" (the CLI leaves the tier's version out).
const tierName = (t) =>
  String(t ?? "")
    .replace(/^TEAMS_TIER_/, "")
    .split("_")
    .filter((w) => w && w !== "UNSPECIFIED" && !/^V\d+$/.test(w))
    .map((w) => w[0] + w.slice(1).toLowerCase())
    .join(" ")

// whoOfStatus names the account GetUserStatus describes: its email (or
// handle), and for a seat in an organization the organization's display
// name too ("email · org"), so two organizations under one email are kept
// apart as two accounts with their own quota pools.
function whoOfStatus(u = {}) {
  const base = String(u.email || u.name || "").trim()
  if (!base) return undefined
  const info = u.planStatus?.planInfo ?? {}
  const devin = info.devinInfo ?? {}
  const org = String(devin.accountDisplayName || info.teamName || u.teamName || "").trim()
  const orgId = String(devin.orgId || u.teamId || "").trim()
  const teamId = String(u.teamId || "").trim()
  const named = org && org.toLowerCase() !== base.toLowerCase()
  const user = named ? `${base} · ${org}` : base
  return {
    user,
    ...(named ? { email: base } : {}),
    plan: tierName(u.teamsTier || info.teamsTier) || info.planName || "",
    ...(orgId ? { orgId } : {}),
    ...(teamId ? { teamId } : {}),
  }
}

// whoByKey is whose key it is, as GetUserStatus (usage's) says, asked with
// the key itself: {user, plan, orgId?, teamId?}, null when Devin refuses
// the key, undefined when it can't tell.
async function whoByKey(key, server) {
  let res, text
  try {
    res = await userStatus(key, server || SERVER)
    text = await res.text()
  } catch {
    return undefined
  }
  if (res.status === 401 || failure(res.status, text).status === 401) return null
  if (!res.ok) return undefined
  let u
  try {
    u = JSON.parse(text || "{}")?.userStatus ?? {}
  } catch {
    return undefined
  }
  return whoOfStatus(u)
}

// cliAnswersFor says whether the CLI, run with home as its data folder,
// answers for key's account: the account it names is the one Devin says
// the key is. A CLI that reads another credentials.toml than home's (on
// Windows it reads %APPDATA%\devin\ whatever APPDATA it is given, #17)
// answers only when it names that account; one that reads home's is taken
// at its word when Devin can't tell. undefined when it can't tell.
async function cliAnswersFor(key, server, home) {
  let out
  try {
    ;({ out } = await runCLI(home, "auth", "status"))
  } catch {
    return undefined
  }
  const own = readsHome(out, home)
  const st = parseStatus(out)
  if (!st?.user) return own
  const who = await whoByKey(key, server)
  if (!who?.user) return own
  if (who.teamId && st.teamId && who.teamId !== st.teamId) return false
  return (who.email || who.user).toLowerCase() === st.user.toLowerCase()
}

// Each account's model list as the CLI last gave it, kept FRESH; one past
// its time is served while it is read again, one that failed is asked
// again after RETRY. A CLI that doesn't answer for the account (#17: on
// Windows it lists its own account's) is never asked for it.
const lists = new Map()

// familiesFor is key's account's list: what Devin tells the CLI's model
// picker for the key (GetCliModelConfigs, the list `devin models list`
// prints, asked with the key itself, so on any system and for every
// account), else the CLI's when it answers for the account, else Devin's
// list as of SNAPSHOT. On Windows the CLI answered for no account added
// in the browser, and an account without the CLI had only SNAPSHOT, so
// neither got Gemini 4 Argon (plugins #115).
async function familiesFor(key, server, wait = true) {
  return (await ownFamilies(key, server, wait)) ?? familiesOf(SNAPSHOT)
}

// ownFamilies is familiesFor's list when Devin or the CLI gave one, null
// when neither has.
async function ownFamilies(key, server, wait = true) {
  const told = (await configsFor(key, server, wait)).families
  if (told) return told
  let c = lists.get(key)
  if (!c) lists.set(key, (c = { families: null, at: 0, failed: 0, asking: null, cli: undefined }))
  const ask = () =>
    (c.asking ??= (async () => {
      try {
        const home = await homeFor(key, server)
        if (c.cli === undefined) c.cli = await cliAnswersFor(key, server, home)
        if (c.cli === false) return
        const { out } = await runCLI(home, "models", "list", "--format", "json")
        const fams = parseFamilies(out)
        if (!fams.length) throw new Error("devin models list: no models")
        Object.assign(c, { families: fams, at: Date.now(), failed: 0 })
      } catch {
        c.failed = Date.now()
      } finally {
        c.asking = null
      }
    })())
  if (c.families) {
    if (Date.now() - c.at >= FRESH && Date.now() - c.failed >= RETRY && c.cli !== false && cliPath()) ask()
    return c.families
  }
  if (c.cli !== false && cliPath() && Date.now() - c.failed >= RETRY) {
    const p = ask()
    if (wait) await p
  }
  return c.families
}

// ---- signing in ----------------------------------------------------------------------

function page(ok, title, text) {
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c])
  return `<!doctype html><meta charset="utf-8"><title>${esc(title)}</title>
<style>body{font:15px system-ui,sans-serif;display:grid;place-items:center;min-height:90vh;margin:0;color:#222;background:#fafafa}
@media (prefers-color-scheme:dark){body{color:#eee;background:#161616}}main{text-align:center;max-width:28rem;padding:1rem}
.d{font-size:2rem;color:${ok ? "#2a9d5c" : "#c0392b"}}</style>
<main><div class="d">${ok ? "✓" : "✕"}</div><h1>${esc(title)}</h1><p>${esc(text)}</p></main>`
}

// exchange trades the callback's code for the session token `devin auth
// login` keeps — already shaped "devin-session-token$<jwt>" — and the
// hosts the account is on.
async function exchange(code, verifier, redirect) {
  const res = await fetch(SERVER + EXCHANGE, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ code, code_verifier: verifier, redirect_uri: redirect }),
    signal: AbortSignal.timeout(30_000),
  })
  const text = await res.text()
  if (!res.ok) throw new Error(`Devin's exchange: ${res.status} ${text.slice(0, 300)}`)
  let r = {}
  try {
    r = JSON.parse(text)
  } catch {}
  const key = r.sessionToken || r.session_token
  if (!key) throw new Error("Devin's exchange returned no session token")
  return { key, webapp: r.devinWebappHost, api: r.devinApiUrl }
}

// success names a key's account as Devin does when asked with the key
// (GetUserStatus), or, when Devin can't tell, as the CLI does in a data
// folder of the plugin's own: never by the CLI alone, which on Windows
// names its own account whatever key it is given (#17). A key Devin or the
// CLI says nobody is signed in with fails it; with neither able to tell,
// the account is just "Devin".
async function success(key, server) {
  let who = await whoByKey(key, server)
  if (who === undefined) who = await identity(await homeFor(key, server))
  if (who === null) throw new Error("Devin didn't take the sign-in: nobody is signed in with the token it gave")
  const metadata = { email: who?.user || "Devin" }
  if (who?.plan) metadata.plan = who.plan
  const orgId = who?.orgId || who?.teamId
  if (orgId) metadata.orgId = orgId
  if (who?.teamId) metadata.teamId = who.teamId
  if (server && server !== SERVER) metadata.server = server
  return { type: "success", provider: ID, key, metadata }
}

// browserSignIn is `devin auth login`'s round, run by the plugin: PKCE
// through app.devin.ai, back to a callback on 127.0.0.1.
async function browserSignIn() {
  const verifier = randomBytes(48).toString("base64url")
  const state = randomBytes(24).toString("base64url")
  const challenge = createHash("sha256").update(verifier).digest("base64url")
  let over = false
  let settle
  const done = new Promise((r) => (settle = r))
  const finish = (result) => {
    if (over) return
    over = true
    settle(result)
  }
  let redirect = ""
  const server = createServer(async (req, res) => {
    const url = new URL(req.url ?? "/", "http://127.0.0.1")
    const html = (code, body) => {
      res.writeHead(code, { "Content-Type": "text/html; charset=utf-8" })
      res.end(body)
    }
    if (url.pathname !== "/callback") return res.writeHead(404).end()
    const q = url.searchParams
    if (over) return html(200, page(false, "This sign-in is over", "Start it again."))
    if (q.get("error")) {
      const msg = q.get("error_description") || q.get("error")
      finish({ type: "failed", error: msg })
      return html(200, page(false, "Sign-in didn't finish", msg))
    }
    if (q.get("state") !== state || !q.get("code")) return html(200, page(false, "This link isn't from this sign-in", "Start it again."))
    try {
      const x = await exchange(q.get("code"), verifier, redirect)
      const r = await success(x.key, SERVER)
      finish(r)
      html(200, page(true, "You're signed in", `${r.metadata.email} is signed in. You can close this tab.`))
    } catch (e) {
      finish({ type: "failed", error: e.message })
      html(200, page(false, "Sign-in didn't finish", e.message))
    }
  })
  await new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", resolve)
  })
  redirect = `http://127.0.0.1:${server.address().port}/callback`
  const timer = setTimeout(() => finish({ type: "failed", error: "the sign-in timed out" }), SIGN_IN_TIMEOUT)
  done.then(() => {
    clearTimeout(timer)
    setTimeout(() => server.close(), 5_000).unref?.()
  })
  const q = new URLSearchParams({
    redirect_uri: redirect,
    state,
    prompt: "select_account",
    code_challenge: challenge,
    code_challenge_method: "S256",
    cli_pkce_marker: "1",
  })
  return {
    url: `${AUTHORIZE}?${q}`,
    instructions: "Sign in to Devin in the browser.",
    method: "auto",
    callback: () => done,
  }
}

// cliSignIn takes the account the devin CLI is signed in to, reading its
// credentials.toml and changing nothing.
async function cliSignIn() {
  const path = cliCredentialsPath()
  return {
    url: "",
    instructions: `Uses the account in ${path}.`,
    method: "auto",
    callback: async () => {
      try {
        const c = readCredentials(await readFile(path, "utf8"))
        if (!c.windsurf_api_key) return { type: "failed", error: `Devin isn't signed in: ${path} has no key` }
        const r = await success(c.windsurf_api_key, (c.api_server_url || SERVER).replace(/\/+$/, ""))
        r.metadata.cli = true
        return r
      } catch (e) {
        return { type: "failed", error: e?.code === "ENOENT" ? "Devin isn't signed in: run `devin auth login`" : e.message }
      }
    },
  }
}

// ---- protobuf, by hand ---------------------------------------------------------------

const enc = new TextEncoder()
const dec = new TextDecoder()

// pb builds a protobuf message field by field.
class PB {
  constructor() {
    this.parts = []
  }
  uvarint(n) {
    const b = []
    n = BigInt(n)
    while (n >= 0x80n) {
      b.push(Number(n & 0x7fn) | 0x80)
      n >>= 7n
    }
    b.push(Number(n))
    this.parts.push(Uint8Array.from(b))
  }
  varint(num, v) {
    this.uvarint((num << 3) | 0)
    this.uvarint(v)
    return this
  }
  bytes(num, v) {
    if (typeof v === "string") v = enc.encode(v)
    else if (v instanceof PB) v = v.done()
    this.uvarint((num << 3) | 2)
    this.uvarint(v.length)
    this.parts.push(v)
    return this
  }
  str(num, v) {
    return this.bytes(num, v)
  }
  double(num, v) {
    this.uvarint((num << 3) | 1)
    const b = new Uint8Array(8)
    new DataView(b.buffer).setFloat64(0, v, true)
    this.parts.push(b)
    return this
  }
  done() {
    const n = this.parts.reduce((s, p) => s + p.length, 0)
    const out = new Uint8Array(n)
    let at = 0
    for (const p of this.parts) {
      out.set(p, at)
      at += p.length
    }
    return out
  }
}

// fields reads a message's fields; a malformed tail is dropped.
function fields(b) {
  const out = []
  let i = 0
  const uvarint = () => {
    let n = 0n
    let shift = 0n
    for (;;) {
      if (i >= b.length) return null
      const c = b[i++]
      n |= BigInt(c & 0x7f) << shift
      if (!(c & 0x80)) return n
      shift += 7n
    }
  }
  while (i < b.length) {
    const key = uvarint()
    if (key === null) break
    const f = { num: Number(key >> 3n), wire: Number(key & 7n), n: 0, data: null }
    if (f.wire === 0) {
      const v = uvarint()
      if (v === null) break
      f.n = Number(v)
    } else if (f.wire === 1) {
      if (i + 8 > b.length) break
      i += 8
    } else if (f.wire === 2) {
      const l = uvarint()
      if (l === null || i + Number(l) > b.length) break
      f.data = b.subarray(i, i + Number(l))
      i += Number(l)
    } else if (f.wire === 5) {
      if (i + 4 > b.length) break
      f.data = b.subarray(i, i + 4)
      i += 4
    } else break
    out.push(f)
  }
  return out
}

// frame wraps a message as the one frame of a Connect stream. A message
// past GZIP_AT goes gzipped (flag 1, with Connect-Content-Encoding: gzip),
// as the devin CLI sends it: Devin's nginx turns away a body past 10 MiB on
// the wire (413). The CLI (3000.11.92) gzips a GetChatMessage of 8,333
// bytes and sends one of 8,083 as it is, and names the encoding only on a
// gzipped request; it sends no Connect-Accept-Encoding.
const GZIP_AT = 8 << 10
function frame(msg) {
  const zip = msg.length > GZIP_AT
  const body = zip ? gzipSync(msg) : msg
  const out = new Uint8Array(5 + body.length)
  out[0] = zip ? 1 : 0
  new DataView(out.buffer).setUint32(1, body.length)
  out.set(body, 5)
  return out
}

// frames reads a Connect stream: each message, or its end with its JSON.
async function* frames(body) {
  let buf = new Uint8Array(0)
  const reader = body.getReader()
  try {
    for (;;) {
      while (buf.length >= 5) {
        const n = new DataView(buf.buffer, buf.byteOffset).getUint32(1)
        if (n > 64 << 20) throw new Error("a malformed stream")
        if (buf.length < 5 + n) break
        const flags = buf[0]
        let data = buf.slice(5, 5 + n)
        buf = buf.slice(5 + n)
        if (flags & 1) data = new Uint8Array(gunzipSync(data))
        yield { end: !!(flags & 2), data }
      }
      const { value, done } = await reader.read()
      if (done) {
        if (buf.length) throw new Error("unexpected EOF")
        return
      }
      const next = new Uint8Array(buf.length + value.length)
      next.set(buf)
      next.set(value, buf.length)
      buf = next
    }
  } finally {
    reader.releaseLock()
  }
}

// ---- the request ----------------------------------------------------------------------

const USER = 1
const ASSISTANT = 2
const TOOL = 4
// answers a call the conversation never answered
const NO_RESULT = "Tool use was interrupted and did not produce a result."

const textOf = (c) =>
  typeof c === "string" ? c : Array.isArray(c) ? c.filter((p) => p?.type === "text").map((p) => p.text ?? "").join("") : ""

// joinSplitCalls joins an assistant message onto the assistant message
// before it when that one made tool calls: Anthropic takes consecutive
// assistant messages as one turn, and an agent can send a turn's parallel
// calls split over two of them, answered together after (yetone/magpie#1275).
// Answered at the second message, the first calls would be marked
// interrupted and their real results dropped. msgs is left as it is.
function joinSplitCalls(msgs) {
  const out = []
  for (const m of msgs) {
    const prev = out.at(-1)
    if (m?.role === "assistant" && prev?.role === "assistant" && prev.tool_calls?.length) {
      const content = [textOf(prev.content), textOf(m.content)].filter(Boolean).join("\n\n")
      out[out.length - 1] = { ...prev, content, tool_calls: [...prev.tool_calls, ...(m.tool_calls ?? [])] }
      continue
    }
    out.push(m)
  }
  return out
}

// effortOf is the effort a request names, as magpie reads it.
function effortOf(s) {
  const e = String(s ?? "").trim().toLowerCase()
  // none and minimal go as given: the family's variant nearest them, as
  // magpie's built-in picks it (devinVariantIn)
  if (["none", "minimal", "low", "medium", "high", "xhigh", "max"].includes(e)) return e
  if (e === "ultra") return "max"
  return ""
}

// argsOf is a call's arguments as JSON.
function argsOf(s) {
  if (s && typeof s === "object") return JSON.stringify(s)
  try {
    const v = JSON.parse(s || "{}")
    return JSON.stringify(v && typeof v === "object" ? v : {})
  } catch {
    return "{}"
  }
}

// toolDescriptions is what each tool says of itself, for the
// instructions.
function toolDescriptions(tools) {
  const said = tools.filter((t) => t.description)
  if (!said.length) return ""
  return "<tool_descriptions>\n" + said.map((t) => `<tool name="${t.name}">\n${t.description.trim()}\n</tool>\n`).join("") + "</tool_descriptions>"
}

const joinNonEmpty = (...s) => s.filter(Boolean).join("\n\n")

// objectRoot is a tool's parameters as a plain object at the root, with no
// anyOf, oneOf or allOf there: Devin's Claude models turn away a request
// offering a tool whose schema has one ("There is an issue with this
// request, please try a different model", magpie#1196; Codex desktop's
// codex_app automation_update has a root oneOf), as Anthropic does behind
// Factory (magpie#646). It folds the schema as magpie's built-in does
// (provider.ObjectRoot): allOf's branches are all merged, properties and
// required alike; of anyOf's and oneOf's object branches the properties are
// merged, a field each of them requires stays required, and the other
// branches go; a branch that is a union itself is folded first, so its
// fields are kept (magpie#1271). Branches may be local $refs. A schema already a plain object
// is returned as it came; the caller's own is never changed.
function objectRoot(ps) {
  return foldRoot(ps, ps, 0)
}

function foldRoot(root, ps, depth) {
  if (!isObj(ps)) return ps
  const has = (k) => Object.hasOwn(ps, k)
  if (ps.type === "object" && !has("anyOf") && !has("oneOf") && !has("allOf")) return ps
  const out = { ...ps }
  const props = { ...(isObj(ps.properties) ? ps.properties : {}) }
  let required = Array.isArray(ps.required) ? [...ps.required] : []
  const ref = (b) => {
    if (!isObj(b)) return null
    if (typeof b.$ref !== "string" || !b.$ref) return b
    for (const k of ["$defs", "definitions"]) {
      const pre = "#/" + k + "/"
      if (b.$ref.startsWith(pre)) {
        const d = root[k]?.[b.$ref.slice(pre.length)]
        return isObj(d) ? d : null
      }
    }
    return null
  }
  for (const b of Array.isArray(ps.allOf) ? ps.allOf : []) {
    const bm = ref(b)
    if (!bm) continue
    if (isObj(bm.properties)) for (const [k, v] of Object.entries(bm.properties)) if (!Object.hasOwn(props, k)) props[k] = v
    if (Array.isArray(bm.required)) required.push(...bm.required)
  }
  delete out.allOf
  const branches = []
  for (const k of ["anyOf", "oneOf"]) {
    for (const b of Array.isArray(ps[k]) ? ps[k] : []) {
      let bm = ref(b)
      if (!bm) continue
      // a branch that is itself a union (zod's discriminated create and
      // update in automation_update) is folded first, not dropped
      if (depth < 8 && ["anyOf", "oneOf", "allOf"].some((u) => Object.hasOwn(bm, u))) bm = foldRoot(root, bm, depth + 1)
      if (bm.type !== "object" && !Object.hasOwn(bm, "properties")) continue
      branches.push(bm)
    }
    delete out[k]
  }
  // a field every branch requires stays required, beside the root's and
  // allOf's own, which no branch can drop; a field the root doesn't define
  // takes each schema the branches give it, as anyOf when they differ
  let common = null
  const from = new Map()
  for (const b of branches) {
    for (const [k, v] of Object.entries(isObj(b.properties) ? b.properties : {})) {
      if (Object.hasOwn(props, k)) continue
      const vs = from.get(k) ?? []
      if (!vs.some((w) => JSON.stringify(w) === JSON.stringify(v))) vs.push(v)
      from.set(k, vs)
    }
    const br = Array.isArray(b.required) ? b.required : []
    common = common === null ? [...br] : common.filter((r) => br.includes(r))
  }
  for (const [k, vs] of from) props[k] = vs.length === 1 ? vs[0] : { anyOf: vs }
  for (const r of common ?? []) if (!required.includes(r)) required.push(r)
  required = [...new Set(required)]
  out.type = "object"
  out.properties = props
  if (required.length) out.required = required
  else delete out.required
  return out
}

const isObj = (v) => !!v && typeof v === "object" && !Array.isArray(v)

// geminiSchema is a tool's parameters as Devin's Gemini models take them.
// Devin answers "There is an issue with this request, please try a
// different model" (invalid_argument) to a Gemini model offered a tool
// whose schema, at any depth, has a type that is a list (["string",
// "null"], as zod-to-json-schema writes a nullable field), an array with
// no items, items that are a list or true, or properties or an enum with no
// type (7eleven on Discord, ZCode on gemini-4-argon). Its Claude models
// and swe-2 take all of them, so only a Gemini uid gets this. A list type
// becomes its one type, nullable when it had "null", or anyOf of each;
// such an array's items become {} (a one-schema list, that schema); a
// schema with properties is an object; an enum's type comes from its
// values, and an enum of mixed values goes. The caller's own is never
// changed.
function geminiSchema(s, depth = 0) {
  if (!isObj(s) || depth > 32) return s
  const out = { ...s }
  const sub = (v) => geminiSchema(v, depth + 1)
  for (const k of ["properties", "patternProperties", "$defs", "definitions", "dependentSchemas"])
    if (isObj(out[k])) out[k] = Object.fromEntries(Object.entries(out[k]).map(([n, v]) => [n, sub(v)]))
  for (const k of ["anyOf", "oneOf", "allOf", "prefixItems"]) if (Array.isArray(out[k])) out[k] = out[k].map(sub)
  for (const k of ["additionalProperties", "not", "if", "then", "else", "contains"]) if (isObj(out[k])) out[k] = sub(out[k])
  if (Array.isArray(out.type)) {
    const types = [...new Set(out.type.filter((t) => typeof t === "string"))]
    const some = types.filter((t) => t !== "null")
    if (some.length < types.length && some.length) out.nullable = true
    if (some.length === 1) out.type = some[0]
    else if (!some.length) out.type = types.length ? "null" : undefined
    else {
      delete out.type
      const branches = some.map((t) => ({ type: t, ...(t === "array" ? { items: {} } : {}) }))
      out.anyOf = Array.isArray(out.anyOf) ? [...out.anyOf, ...branches] : branches
    }
    if (out.type === undefined) delete out.type
  }
  if (out.type === undefined && Array.isArray(out.enum)) {
    const vals = out.enum.filter((v) => v !== null)
    if (vals.length < out.enum.length) out.nullable = true
    const kinds = new Set(vals.map((v) => (Number.isInteger(v) ? "integer" : typeof v)))
    const one = kinds.size === 1 ? [...kinds][0] : kinds.size === 2 && kinds.has("integer") && kinds.has("number") ? "number" : ""
    if (["string", "integer", "number", "boolean"].includes(one)) {
      out.type = one
      out.enum = vals
    } else delete out.enum
  }
  if (out.type === undefined && isObj(out.properties)) out.type = "object"
  if (out.type === "array" || (out.type === undefined && Object.hasOwn(out, "items"))) {
    if (Array.isArray(out.items)) out.items = out.items.length === 1 && isObj(out.items[0]) ? sub(out.items[0]) : {}
    else out.items = isObj(out.items) ? sub(out.items) : {}
    out.type = "array"
  }
  return out
}

const osName = () => (process.platform === "win32" ? "windows" : process.platform)

// metadata is what each request says of the client: the CLI's own (with
// its ide and extension names alone Devin lists one model).
const metadata = (key) =>
  new PB().str(1, "devin-cli").str(2, CLI_VERSION).str(3, key).str(4, "en").str(5, osName()).str(7, CLI_VERSION).str(12, "chisel").str(28, "chisel")

// Devin names a call after its tool and turn (Bash:0#a65b6a5e…), and
// Claude Code takes a tool_use id only of [A-Za-z0-9_-]: it drops a call
// whose id has the ':' or '#', and the turn fails "could not be parsed"
// (yetone/magpie#1304). Such an id goes out as "dv_" and its base64url, and
// comes back to Devin as it was, so its result still answers its call. An
// id that is already safe goes out as it came, unless it begins "dv_".
const SAFE_ID = /^[A-Za-z0-9_-]+$/
const outID = (id) => (SAFE_ID.test(id) && !id.startsWith("dv_") ? id : "dv_" + Buffer.from(id).toString("base64url"))
const inID = (id) => {
  if (typeof id !== "string" || !id.startsWith("dv_") || !SAFE_ID.test(id)) return id
  const raw = Buffer.from(id.slice(3), "base64url").toString()
  return raw && outID(raw) === id ? raw : id
}

// formatText is the structured output a chat's response_format asks for,
// said in words as magpie's built-in says it (format.go's instruction):
// json_schema's schema, or any JSON object for json_object or a schema of
// no shape; "" for text or none.
function formatText(f) {
  if (f?.type === "json_object") return "Respond with a single JSON object and nothing else."
  if (f?.type !== "json_schema") return ""
  const schema = f.json_schema ? f.json_schema.schema : f.schema
  if (schema === undefined || schema === null) return "Respond with a single JSON object and nothing else."
  return "Respond with a single JSON value and nothing else, matching this JSON schema:\n" + JSON.stringify(schema) + "."
}

// inlineImage is an image_url part as Devin takes an image, its bytes and
// type, or null for one that carries no bytes: Devin takes no URL, and
// magpie's built-in sends only an image with its bytes (inlineImages).
function inlineImage(p) {
  if (p?.type !== "image_url") return null
  const u = typeof p.image_url === "string" ? p.image_url : p.image_url?.url ?? ""
  const d = /^data:([^,]*),(.*)$/s.exec(u)
  return d && d[2] ? { data: d[2], type: d[1].replace(/;base64$/, "") || "image/png" } : null
}

// A chat's tool message holds text alone, so magpie's gateway (buildChat's
// seeLater) sends the images a tool returned in the user message after the
// results: each result's text ends saying how many follow (NOTE), and its
// images are headed FROM "<tool> (tool call <id>):]" or FROM
// "tool call <id>:]", ahead of the user's own words and images.
const FROM = "[From the result of "
const NOTE = /(?:\n\n)?\[The tool returned (an image; it|(\d+) images; they) follows? in the next message\.\]$/

// noted is how many images a result's text says follow it, and its text
// without saying so.
function noted(text) {
  const m = NOTE.exec(text)
  if (!m) return { n: 0, text }
  return { n: m[2] ? Number(m[2]) : 1, text: text.slice(0, m.index) }
}

// build is the GetChatMessage request for a chat completion, to the model
// uid.
function build(chat, uid, key) {
  const msgs = []
  let pending = [] // calls the last reply made, not yet answered
  const answer = () => {
    for (const c of pending) msgs.push({ role: TOOL, callID: c.id, text: NO_RESULT })
    pending = []
  }
  const system = []
  // the results just sent, by the call id the chat gave each, for the
  // images that follow them in the user message after
  const results = new Map()
  const resultFor = (text) => {
    if (typeof text !== "string" || !text.startsWith(FROM)) return null
    for (const [id, r] of results) if (r.said.n > r.seen && (text === `${FROM}tool call ${id}:]` || text.endsWith(` (tool call ${id}):]`))) return r
    return null
  }
  // settleResults gives each result just sent its text: without the note
  // that its images follow when every one went back with it, and "(no
  // output)" for none, as the built-in words it
  const settleResults = () => {
    for (const r of results.values()) {
      if (r.said.n > 0 && r.seen === r.said.n) r.msg.text = r.said.text
      r.msg.text ||= "(no output)"
    }
    results.clear()
  }
  for (const m of joinSplitCalls(chat.messages ?? [])) {
    if (m.role === "system" || m.role === "developer") {
      system.push(textOf(m.content))
      continue
    }
    if (m.role !== "tool" && m.role !== "user") settleResults()
    if (m.role === "assistant") {
      const calls = (m.tool_calls ?? []).map((c) => ({ id: inID(c.id), name: c.function?.name, args: argsOf(c.function?.arguments) }))
      const text = typeof m.content === "string" ? m.content : Array.isArray(m.content) ? m.content.filter((p) => p?.type === "text" && p.text).map((p) => p.text).join("\n\n") : ""
      if (!text && !calls.length) continue // a turn that only thought, or failed
      answer()
      msgs.push({ role: ASSISTANT, text, calls })
      // a copy: answering a call takes it off pending, never off the
      // reply that made it, which goes to Devin with every call it made
      pending = [...calls]
      continue
    }
    if (m.role === "tool") {
      // only a call just made is answered, and only once
      const i = pending.findIndex((c) => c.id === inID(m.tool_call_id))
      if (i >= 0) {
        const images = Array.isArray(m.content) ? m.content.map(inlineImage).filter(Boolean) : []
        const msg = { role: TOOL, callID: pending[i].id, text: textOf(m.content), images }
        msgs.push(msg)
        results.set(m.tool_call_id, { msg, said: noted(msg.text), seen: 0 })
        pending.splice(i, 1)
      }
      continue
    }
    if (m.role !== "user") continue
    const texts = []
    const images = []
    if (typeof m.content === "string") {
      if (m.content) texts.push(m.content)
    } else {
      // the images a tool returned, which a chat's tool message can't
      // hold, go back with the result that returned them, as magpie's
      // built-in sends them (inlineImages): a tool message's images
      // (field 10) and not a user message of their own
      let to = null // the result the images that follow are of, until it has the number it said
      for (const p of m.content ?? []) {
        if (p?.type === "text") {
          to = results.size ? resultFor(p.text) : null
          if (!to && p.text) texts.push(p.text)
        } else if (p?.type === "image_url") {
          const im = inlineImage(p)
          if (to) {
            if (im) to.msg.images.push(im)
            if (++to.seen === to.said.n) to = null
          } else if (im) images.push(im)
        }
      }
    }
    settleResults()
    const text = texts.join("\n\n")
    if (!text && !images.length) continue
    answer()
    msgs.push({ role: USER, text, images })
  }
  settleResults()
  answer()
  // Devin has no field for the answer's format: it is told in words, at
  // the end of the instructions, as magpie's built-in tells it (inSystem)
  const format = formatText(chat.response_format)
  if (format) system.push(format)
  const last = msgs[msgs.length - 1]
  if (!last || (last.role === ASSISTANT && !last.calls.length)) msgs.push({ role: USER, text: "Please proceed with the task." })

  // the instructions go at the head of the first user message: Devin turns
  // away some agents' own in its system field. The tools' descriptions go
  // there too, each tool sent with only a pointer to its own: Devin
  // answers "an internal error occurred" to some agents' tools as they
  // describe themselves, and the same words in a message go through
  let tools = (chat.tools ?? [])
    .filter((t) => (!t?.type || t.type === "function") && t.function?.name)
    .map((t) => ({ name: t.function.name, description: t.function.description ?? "", schema: objectRoot(t.function.parameters) }))
  if (chat.tool_choice === "none") tools = []
  if (/gemini/i.test(uid)) tools = tools.map((t) => ({ ...t, schema: geminiSchema(t.schema) }))
  const instructions = joinNonEmpty(system.join("\n\n"), toolDescriptions(tools))
  if (instructions) {
    let at = msgs.findIndex((m) => m.role === USER)
    if (at < 0) {
      msgs.unshift({ role: USER, text: "" })
      at = 0
    }
    msgs[at].text = joinNonEmpty(instructions, msgs[at].text)
  }

  const out = new PB().bytes(1, metadata(key))
  for (const m of msgs) out.bytes(3, encodeMsg(m))
  out.varint(7, 5)
  const max = chat.max_completion_tokens || chat.max_tokens || 128000 // the server holds it to the model's own
  // Devin answers "an internal error occurred" to a temperature or top_p of
  // exactly 0 (on every model: its Claude ones and swe-2), where 1e-6 goes
  // through; a 0 is sent as the smallest step above it, as near greedy
  const above0 = (v) => (v === 0 ? 1e-6 : v)
  const temp = above0(typeof chat.temperature === "number" ? chat.temperature : 1)
  const topP = above0(typeof chat.top_p === "number" ? chat.top_p : 0.95)
  out.bytes(8, new PB().varint(1, 1).varint(2, max).varint(3, 400).double(5, temp).varint(7, 40).double(8, topP))

  // the caller's tools, and any the conversation used that it no longer
  // offers
  const offered = new Set()
  const tool = (name, desc, schema) => {
    if (!schema || typeof schema !== "object") schema = { type: "object", properties: {} }
    offered.add(name)
    out.bytes(10, new PB().str(1, name).str(2, desc || name).bytes(3, JSON.stringify(schema)))
  }
  for (const t of tools) tool(t.name, t.description ? `Described under <tool name="${t.name}"> in <tool_descriptions>, in the instructions.` : "", t.schema)
  for (const m of msgs) for (const c of m.calls ?? []) if (!offered.has(c.name)) tool(c.name, "Tool")
  return out.str(21, uid).done()
}

function encodeMsg(m) {
  const b = new PB().str(1, randomUUID()).varint(2, m.role)
  if (m.text) b.str(3, m.text)
  for (const c of m.calls ?? []) b.bytes(6, new PB().str(1, c.id ?? "").str(2, c.name ?? "").bytes(3, c.args))
  if (m.callID) b.str(7, m.callID)
  for (const p of m.images ?? []) b.bytes(10, new PB().str(1, p.data).str(2, p.type))
  return b.done()
}

// ---- the reply -----------------------------------------------------------------------

// statusText is a status as magpie's built-in words an error that says
// nothing (Go's http.StatusText).
const statusText = (s) =>
  ({ 413: "Request Entity Too Large", 414: "Request URI Too Long", 416: "Requested Range Not Satisfiable", 418: "I'm a teapot", 509: "" })[s] ?? STATUS_CODES[s] ?? ""

// failure is the status and message for a Connect error: {"code",
// "message"}, or at a stream's end {"error": {"code", "message"}}.
function failure(status, text) {
  let msg = String(text ?? "").trim()
  let code = ""
  try {
    const e = JSON.parse(text)
    if (e?.error && typeof e.error === "object") [code, msg] = [e.error.code ?? "", e.error.message ?? ""]
    else if (e?.code) [code, msg] = [e.code, e.message ?? ""]
    msg ||= code
  } catch {}
  msg ||= statusText(status) || `HTTP ${status}`
  const low = msg.toLowerCase()
  if (code === "unauthenticated") return { status: 401, message: msg + " — sign in to Devin again" }
  if (code === "resource_exhausted" || low.includes("quota") || low.includes("rate limit")) return { status: 429, message: "usage limit reached: " + msg }
  // Devin turns away some system prompts outright; sent again, the same
  // one is turned away again
  if (low.includes("content policy")) return { status: 400, message: msg }
  if (low.includes("too long") || low.includes("context length") || low.includes("too many tokens"))
    return { status: 400, message: "input is too long for the model's context: " + msg }
  if (code === "invalid_argument") return { status: 400, message: msg }
  if (code === "unavailable") return { status: 503, message: msg }
  if (code) return { status: 502, message: msg }
  return { status, message: msg }
}

// events turns the reply's frames into chat completion pieces: text,
// reasoning, each tool call opened with its id and name, its arguments in
// pieces after, then the finish with the usage.
async function* events(it) {
  let tools = -1
  let stop = 0
  let said = 0
  const usage = { input: 0, output: 0, cacheWrite: 0, cacheRead: 0 }
  for (;;) {
    let r
    try {
      r = await it.next()
    } catch (e) {
      yield { error: { status: 502, message: "the reply broke off: " + e.message } }
      return
    }
    if (r.done) break
    const f = r.value
    if (f.end) {
      const e = failure(200, dec.decode(f.data))
      if (e.status >= 300) {
        yield { error: e }
        return
      }
      break
    }
    for (const x of fields(f.data)) {
      if (x.num === 3 && x.wire === 2 && x.data.length) {
        said += x.data.length
        yield { text: dec.decode(x.data) }
      } else if (x.num === 9 && x.wire === 2 && x.data.length) {
        said += x.data.length
        yield { reasoning: dec.decode(x.data) }
      } else if (x.num === 6 && x.wire === 2) {
        let id = ""
        let name = ""
        let args = ""
        for (const g of fields(x.data)) {
          if (g.num === 1) id = dec.decode(g.data ?? new Uint8Array())
          else if (g.num === 2) name = dec.decode(g.data ?? new Uint8Array())
          else if (g.num === 3) args += dec.decode(g.data ?? new Uint8Array())
        }
        if (name) {
          tools++
          yield { tool: { index: tools, id: id ? outID(id) : "call_" + randomBytes(12).toString("hex"), name } }
        }
        if (args && tools >= 0) yield { args: { index: tools, text: args } }
      } else if (x.num === 5 && x.wire === 0) stop = x.n
      else if (x.num === 7 && x.wire === 2) {
        for (const g of fields(x.data)) {
          if (g.wire !== 0 || !g.n) continue
          if (g.num === 2) usage.input = g.n
          else if (g.num === 3) usage.output = g.n
          else if (g.num === 4) usage.cacheWrite = g.n
          else if (g.num === 5) usage.cacheRead = g.n
        }
      }
    }
  }
  if (!said && tools < 0) {
    yield { error: { status: 502, message: "an empty reply (Devin returned no text, reasoning or tool call)" } }
    return
  }
  if (!usage.output) usage.output = Math.floor((said + 3) / 4)
  const prompt = usage.input + usage.cacheRead + usage.cacheWrite
  yield {
    stop: tools >= 0 || stop === 10 ? "tool_calls" : stop === 3 ? "length" : "stop",
    usage: {
      prompt_tokens: prompt,
      completion_tokens: usage.output,
      total_tokens: prompt + usage.output,
      prompt_tokens_details: { cached_tokens: usage.cacheRead, cache_write_tokens: usage.cacheWrite },
    },
  }
}

// errorResponse is a failure as the built-in answered it. None marks the
// account lapsed: the built-in's 401s (a key Devin turned away, none to
// send) were answered without touching the account, so each says
// X-Magpie-Sign-In: kept — as a success does, which cleared nothing either.
const errorResponse = ({ status, message }) =>
  new Response(JSON.stringify({ error: { message, type: "devin_error", code: status } }), {
    status,
    headers: { "Content-Type": "application/json", "X-Magpie-Sign-In": "kept" },
  })

async function* prepend(first, rest) {
  yield first
  yield* rest
}

// complete answers a chat completion through GetChatMessage.
async function complete({ key, server, families }, chat, signal) {
  const uid = variantFor(families, chat.model, effortOf(chat.reasoning_effort))
  const body = frame(build(chat, uid, key))
  let res
  try {
    res = await fetch(server + CHAT_RPC, {
      method: "POST",
      headers: { "Content-Type": "application/connect+proto", "Connect-Protocol-Version": "1", ...(body[0] & 1 ? { "Connect-Content-Encoding": "gzip" } : {}), Authorization: "Basic " + key },
      body,
      signal,
    })
  } catch (e) {
    return errorResponse({ status: 502, message: e.message })
  }
  if (!res.ok) {
    const e = failure(res.status, (await res.text()).slice(0, 1 << 20))
    return errorResponse(e)
  }
  // an error comes as the stream's end, before anything else: read that
  // far so it is answered with its own status
  const fr = frames(res.body)
  let first
  try {
    first = await fr.next()
  } catch (e) {
    return errorResponse({ status: 502, message: "the reply broke off: " + e.message })
  }
  if (first.done) return errorResponse({ status: 502, message: "the reply broke off: EOF" })
  if (first.value.end) {
    const e = failure(200, dec.decode(first.value.data))
    return errorResponse(e.status < 300 ? { status: 502, message: "an empty reply" } : e)
  }
  // 先等到实际内容或错误，才能让网关在空回复时切换渠道。
  const source = events(prepend(first.value, fr))
  const head = await source.next()
  if (head.done) return errorResponse({ status: 502, message: "an empty reply" })
  if (head.value.error) {
    await fr.return?.()
    return errorResponse(head.value.error)
  }
  const it = prepend(head.value, source)
  const id = "chatcmpl-" + randomBytes(12).toString("hex")
  const created = Math.floor(Date.now() / 1000)

  if (!chat.stream) {
    const msg = { role: "assistant", content: "" }
    let reasoning = ""
    const calls = []
    let stop = "stop"
    let usage
    for await (const e of it) {
      if (e.error) return errorResponse(e.error)
      if (e.text) msg.content += e.text
      if (e.reasoning) reasoning += e.reasoning
      if (e.tool) calls.push({ id: e.tool.id, type: "function", function: { name: e.tool.name, arguments: "" } })
      if (e.args) calls[e.args.index].function.arguments += e.args.text
      if (e.stop) [stop, usage] = [e.stop, e.usage]
    }
    if (reasoning) msg.reasoning_content = reasoning
    if (calls.length) msg.tool_calls = calls
    return Response.json({ id, object: "chat.completion", created, model: chat.model, choices: [{ index: 0, message: msg, finish_reason: stop }], usage }, { headers: { "X-Magpie-Sign-In": "kept" } })
  }

  const chunk = (delta, finish_reason = null, extra = {}) =>
    enc.encode(`data: ${JSON.stringify({ id, object: "chat.completion.chunk", created, model: chat.model, choices: [{ index: 0, delta, finish_reason }], ...extra })}\n\n`)
  let began = false
  const stream = new ReadableStream({
    async pull(ctl) {
      if (!began) {
        began = true
        ctl.enqueue(chunk({ role: "assistant", content: "" }))
      }
      const r = await it.next()
      if (r.done) {
        ctl.enqueue(enc.encode("data: [DONE]\n\n"))
        return ctl.close()
      }
      const e = r.value
      if (e.text) ctl.enqueue(chunk({ content: e.text }))
      else if (e.reasoning) ctl.enqueue(chunk({ reasoning_content: e.reasoning }))
      else if (e.tool) ctl.enqueue(chunk({ tool_calls: [{ index: e.tool.index, id: e.tool.id, type: "function", function: { name: e.tool.name, arguments: "" } }] }))
      else if (e.args) ctl.enqueue(chunk({ tool_calls: [{ index: e.args.index, function: { arguments: e.args.text } }] }))
      else if (e.stop) ctl.enqueue(chunk({}, e.stop, { usage: e.usage }))
      else if (e.error) {
        ctl.enqueue(enc.encode(`data: ${JSON.stringify({ error: { message: e.error.message, code: e.error.status } })}\n\n`))
        ctl.close()
      }
    },
    cancel() {
      it.return?.()
    },
  })
  return new Response(stream, { status: 200, headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", "X-Magpie-Sign-In": "kept" } })
}

// ---- the plugin ---------------------------------------------------------------------

// ---- usage -----------------------------------------------------------------

// userStatus asks GetUserStatus about the key's account.
function userStatus(key, server) {
  return fetch(server + "/exa.seat_management_pb.SeatManagementService/GetUserStatus", {
    method: "POST",
    headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" },
    body: JSON.stringify({
      metadata: { ideName: "devin-cli", ideVersion: CLI_VERSION, extensionName: "devin-cli", extensionVersion: CLI_VERSION, apiKey: key, locale: "en", os: osName() },
    }),
    signal: AbortSignal.timeout(15_000),
  })
}

// usage is the account's plan and how much of its quota is gone, as
// GetUserStatus (what the CLI's /usage reads) tells them: the plan's name
// and end, the daily and weekly quotas a quota-billed plan has (the share
// left of each, and when it comes back), the ACUs a plan with a limit has
// used this cycle, and the extra usage balance. JSON leaves out what is
// zero, so a quota-billed plan's quota with a reset and no share is used
// up. magpie's
// built-in Devin account showed none of this. The day and the week don't
// count the models Devin says are free (notModels): with them used up,
// SWE-2 still answers, and magpie sends it to the account.
async function usage(key, server, onStatus) {
  const res = await userStatus(key, server)
  const text = await res.text()
  if (res.status === 401 || failure(res.status, text).status === 401)
    return { error: "Devin's sign-in has expired — sign in again", windows: [] }
  if (!res.ok) return { error: failure(res.status, text).message, windows: [] }
  const u = JSON.parse(text || "{}")?.userStatus ?? {}
  await onStatus?.(u)
  const st = u.planStatus ?? {}
  const info = st.planInfo ?? {}
  const num = (v) => (typeof v === "number" ? v : typeof v === "string" && v.trim() && !isNaN(Number(v)) ? Number(v) : 0)
  // no plan: the row keeps the one `devin auth status` gave the sign-in
  // ("Devin Pro"), as magpie's built-in row said it, not planName's "Pro"
  const out = { windows: [] }
  if (st.planEnd) out.until = st.planEnd
  // a quota's window is shown when Devin says what is left of it. JSON
  // leaves a 0 out, so on a quota-billed plan a window with a reset and no
  // share left is used up: a week at 0% left came as no weeklyQuota-
  // RemainingPercent beside the day's 100, and the day alone was shown
  // (面条 on magpie's Discord). Billed otherwise, a missing share is no quota.
  const quota = info.billingStrategy === "BILLING_STRATEGY_QUOTA"
  for (const [name, span, left, reset, hide] of [
    ["1 day", 24 * 3600, st.dailyQuotaRemainingPercent, st.dailyQuotaResetAtUnix, info.hideDailyQuota],
    ["7 days", 7 * 24 * 3600, st.weeklyQuotaRemainingPercent, st.weeklyQuotaResetAtUnix, info.hideWeeklyQuota],
  ]) {
    const at = num(reset)
    if (hide || ((left === undefined || left === null) && !(quota && at > 0))) continue
    out.windows.push({ name, used: Math.min(100, Math.max(0, 100 - num(left))), span, ...(at > 0 ? { resetsAt: at } : {}) })
  }
  if (out.windows.length) {
    const free = await freeIDs(key, server)
    if (free.length) for (const w of out.windows) w.notModels = free
  }
  const limit = num(st.acuLimit)
  if (limit > 0) {
    const used = num(st.acuConsumed)
    out.windows.push({ name: "ACUs", used: Math.min(100, (100 * used) / limit), display: `${+used.toFixed(2)} / ${+limit.toFixed(2)} ACUs`, ...(st.planEnd ? { resetsAt: st.planEnd } : {}), aside: true })
  }
  const extra = num(st.overageBalanceMicros)
  if (extra > 0) out.balance = "$" + (extra / 1e6).toFixed(2)
  return out
}

// freeIDs are the ids of key's account's free models (parseFree) as magpie
// may ask for them: each variant's own, and each entry of the list that
// stands for free variants alone (swe-2 for swe-2-high, -medium and -max).
// None when Devin hasn't said.
async function freeIDs(key, server) {
  const c = await configsFor(key, server)
  if (!c.free?.size) return []
  const ids = new Set(c.free)
  for (const m of listed(c.families ?? [], [], {}, c.free)) if (m.free) ids.add(m.id)
  return [...ids]
}

// serverOf is the API server an account's requests go to:
// WINDSURF_API_SERVER_URL moves it, as it does the CLI's.
const serverOf = (auth) => (process.env.WINDSURF_API_SERVER_URL || auth?.metadata?.server || SERVER).replace(/\/+$/, "")

// live is the key and server an account has now: one taken from the CLI's
// sign-in reads credentials.toml again, as a `devin auth login` since may
// have changed it, and keeps the ones it was saved with while the CLI has
// none.
async function live(auth) {
  if (auth?.metadata?.cli) {
    try {
      const c = readCredentials(await readFile(cliCredentialsPath(), "utf8"))
      if (c.windsurf_api_key)
        return { key: c.windsurf_api_key, server: (process.env.WINDSURF_API_SERVER_URL || c.api_server_url || SERVER).replace(/\/+$/, "") }
    } catch {}
  }
  return { key: auth?.key, server: serverOf(auth) }
}

export async function DevinAuthPlugin({ client } = {}) {
  return {
    auth: {
      provider: ID,
      async loader(getAuth) {
        const auth = await getAuth()
        if (auth?.type !== "api" || !auth.key) return {}
        return {
          baseURL: BASE,
          apiKey: auth.key,
          async fetch(input, init = {}) {
            const now = await getAuth()
            if (now?.type !== "api" || !now.key) return errorResponse({ status: 401, message: "Devin isn't signed in" })
            const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
            if (!/\/chat\/completions$/.test(new URL(url).pathname)) return errorResponse({ status: 404, message: "only chat completions are served" })
            let chat
            try {
              const b = init.body ?? (input instanceof Request ? await input.clone().text() : undefined)
              chat = JSON.parse(typeof b === "string" ? b : dec.decode(b))
            } catch {
              return errorResponse({ status: 400, message: "a request that isn't JSON" })
            }
            const { key, server } = await live(now)
            const families = await familiesFor(key, server)
            return complete({ key, server, families }, chat, init.signal)
          },
        }
      },
      methods: [
        { type: "oauth", label: "Devin (browser)", authorize: browserSignIn },
        { type: "oauth", label: "Devin CLI's sign-in", authorize: cliSignIn },
      ],
      // magpie's: the plan and how much of its quota is gone. The built-in
      // read none, so no read marks the account or clears it, not even a
      // key Devin turned away.
      async usage(getAuth) {
        const auth = await getAuth()
        if (auth?.type !== "api" || !auth.key) return { error: "Devin isn't signed in", signIn: "kept" }
        const { key, server } = await live(auth)
        const syncOrg = async (u) => {
          if (!client?.auth?.set) return
          const who = whoOfStatus(u)
          if (!who) return
          const md = { ...(auth.metadata ?? {}) }
          let changed = key !== auth.key
          if (who.user && md.email !== who.user) {
            md.email = who.user
            changed = true
          }
          const orgId = who.orgId || who.teamId
          if (orgId && md.orgId !== orgId) {
            md.orgId = orgId
            changed = true
          }
          if (who.teamId && md.teamId !== who.teamId) {
            md.teamId = who.teamId
            changed = true
          }
          if (changed) {
            try {
              await client.auth.set({ path: { id: ID }, body: { ...auth, key, metadata: md } })
            } catch {}
          }
        }
        return { ...(await usage(key, server, syncOrg)), signIn: "kept" }
      },
    },
    async config(config) {
      config.provider ??= {}
      const was = config.provider[ID] ?? {}
      config.provider[ID] = {
        name: "Devin",
        npm: CHAT,
        api: BASE,
        ...was,
        models: { ...Object.fromEntries(listed(familiesOf(SNAPSHOT), [], SNAPSHOT_IMAGES).map((m) => [m.id, configModel(m)])), ...(was.models ?? {}) },
      }
    },
    // the account's list, as Devin or the devin CLI gives it (familiesFor),
    // with what Devin says of each model's images; with neither, the list
    // it was given with that
    provider: {
      id: ID,
      async models(provider, { auth } = {}) {
        if (auth?.type !== "api" || !auth.key) return provider.models
        const { key, server } = await live(auth)
        const images = await imagesFor(key, server)
        const { free, prices } = await configsFor(key, server)
        const families = await ownFamilies(key, server)
        if (!families) return withImages(provider.models, listed(familiesOf(SNAPSHOT), Object.keys(provider?.models ?? {}), images))
        return Object.fromEntries(listed(families, Object.keys(provider?.models ?? {}), images, free, prices).map((m) => [m.id, runtimeModel(m)]))
      },
    },
  }
}

// for tests
export const _internal = { parseFree, parsePrices, geminiSchema, freeIDs, outID, inID, objectRoot, seesImages, imagesFor, forgetSaid: () => said.clear(), parseModelConfigs, parseConfigFamilies, SNAPSHOT_IMAGES, withImages, forgetImages: () => (seen = null), effortOf, runtimeModel, configModel, live, build, failure, complete, parseFamilies, listed, variantFor, familiesOf, SNAPSHOT, parseStatus, readsHome, tierName, whoOfStatus, whoByKey, success, familiesFor, readCredentials, credentials, fields, frame, PB, events, frames }

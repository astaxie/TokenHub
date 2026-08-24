import {
  ArrowDown,
  Check,
  Code2,
  Copy,
  Download,
  ImagePlus,
  RotateCcw,
  Send,
  Sparkles,
  Square,
  Trash2,
  X,
} from "lucide-react";
import { type FormEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  type ApiContext,
  type ApiExampleLanguage,
  type AppData,
  type PlaygroundChatPayload,
  type PlaygroundRouteAttempt,
  type PlaygroundStreamEvent,
} from "../core/types";
import { modelCategory, modelCategoryLabel } from "../domain/catalog";
import { copyText } from "../domain/clipboard";
import {
  activeRouteCount,
  apiExampleLanguages,
  apiExampleScripts,
  extractAssistantText,
  formatBytes,
  playgroundModels,
  routeStrategyLabel,
  uniqueUIID,
} from "../domain/formatting";
import {
  type PlaygroundImageAttachment,
  type PlaygroundImageSelectionError,
  type PlaygroundRequestContent,
  modelSupportsPlaygroundImages,
  playgroundAttachmentBytes,
  playgroundImageMIMETypes,
  playgroundImagesForExport,
  playgroundMaxImagesPerMessage,
  playgroundMessageContent,
  validatePlaygroundImageSelection,
} from "../domain/playground-images";
import {
  clampPlaygroundMaxTokens,
  hasPlaygroundUsage,
  playgroundMaxTokenLimit,
  selectPlaygroundCandidateBranch,
} from "../domain/playground-logic";
import { playgroundMissingStreamBodyCode, streamPlaygroundChat } from "../resources/playground-stream";
import { activeLanguage, countWithUnit, defaultPlaygroundSystemPrompt, isDefaultPlaygroundSystemPrompt, languageLocale, tx } from "../i18n/runtime";
import { isAuthExpiredError } from "../resources/payloads";
import { DetailField } from "./audit";

type PlaygroundCandidateStatus = "streaming" | "completed" | "failed" | "cancelled";

type PlaygroundCandidate = {
  id: string;
  model: string;
  content: string;
  status: PlaygroundCandidateStatus;
  startedAt: string;
  localStartedMS: number;
  requestID?: string;
  mode?: "stream" | "buffered";
  liveTTFTMS?: number;
  result?: PlaygroundChatPayload;
  error?: string;
  blocked?: boolean;
};

type PlaygroundTurn = {
  id: string;
  userContent: string;
  userImages: PlaygroundImageAttachment[];
  userCreatedAt: string;
  candidates: PlaygroundCandidate[];
  selectedCandidateID: string;
};

type PlaygroundRequestMessage = { role: "system" | "user" | "assistant"; content: PlaygroundRequestContent };

const playgroundDateTimeOptions: Intl.DateTimeFormatOptions = {
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  timeZoneName: "short",
};

const playgroundClockOptions: Intl.DateTimeFormatOptions = {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
};

const playgroundDateTimeFormatters = new Map<string, Intl.DateTimeFormat>();
const playgroundClockFormatters = new Map<string, Intl.DateTimeFormat>();
const playgroundIntegerFormatters = new Map<string, Intl.NumberFormat>();
const playgroundDecimalFormatters = new Map<string, Intl.NumberFormat>();
const playgroundUSDFormatters = new Map<string, Intl.NumberFormat>();

function playgroundFormatter(cache: Map<string, Intl.DateTimeFormat>, options: Intl.DateTimeFormatOptions) {
  const locale = languageLocale();
  const cached = cache.get(locale);
  if (cached) return cached;
  const formatter = new Intl.DateTimeFormat(locale, options);
  cache.set(locale, formatter);
  return formatter;
}

function playgroundNumberFormatter(cache: Map<string, Intl.NumberFormat>, options: Intl.NumberFormatOptions) {
  const locale = languageLocale();
  const cached = cache.get(locale);
  if (cached) return cached;
  const formatter = new Intl.NumberFormat(locale, options);
  cache.set(locale, formatter);
  return formatter;
}

function formatPlaygroundInteger(value?: number) {
  return value === undefined || !Number.isFinite(value)
    ? "-"
    : playgroundNumberFormatter(playgroundIntegerFormatters, { maximumFractionDigits: 0 }).format(value);
}

function formatPlaygroundDecimal(value?: number) {
  return value === undefined || !Number.isFinite(value)
    ? "-"
    : playgroundNumberFormatter(playgroundDecimalFormatters, { minimumFractionDigits: 1, maximumFractionDigits: 1 }).format(value);
}

function formatPlaygroundUSD(value?: number) {
  return value === undefined || !Number.isFinite(value)
    ? "-"
    : playgroundNumberFormatter(playgroundUSDFormatters, {
      style: "currency",
      currency: "USD",
      currencyDisplay: "narrowSymbol",
      minimumFractionDigits: 6,
      maximumFractionDigits: 6,
    }).format(value);
}

function playgroundImageSelectionErrorText(error: PlaygroundImageSelectionError) {
  switch (error) {
    case "too_many_images": return tx("每条消息最多上传 4 张图片。");
    case "unsupported_type": return tx("仅支持 JPEG、PNG 和 WebP 图片。");
    case "image_too_large": return tx("单张图片不能超过 5 MiB。");
    case "conversation_too_large": return tx("当前会话图片总量不能超过 12 MiB，请删除图片或新建会话。");
  }
}

function selectedCandidate(turn: PlaygroundTurn) {
  return turn.candidates.find((candidate) => candidate.id === turn.selectedCandidateID) ?? turn.candidates.at(-1);
}

function requestMessagesForNewTurn(turns: PlaygroundTurn[], systemPrompt: string, userContent: string, userImages: PlaygroundImageAttachment[]) {
  const messages: PlaygroundRequestMessage[] = [];
  if (systemPrompt.trim()) messages.push({ role: "system", content: systemPrompt.trim() });
  for (const turn of turns) {
    messages.push({ role: "user", content: playgroundMessageContent(turn.userContent, turn.userImages) });
    const candidate = selectedCandidate(turn);
    if (candidate?.content) messages.push({ role: "assistant", content: candidate.content });
  }
  messages.push({ role: "user", content: playgroundMessageContent(userContent, userImages) });
  return messages;
}

function requestMessagesForRerun(turns: PlaygroundTurn[], turnIndex: number, systemPrompt: string) {
  const messages: PlaygroundRequestMessage[] = [];
  if (systemPrompt.trim()) messages.push({ role: "system", content: systemPrompt.trim() });
  turns.slice(0, turnIndex + 1).forEach((turn, index) => {
    messages.push({ role: "user", content: playgroundMessageContent(turn.userContent, turn.userImages) });
    if (index < turnIndex) {
      const candidate = selectedCandidate(turn);
      if (candidate?.content) messages.push({ role: "assistant", content: candidate.content });
    }
  });
  return messages;
}

function readPlaygroundImage(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => typeof reader.result === "string" ? resolve(reader.result) : reject(new Error("invalid_image_data"));
    reader.onerror = () => reject(reader.error ?? new Error("image_read_failed"));
    reader.readAsDataURL(file);
  });
}

function formatDuration(milliseconds?: number) {
  if (milliseconds === undefined || !Number.isFinite(milliseconds)) return "-";
  if (milliseconds < 1000) return `${formatPlaygroundInteger(Math.max(0, milliseconds))}ms`;
  return `${formatPlaygroundDecimal(milliseconds / 1000)}s`;
}

function formatRate(value?: number) {
  return value === undefined || !Number.isFinite(value) ? "-" : `${formatPlaygroundDecimal(value)} tok/s`;
}

function formatClock(value?: string) {
  if (!value) return "-";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "-" : playgroundFormatter(playgroundClockFormatters, playgroundClockOptions).format(parsed);
}

function formatDateTime(value?: string) {
  if (!value) return "-";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "-" : playgroundFormatter(playgroundDateTimeFormatters, playgroundDateTimeOptions).format(parsed);
}

function prettyJSON(value: unknown) {
  return JSON.stringify(value ?? {}, null, 2);
}

function numericParameter(value: string) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function routeHasDetails(payload?: PlaygroundChatPayload) {
  const route = payload?.route;
  return Boolean(route?.route_id || route?.provider_id || route?.resource_id || route?.provider_model);
}

export function PlaygroundPage({ api, data, canViewRoutes }: { api: ApiContext; data: AppData; canViewRoutes: boolean }) {
  return (
    <section className="playground-page">
      <PlaygroundPanel api={api} data={data} canViewRoutes={canViewRoutes} />
    </section>
  );
}

export function PlaygroundPanel({ api, data, canViewRoutes }: { api: ApiContext; data: AppData; canViewRoutes: boolean }) {
  const models = useMemo(() => {
    const candidates = playgroundModels(data, canViewRoutes);
    return canViewRoutes ? candidates.filter((model) => activeRouteCount(model.name, data) > 0) : candidates;
  }, [data, canViewRoutes]);
  const projects = useMemo(() => data.projects.filter((project) => !project.status || project.status === "active"), [data.projects]);
  const [modelName, setModelName] = useState(models[0]?.name ?? "");
  const [projectID, setProjectID] = useState(projects[0]?.id ?? "");
  const [pendingModelName, setPendingModelName] = useState("");
  const [turns, setTurns] = useState<PlaygroundTurn[]>([]);
  const [draft, setDraft] = useState("");
  const [images, commitImages] = useState<PlaygroundImageAttachment[]>([]);
  const [systemPrompt, setSystemPrompt] = useState(defaultPlaygroundSystemPrompt);
  const [responseFormat, setResponseFormat] = useState("text");
  const [maxTokens, setMaxTokens] = useState("4096");
  const [temperature, setTemperature] = useState("0.7");
  const [topP, setTopP] = useState("1.0");
  const [reasoningEffort, setReasoningEffort] = useState("");
  const [presencePenalty, setPresencePenalty] = useState("0.0");
  const [frequencyPenalty, setFrequencyPenalty] = useState("0.0");
  const [minP, setMinP] = useState("0.00");
  const [topK, setTopK] = useState("50");
  const [showCode, setShowCode] = useState(false);
  const [showModelDetails, setShowModelDetails] = useState(false);
  const [loading, setLoading] = useState(false);
  const [imageUploadPending, setImageUploadPending] = useState(false);
  const [error, setError] = useState("");
  const [nowMS, setNowMS] = useState(Date.now());
  const [showNewContent, setShowNewContent] = useState(false);
  const activeRequestRef = useRef<AbortController | null>(null);
  const chatRef = useRef<HTMLDivElement | null>(null);
  const imageInputRef = useRef<HTMLInputElement | null>(null);
  const imagesRef = useRef<PlaygroundImageAttachment[]>([]);
  const imageUploadPendingRef = useRef(false);
  const imageUploadGenerationRef = useRef(0);
  const conversationImageBytesRef = useRef(0);
  const isAtBottomRef = useRef(true);
  const selectedModel = models.find((model) => model.name === modelName);
  const supportsImages = modelSupportsPlaygroundImages(selectedModel);
  const conversationImageBytes = turns.reduce((total, turn) => total + playgroundAttachmentBytes(turn.userImages), 0);
  const hasConversationImages = conversationImageBytes > 0 || images.length > 0;
  const selectedModelRouteCount = canViewRoutes && selectedModel ? activeRouteCount(selectedModel.name, data) : 0;
  const contextWindow = selectedModel?.context_window ?? 0;
  const maxTokenLimit = playgroundMaxTokenLimit(selectedModel?.metadata?.max_output_tokens, contextWindow);
  const maxTokenMinimum = Math.min(128, maxTokenLimit);
  const maxTokenStep = maxTokenLimit < 128 ? 1 : 128;
  const inputPrice = selectedModel?.input_price_usd_per_1m ?? 0;
  const outputPrice = selectedModel?.output_price_usd_per_1m ?? 0;
  const supportedParameters = new Set(selectedModel?.supported_parameters ?? []);
  const supports = (parameter: string) => supportedParameters.has(parameter);
  const supportsReasoningEffort = supports("reasoning") || supports("reasoning_effort") || Boolean(selectedModel?.capabilities?.includes("reasoning"));
  const advancedParameterCount = ["top_p", "presence_penalty", "frequency_penalty", "min_p", "top_k"].filter((parameter) => supports(parameter)).length;
  const lastCandidate = turns.at(-1) ? selectedCandidate(turns.at(-1)!) : undefined;
  const lastResult = lastCandidate?.result;

  function updateImages(next: PlaygroundImageAttachment[] | ((current: PlaygroundImageAttachment[]) => PlaygroundImageAttachment[])) {
    const value = typeof next === "function" ? next(imagesRef.current) : next;
    imagesRef.current = value;
    commitImages(value);
  }

  const invalidatePendingImageUploads = useCallback(() => {
    imageUploadGenerationRef.current += 1;
    imageUploadPendingRef.current = false;
    setImageUploadPending(false);
  }, []);

  const changeModelName = useCallback((nextModelName: string) => {
    invalidatePendingImageUploads();
    setModelName(nextModelName);
  }, [invalidatePendingImageUploads]);

  useEffect(() => {
    if (!modelName && models[0]?.name) changeModelName(models[0].name);
    if (modelName && !models.some((model) => model.name === modelName)) changeModelName(models[0]?.name ?? "");
  }, [changeModelName, modelName, models]);

  useEffect(() => {
    if (!projectID && projects[0]?.id) setProjectID(projects[0].id);
    if (projectID && !projects.some((project) => project.id === projectID)) setProjectID(projects[0]?.id ?? "");
  }, [projectID, projects]);

  useEffect(() => {
    conversationImageBytesRef.current = conversationImageBytes;
  }, [conversationImageBytes]);

  useEffect(() => {
    if (!supportsImages) invalidatePendingImageUploads();
  }, [invalidatePendingImageUploads, supportsImages]);

  const languageVersion = activeLanguage;
  useEffect(() => {
    setSystemPrompt((current) => isDefaultPlaygroundSystemPrompt(current) ? defaultPlaygroundSystemPrompt() : current);
  }, [languageVersion]);

  useEffect(() => {
    const clamped = clampPlaygroundMaxTokens(numericParameter(maxTokens), maxTokenLimit);
    if (String(clamped) !== maxTokens) setMaxTokens(String(clamped));
  }, [maxTokenLimit, maxTokens]);

  useEffect(() => () => {
    activeRequestRef.current?.abort();
    imageUploadGenerationRef.current += 1;
    imageUploadPendingRef.current = false;
  }, []);

  useEffect(() => {
    if (!loading) return;
    const timer = window.setInterval(() => setNowMS(Date.now()), 100);
    return () => window.clearInterval(timer);
  }, [loading]);

  useEffect(() => {
    const element = chatRef.current;
    if (!element) return;
    window.requestAnimationFrame(() => {
      if (isAtBottomRef.current) {
        element.scrollTop = element.scrollHeight;
        setShowNewContent(false);
      } else {
        setShowNewContent(true);
      }
    });
  }, [turns]);

  function updateCandidate(turnID: string, candidateID: string, updater: (candidate: PlaygroundCandidate) => PlaygroundCandidate) {
    setTurns((current) => current.map((turn) => turn.id !== turnID ? turn : {
      ...turn,
      candidates: turn.candidates.map((candidate) => candidate.id === candidateID ? updater(candidate) : candidate),
    }));
  }

  function buildPayload(messages: PlaygroundRequestMessage[], requestModel: string) {
    const payload: Record<string, unknown> = {
      project_id: projectID,
      model: requestModel,
      messages,
      max_tokens: clampPlaygroundMaxTokens(numericParameter(maxTokens), maxTokenLimit),
    };
    if (supports("temperature")) payload.temperature = numericParameter(temperature);
    if (supports("top_p")) payload.top_p = numericParameter(topP);
    if (supports("presence_penalty")) payload.presence_penalty = numericParameter(presencePenalty);
    if (supports("frequency_penalty")) payload.frequency_penalty = numericParameter(frequencyPenalty);
    if (supports("min_p")) payload.min_p = numericParameter(minP);
    if (supports("top_k")) payload.top_k = Math.round(numericParameter(topK) ?? 0);
    if (supportsReasoningEffort && reasoningEffort) payload.reasoning_effort = reasoningEffort;
    if (supports("response_format") && responseFormat !== "text") payload.response_format = { type: responseFormat };
    return payload;
  }

  async function runCandidate(turnID: string, candidateID: string, messages: PlaygroundRequestMessage[], requestModel: string) {
    const controller = new AbortController();
    activeRequestRef.current = controller;
    setLoading(true);
    let terminalEventSeen = false;
    try {
      await streamPlaygroundChat(api, buildPayload(messages, requestModel), controller.signal, (_name, event: PlaygroundStreamEvent) => {
        if (event.type === "started") {
          updateCandidate(turnID, candidateID, (candidate) => ({
            ...candidate,
            requestID: event.request_id,
            startedAt: event.started_at,
          }));
          return;
        }
        if (event.type === "delta") {
          updateCandidate(turnID, candidateID, (candidate) => {
            const gatewayStarted = new Date(candidate.startedAt).getTime();
            const received = new Date(event.received_at).getTime();
            return {
              ...candidate,
              content: candidate.content + event.delta,
              mode: event.mode,
              liveTTFTMS: candidate.liveTTFTMS ?? (Number.isFinite(gatewayStarted) && Number.isFinite(received) ? Math.max(0, received - gatewayStarted) : undefined),
            };
          });
          return;
        }
        if (event.type === "completed" || event.type === "failed" || event.type === "cancelled") {
          terminalEventSeen = true;
          updateCandidate(turnID, candidateID, (candidate) => ({
            ...candidate,
            status: event.type === "completed" ? "completed" : event.type === "cancelled" ? "cancelled" : "failed",
            result: event,
            requestID: event.request_id ?? candidate.requestID,
            mode: event.timing?.mode ?? candidate.mode,
            content: candidate.content || extractAssistantText(event) || (event.type === "completed" ? tx("模型没有返回可展示内容。") : candidate.content),
            error: event.error,
          }));
        }
      });
      if (!terminalEventSeen) throw new Error(tx("演练流提前结束，未收到完成事件"));
    } catch (caught) {
      if (isAuthExpiredError(caught)) return;
      const cancelled = controller.signal.aborted || (caught instanceof DOMException && caught.name === "AbortError");
      const message = cancelled
        ? tx("已停止生成，保留部分输出")
        : caught instanceof Error && caught.message === playgroundMissingStreamBodyCode
          ? tx("浏览器未提供流式响应体")
          : caught instanceof Error ? caught.message : tx("演练请求失败");
      updateCandidate(turnID, candidateID, (candidate) => ({
        ...candidate,
        status: cancelled ? "cancelled" : "failed",
        error: message,
        blocked: !cancelled && message.startsWith(tx("请求已被内容安全策略阻断。")),
      }));
    } finally {
      if (activeRequestRef.current === controller) activeRequestRef.current = null;
      setLoading(false);
    }
  }

  function createCandidate(requestModel: string): PlaygroundCandidate {
    return {
      id: uniqueUIID("candidate"),
      model: requestModel,
      content: "",
      status: "streaming",
      startedAt: new Date().toISOString(),
      localStartedMS: Date.now(),
    };
  }

  function sendMessage(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault();
    const content = draft.trim();
    if ((!content && images.length === 0) || !modelName || !projectID || loading || imageUploadPendingRef.current) return;
    if (hasConversationImages && !supportsImages) {
      setError(tx("当前模型未声明图片输入能力。"));
      return;
    }
    const candidate = createCandidate(modelName);
    const submittedImages = images.slice();
    const turn: PlaygroundTurn = {
      id: uniqueUIID("turn"),
      userContent: content,
      userImages: submittedImages,
      userCreatedAt: new Date().toISOString(),
      candidates: [candidate],
      selectedCandidateID: candidate.id,
    };
    const messages = requestMessagesForNewTurn(turns, systemPrompt, content, submittedImages);
    isAtBottomRef.current = true;
    setTurns((current) => [...current, turn]);
    setDraft("");
    updateImages([]);
    void runCandidate(turn.id, candidate.id, messages, modelName);
  }

  function rerunTurn(turnID: string) {
    if (loading || !projectID) return;
    const turnIndex = turns.findIndex((turn) => turn.id === turnID);
    if (turnIndex < 0) return;
    const candidate = createCandidate(modelName);
    const branched = turns.slice(0, turnIndex + 1).map((turn, index) => index !== turnIndex ? turn : {
      ...turn,
      candidates: [...turn.candidates, candidate],
      selectedCandidateID: candidate.id,
    });
    const messages = requestMessagesForRerun(branched, turnIndex, systemPrompt);
    isAtBottomRef.current = true;
    setTurns(branched);
    void runCandidate(turnID, candidate.id, messages, modelName);
  }

  function selectCandidate(turnID: string, candidateID: string) {
    setTurns((current) => selectPlaygroundCandidateBranch(current, turnID, candidateID));
  }

  function requestModelChange(nextModelName: string) {
    if (nextModelName === modelName) {
      setPendingModelName("");
      return;
    }
    if (turns.length === 0 && images.length === 0) {
      changeModelName(nextModelName);
      return;
    }
    setPendingModelName(nextModelName);
  }

  function applyModelChange(keepContext: boolean) {
    if (!pendingModelName) return;
    const nextModel = models.find((model) => model.name === pendingModelName);
    if (keepContext && hasConversationImages && !modelSupportsPlaygroundImages(nextModel)) {
      setError(tx("包含图片的上下文不能切换到非视觉模型。"));
      return;
    }
    changeModelName(pendingModelName);
    setPendingModelName("");
    if (!keepContext) {
      setTurns([]);
      updateImages([]);
    }
    setError("");
  }

  function clearHistory() {
    activeRequestRef.current?.abort();
    invalidatePendingImageUploads();
    setTurns([]);
    updateImages([]);
    setError("");
  }

  async function addImages(selected: File[]) {
    if (selected.length === 0 || imageUploadPendingRef.current) return;
    if (!supportsImages) {
      setError(tx("当前模型未声明图片输入能力。"));
      return;
    }
    const candidates = selected.map((file) => ({ mediaType: file.type, sizeBytes: file.size }));
    const initialError = validatePlaygroundImageSelection(imagesRef.current, conversationImageBytesRef.current, candidates);
    if (initialError) {
      setError(playgroundImageSelectionErrorText(initialError));
      return;
    }
    const generation = imageUploadGenerationRef.current;
    imageUploadPendingRef.current = true;
    setImageUploadPending(true);
    try {
      const dataURLs = await Promise.all(selected.map(readPlaygroundImage));
      if (generation !== imageUploadGenerationRef.current) return;
      const attachments = selected.map((file, index) => ({
        id: uniqueUIID("image"),
        name: file.name,
        mediaType: file.type,
        sizeBytes: file.size,
        dataURL: dataURLs[index],
      }));
      const current = imagesRef.current;
      const currentError = validatePlaygroundImageSelection(current, conversationImageBytesRef.current, attachments);
      if (currentError) {
        setError(playgroundImageSelectionErrorText(currentError));
        return;
      }
      updateImages([...current, ...attachments]);
      setError("");
    } catch {
      if (generation === imageUploadGenerationRef.current) setError(tx("读取图片失败，请重新选择。"));
    } finally {
      if (generation === imageUploadGenerationRef.current) {
        imageUploadPendingRef.current = false;
        setImageUploadPending(false);
      }
    }
  }

  function exportSession() {
    const payload = {
      exported_at: new Date().toISOString(),
      model: modelName,
      system_prompt: systemPrompt,
      parameters: buildPayload([], modelName),
      turns: turns.map((turn) => ({
        ...turn,
        userImages: playgroundImagesForExport(turn.userImages),
      })),
    };
    const url = URL.createObjectURL(new Blob([prettyJSON(payload)], { type: "application/json" }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `tokenhub-playground-${new Date().toISOString().replaceAll(":", "-")}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  function handleChatScroll() {
    const element = chatRef.current;
    if (!element) return;
    const atBottom = element.scrollHeight - element.scrollTop - element.clientHeight < 40;
    isAtBottomRef.current = atBottom;
    if (atBottom) setShowNewContent(false);
  }

  function scrollToLatest() {
    const element = chatRef.current;
    if (!element) return;
    element.scrollTop = element.scrollHeight;
    isAtBottomRef.current = true;
    setShowNewContent(false);
  }

  return (
    <div className="playground-shell">
      <aside className="playground-config" aria-label={tx("模型配置")}>
        <label className="playground-model-select">
          <select value={pendingModelName || modelName} onChange={(event) => requestModelChange(event.target.value)} disabled={models.length === 0 || loading}>
            {models.length === 0 ? <option value="">{tx("暂无聊天模型")}</option> : null}
            {models.map((model) => {
              const routeCount = canViewRoutes ? activeRouteCount(model.name, data) : 0;
              return (
                <option key={model.name} value={model.name}>
                  {!canViewRoutes ? model.name : `${model.name} · ${countWithUnit(routeCount, "条路由", "route", "件のルート")}`}
                </option>
              );
            })}
          </select>
        </label>

        <div className="playground-config-body">
          <h2>{tx("模型配置")}</h2>
          <label className="playground-field">
            <span>{tx("选择项目")}</span>
            <select value={projectID} onChange={(event) => setProjectID(event.target.value)} disabled={projects.length === 0 || loading}>
              {projects.length === 0 ? <option value="">{tx("暂无可选项目")}</option> : null}
              {projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}
            </select>
          </label>
          <label className="playground-field">
            <span>{tx("系统提示")}</span>
            <textarea value={systemPrompt} onChange={(event) => setSystemPrompt(event.target.value)} disabled={loading} />
          </label>
          <PlaygroundConfigSlider label="max_tokens" value={maxTokens} onChange={setMaxTokens} min={maxTokenMinimum} max={maxTokenLimit} step={maxTokenStep} />
          {supports("temperature") ? <PlaygroundConfigSlider label="temperature" value={temperature} onChange={setTemperature} min={0} max={2} step={0.1} /> : null}
          {supportsReasoningEffort ? (
            <label className="playground-field">
              <span>{tx("推理强度")}</span>
              <select value={reasoningEffort} onChange={(event) => setReasoningEffort(event.target.value)}>
                <option value="">{tx("模型默认")}</option>
                {['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'].map((effort) => <option key={effort} value={effort}>{effort}</option>)}
              </select>
            </label>
          ) : null}
          {supports("response_format") ? (
            <label className="playground-field">
              <span>{tx("响应格式")}</span>
              <select value={responseFormat} onChange={(event) => setResponseFormat(event.target.value)}>
                <option value="text">text</option>
                <option value="json_object">json_object</option>
              </select>
            </label>
          ) : null}
          {advancedParameterCount > 0 ? (
            <details className="playground-advanced">
              <summary>{tx("高级参数")} · {advancedParameterCount}</summary>
              <div>
                {supports("top_p") ? <PlaygroundConfigSlider label="top_p" value={topP} onChange={setTopP} min={0} max={1} step={0.01} /> : null}
                {supports("presence_penalty") ? <PlaygroundConfigSlider label="presence_penalty" value={presencePenalty} onChange={setPresencePenalty} min={-2} max={2} step={0.1} /> : null}
                {supports("frequency_penalty") ? <PlaygroundConfigSlider label="frequency_penalty" value={frequencyPenalty} onChange={setFrequencyPenalty} min={-2} max={2} step={0.1} /> : null}
                {supports("min_p") ? <PlaygroundConfigSlider label="min_p" value={minP} onChange={setMinP} min={0} max={1} step={0.01} /> : null}
                {supports("top_k") ? <PlaygroundConfigSlider label="top_k" value={topK} onChange={setTopK} min={0} max={100} step={1} /> : null}
              </div>
            </details>
          ) : null}
          <div className="playground-capability-note">
            <strong>{tx("参数来源")}</strong>
            <span>{supportedParameters.size > 0 ? Array.from(supportedParameters).join(" · ") : tx("模型未声明可选参数，仅发送基础参数")}</span>
          </div>
        </div>
      </aside>

      <section className="playground-main" aria-label={tx("模型演练对话")}>
        <div className="playground-model-bar">
          <div className="playground-model-title">
            <button type="button" className="playground-copy-model" title={tx("复制模型名")} onClick={() => void copyText(modelName)}>
              <strong>{modelName || tx("选择模型")}</strong>
              <Copy size={13} />
            </button>
            <span>
              {contextWindow ? `${formatPlaygroundInteger(contextWindow)} ${tx("上下文")}` : `${tx("上下文")} -`}
              <em />{formatPlaygroundUSD(inputPrice)}/Mt {tx("输入")}<em />{formatPlaygroundUSD(outputPrice)}/Mt {tx("输出")}
              {canViewRoutes ? <><em />{countWithUnit(selectedModelRouteCount, "条启用路由", "active route", "件の有効ルート")}</> : null}
            </span>
          </div>
          <div className="playground-actions">
            <button type="button" className={showModelDetails ? "secondary-button compact active" : "secondary-button compact"} onClick={() => setShowModelDetails((value) => !value)}>
              <Sparkles size={14} />{tx("模型详情")}
            </button>
            <button type="button" className={showCode ? "secondary-button compact active" : "secondary-button compact"} onClick={() => setShowCode((value) => !value)}>
              <Code2 size={14} />{tx("查看代码")}
            </button>
            <button type="button" className="secondary-button compact" onClick={exportSession} disabled={turns.length === 0}>
              <Download size={14} />{tx("导出演练")}
            </button>
            <button type="button" className="secondary-button compact" onClick={clearHistory} disabled={turns.length === 0}>
              <Trash2 size={14} />{tx("清空历史")}
            </button>
          </div>
        </div>

        {pendingModelName ? (
          <div className="playground-model-change">
            <span>{tx("切换模型会改变后续请求的路由与参数能力。")}</span>
            <button type="button" className="primary-button compact" onClick={() => applyModelChange(false)}>{tx("新建会话并切换")}</button>
            <button
              type="button"
              className="secondary-button compact"
              onClick={() => applyModelChange(true)}
              disabled={hasConversationImages && !modelSupportsPlaygroundImages(models.find((model) => model.name === pendingModelName))}
              title={hasConversationImages ? tx("包含图片的上下文只能由视觉模型继续。") : undefined}
            >{tx("保留上下文继续")}</button>
            <button type="button" className="text-button" onClick={() => setPendingModelName("")}>{tx("取消")}</button>
          </div>
        ) : null}

        {showModelDetails ? (
          <div className="playground-detail-strip">
            <DetailField label={tx("类型")} value={selectedModel ? modelCategoryLabel(modelCategory(selectedModel)) : "-"} />
            <DetailField label={tx("能力")} value={selectedModel?.modality || "chat"} />
            <DetailField label={tx("图片输入")} value={supportsImages ? tx("支持") : tx("不支持")} />
            <DetailField label={tx("上下文")} value={contextWindow ? formatPlaygroundInteger(contextWindow) : "-"} />
            <DetailField label={tx("最近路由")} value={routeHasDetails(lastResult) ? `${lastResult?.route?.provider_name || lastResult?.route?.provider_id} / ${lastResult?.route?.provider_model}` : tx("仅授权角色可见")} />
          </div>
        ) : null}

        {showCode ? <div className="playground-code-drawer"><PlaygroundAPIExamples baseURL={api.baseURL} modelName={modelName || "gpt-4.1-mini"} supportsImages={supportsImages} /></div> : null}
        {error ? <div className="status-line error playground-error">{error}</div> : null}

        <div className="playground-chat" ref={chatRef} onScroll={handleChatScroll}>
          {turns.length === 0 ? (
            <div className="playground-empty">
              <Sparkles size={22} />
              <strong>{tx("诊断")} {modelName || tx("当前模型")}</strong>
              <span>{models.length === 0 ? tx("当前没有可演练模型。请先启用模型路由。") : tx("每轮记录流式性能、完整上下文用量和路由尝试")}</span>
            </div>
          ) : turns.map((turn, turnIndex) => {
            const candidate = selectedCandidate(turn);
            if (!candidate) return null;
            return (
              <div className="playground-turn" key={turn.id}>
                <div className="playground-message user">
                  <span>{tx("用户")} · {formatClock(turn.userCreatedAt)}</span>
                  {turn.userImages.length > 0 ? (
                    <div className="playground-message-images">
                      {turn.userImages.map((image) => <img alt={image.name} key={image.id} src={image.dataURL} />)}
                    </div>
                  ) : null}
                  {turn.userContent ? <p>{turn.userContent}</p> : <p className="playground-image-only">{tx("仅图片")}</p>}
                </div>
                <div className="playground-candidate-head">
                  <span>{tx("助手")} · {candidate.model} · {formatClock(candidate.startedAt)}</span>
                  <div>
                    {turn.candidates.length > 1 ? turn.candidates.map((item, index) => (
                      <button type="button" className={item.id === turn.selectedCandidateID ? "active" : ""} key={item.id} onClick={() => selectCandidate(turn.id, item.id)} disabled={loading}>
                        {formatPlaygroundInteger(index + 1)}
                      </button>
                    )) : null}
                    <button type="button" onClick={() => rerunTurn(turn.id)} disabled={loading || !projectID} title={tx("从这一轮重新生成，并移除后续分支")}>
                      <RotateCcw size={13} />{tx("重跑")}
                    </button>
                  </div>
                </div>
                <PlaygroundCandidateCard candidate={candidate} nowMS={nowMS} canViewRoutes={canViewRoutes} turnIndex={turnIndex} />
              </div>
            );
          })}
        </div>

        {showNewContent ? (
          <button type="button" className="playground-new-content" onClick={scrollToLatest}>
            <ArrowDown size={14} />{tx("查看新内容")}
          </button>
        ) : null}

        <form className="playground-composer" onSubmit={sendMessage}>
          {images.length > 0 ? (
            <div className="playground-image-previews">
              {images.map((image) => (
                <div className="playground-image-preview" key={image.id}>
                  <img alt={image.name} src={image.dataURL} />
                  <span><strong>{image.name}</strong><small>{formatBytes(image.sizeBytes)}</small></span>
                  <button type="button" onClick={() => updateImages((current) => current.filter((item) => item.id !== image.id))} title={tx("移除图片")}><X size={13} /></button>
                </div>
              ))}
            </div>
          ) : null}
          <textarea value={draft} onChange={(event) => setDraft(event.target.value)} placeholder={tx("输入用于诊断模型的消息...")} disabled={loading || models.length === 0 || projects.length === 0} />
          <div className="playground-composer-actions">
            <div className="playground-composer-tools">
              <input
                ref={imageInputRef}
                type="file"
                accept={playgroundImageMIMETypes.join(",")}
                multiple
                hidden
                disabled={imageUploadPending}
                onChange={(event) => {
                  const selected = event.currentTarget.files ? Array.from(event.currentTarget.files) : [];
                  event.currentTarget.value = "";
                  void addImages(selected);
                }}
              />
              <button
                type="button"
                className="playground-image-button"
                disabled={loading || imageUploadPending || models.length === 0 || !supportsImages || images.length >= playgroundMaxImagesPerMessage}
                onClick={() => imageInputRef.current?.click()}
                title={supportsImages ? tx("上传图片") : tx("当前模型未声明图片输入能力。")}
              ><ImagePlus size={17} /></button>
              <span className="playground-ephemeral">{supportsImages ? tx("支持图片输入，会话仅保存在当前页面") : tx("当前模型仅支持文本输入")}</span>
            </div>
            <div className="playground-foot">
              {lastResult?.request_id ? <span>{lastResult.request_id}</span> : null}
              {hasPlaygroundUsage(lastResult?.usage) ? <span>{tx("输入")} {formatPlaygroundInteger(lastResult?.usage?.prompt_tokens ?? 0)} · {tx("输出")} {formatPlaygroundInteger(lastResult?.usage?.completion_tokens ?? 0)}</span> : null}
            </div>
            {loading ? (
              <button className="playground-stop-button" type="button" onClick={() => activeRequestRef.current?.abort()} title={tx("停止生成")}><Square size={16} /></button>
            ) : (
              <button className="playground-send-button" disabled={imageUploadPending || (!draft.trim() && images.length === 0) || models.length === 0 || projects.length === 0} type="submit" title={tx("发送")}><Send size={18} /></button>
            )}
          </div>
        </form>
      </section>
    </div>
  );
}

function PlaygroundCandidateCard({ candidate, nowMS, canViewRoutes, turnIndex }: { candidate: PlaygroundCandidate; nowMS: number; canViewRoutes: boolean; turnIndex: number }) {
  const payload = candidate.result;
  const timing = payload?.timing;
  const usage = payload?.usage;
  const isStreaming = candidate.status === "streaming";
  const elapsedMS = Math.max(0, nowMS - candidate.localStartedMS);
  const rate = timing?.output_tokens_per_second ?? timing?.end_to_end_tokens_per_second;
  const usageKnown = hasPlaygroundUsage(usage);
  const modeLabel = (timing?.mode ?? candidate.mode) === "buffered" ? tx("非流式 · 缓冲返回") : tx("流式");
  const statusLabel = candidate.blocked ? tx("已阻断") : candidate.status === "cancelled" ? tx("已取消") : candidate.status === "failed" ? tx("失败") : tx("已完成");
  return (
    <div className={`playground-message assistant ${candidate.status}`}>
      <p>{candidate.content || candidate.error || (isStreaming ? tx("等待模型返回...") : tx("没有可展示内容"))}</p>
      {isStreaming ? (
        <div className="playground-live-metrics">
          <span className="playground-live-dot" />
          <strong>{candidate.mode === "buffered" ? tx("等待非流式上游完整响应") : tx("正在生成")}</strong>
          <span>{formatDuration(elapsedMS)}</span>
          {candidate.liveTTFTMS !== undefined && candidate.mode !== "buffered" ? <span>TTFT {formatDuration(candidate.liveTTFTMS)}</span> : null}
        </div>
      ) : (
        <>
          <div className={`playground-metric-summary ${candidate.status}`}>
            <span>{candidate.status === "completed" ? modeLabel : statusLabel}</span>
            {timing?.mode === "buffered" ? <span>TTFT {tx("不适用")}</span> : timing?.ttft_ms !== undefined ? <span>TTFT {formatDuration(timing.ttft_ms)}</span> : null}
            {rate !== undefined ? <span>{formatRate(rate)}</span> : null}
            {timing ? <span>{tx("总耗时")} {formatDuration(timing.total_ms)}</span> : null}
          </div>
          {usage || timing ? (
            <div className="playground-usage-summary">
              {usageKnown ? (
                <>
                  <span>{tx("输入")} {formatPlaygroundInteger(usage?.prompt_tokens)}</span>
                  <span>{tx("输出")} {formatPlaygroundInteger(usage?.completion_tokens)}</span>
                  <span>{tx("估算")} {formatPlaygroundUSD(usage?.estimated_cost_usd)}</span>
                </>
              ) : <span>{tx("用量不可用")}</span>}
              {timing?.completed_at ? <span>{formatClock(timing.completed_at)}</span> : null}
            </div>
          ) : null}
          {payload ? <PlaygroundDiagnostics payload={payload} canViewRoutes={canViewRoutes} turnIndex={turnIndex} /> : null}
        </>
      )}
    </div>
  );
}

function PlaygroundDiagnostics({ payload, canViewRoutes, turnIndex }: { payload: PlaygroundChatPayload; canViewRoutes: boolean; turnIndex: number }) {
  const timing = payload.timing;
  const usage = payload.usage;
  const usageKnown = hasPlaygroundUsage(usage);
  const route = payload.route;
  return (
    <details className="playground-diagnostics">
      <summary>{tx("诊断详情")}</summary>
      <div className="playground-diagnostics-body">
        <div className="playground-diagnostic-grid">
          <DetailField label="Request ID" value={payload.request_id || "-"} />
          <DetailField label={tx("轮次")} value={formatPlaygroundInteger(turnIndex + 1)} />
          <DetailField label={tx("开始时间")} value={formatDateTime(timing?.started_at)} />
          <DetailField label={tx("完成时间")} value={formatDateTime(timing?.completed_at)} />
          <DetailField label="TTFT" value={timing?.mode === "buffered" ? tx("不适用（上游非流式）") : formatDuration(timing?.ttft_ms)} />
          <DetailField label={tx("生成耗时")} value={formatDuration(timing?.generation_ms)} />
          <DetailField label={tx("总耗时")} value={formatDuration(timing?.total_ms)} />
          <DetailField label={tx("吞吐")} value={formatRate(timing?.output_tokens_per_second ?? timing?.end_to_end_tokens_per_second)} />
          <DetailField label={tx("输入 Tokens")} value={usageKnown ? formatPlaygroundInteger(usage?.prompt_tokens) : tx("不适用")} />
          <DetailField label={tx("缓存输入 Tokens")} value={usageKnown ? formatPlaygroundInteger(usage?.cached_input_tokens) : tx("不适用")} />
          <DetailField label={tx("输出 Tokens")} value={usageKnown ? formatPlaygroundInteger(usage?.completion_tokens) : tx("不适用")} />
          <DetailField label={tx("估算成本")} value={usageKnown ? formatPlaygroundUSD(usage?.estimated_cost_usd) : tx("不适用")} />
        </div>
        {canViewRoutes && routeHasDetails(payload) ? (
          <section className="playground-route-diagnostics">
            <h4>{tx("路由结果")}</h4>
            <div className="playground-route-summary">
              <strong>{route?.provider_name || route?.provider_id}</strong>
              <span>{route?.resource_name || route?.resource_id || tx("默认资源")}</span>
              <span>{route?.provider_model}</span>
              <span>{routeStrategyLabel(route?.strategy)}</span>
              {route?.transport ? <span>{route.transport}</span> : null}
              {route?.upstream_request_id ? <span>{route.upstream_request_id}</span> : null}
            </div>
            <PlaygroundAttemptTimeline attempts={payload.attempts ?? []} />
          </section>
        ) : null}
        <div className="playground-payload-grid">
          <section><h4>{tx("实际响应")}</h4><pre>{prettyJSON(payload.response)}</pre></section>
        </div>
      </div>
    </details>
  );
}

function PlaygroundAttemptTimeline({ attempts }: { attempts: PlaygroundRouteAttempt[] }) {
  if (attempts.length === 0) return null;
  return (
    <div className="playground-attempts">
      {attempts.map((attempt, index) => {
        const usageKnown = hasPlaygroundUsage(attempt.usage);
        return (
          <div className={attempt.status >= 200 && attempt.status < 300 ? "success" : "failed"} key={`${attempt.route?.route_id || "attempt"}-${index}`}>
            <span>{formatPlaygroundInteger(index + 1)}</span>
            <strong>{attempt.route?.provider_name || attempt.route?.provider_id || tx("已隐藏路由")}</strong>
            <em>{attempt.invoked ? tx("已调用") : tx("未调用")}</em>
            <small>{attempt.status || "-"}{attempt.upstream_status ? ` / ${tx("上游")} ${attempt.upstream_status}` : ""} · {formatDuration(attempt.latency_ms)}</small>
            {usageKnown ? (
              <small className="playground-attempt-usage">
                {tx("输入")} {formatPlaygroundInteger(attempt.usage?.prompt_tokens)} · {tx("输出")} {formatPlaygroundInteger(attempt.usage?.completion_tokens)} · {tx("估算")} {formatPlaygroundUSD(attempt.usage?.estimated_cost_usd)}
              </small>
            ) : null}
            {attempt.error ? <p>{attempt.code || tx("错误")} · {attempt.error}</p> : null}
          </div>
        );
      })}
    </div>
  );
}

export function PlaygroundConfigSlider({ label, value, onChange, min, max, step }: { label: string; value: string; onChange: (value: string) => void; min: number; max: number; step: number }) {
  const numeric = Number(value);
  const rangeValue = Number.isFinite(numeric) ? Math.min(max, Math.max(min, numeric)) : min;
  return (
    <label className="playground-slider">
      <div><span>{label}</span><input type="number" value={value} min={min} max={max} step={step} onChange={(event) => onChange(event.target.value)} /></div>
      <input type="range" value={rangeValue} min={min} max={max} step={step} onChange={(event) => onChange(event.target.value)} />
    </label>
  );
}

export function PlaygroundAPIExamples({ baseURL, modelName, supportsImages = false }: { baseURL: string; modelName: string; supportsImages?: boolean }) {
  const [language, setLanguage] = useState<ApiExampleLanguage>("python");
  const [copied, setCopied] = useState(false);
  const examples = useMemo(() => apiExampleScripts(baseURL, modelName, supportsImages), [baseURL, modelName, supportsImages]);
  const current = examples[language];
  async function copyCurrent() {
    const success = await copyText(current);
    setCopied(success);
    if (success) window.setTimeout(() => setCopied(false), 1400);
  }
  return (
    <section className="api-example-panel">
      <div className="api-example-header">
        <div><strong>{tx("API 使用")}</strong><span>{tx("使用以下代码示例集成 TokenHub 模型接口")}</span></div>
        <button className="icon-button subtle" onClick={() => void copyCurrent()} type="button" title={tx("复制代码")}>{copied ? <Check size={15} /> : <Copy size={15} />}</button>
      </div>
      <div className="api-example-tabs" role="tablist" aria-label={tx("API 调用语言")}>
        {apiExampleLanguages.map((item) => (
          <button aria-selected={language === item.key} className={language === item.key ? "api-example-tab active" : "api-example-tab"} key={item.key} onClick={() => { setLanguage(item.key); setCopied(false); }} role="tab" type="button">{item.label}</button>
        ))}
      </div>
      <div className="api-example-meta"><span>Chat Completions</span><em>{modelName || tx("未选择模型")}</em></div>
      <pre className="api-code-block"><code>{current}</code></pre>
    </section>
  );
}

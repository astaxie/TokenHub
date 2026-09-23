import { type ApiContext, type PlaygroundStreamEvent } from "../core/types";
import { readAPIError } from "../domain/formatting";
import { adminFetch } from "./payloads";

type PlaygroundEventHandler = (name: string, event: PlaygroundStreamEvent) => void;

export const playgroundMissingStreamBodyCode = "playground_missing_stream_body";

function consumeSSEBlock(block: string, onEvent: PlaygroundEventHandler) {
  let name = "message";
  const data: string[] = [];
  for (const rawLine of block.split(/\r?\n/)) {
    if (rawLine.startsWith("event:")) {
      name = rawLine.slice(6).trim();
    } else if (rawLine.startsWith("data:")) {
      data.push(rawLine.slice(5).trimStart());
    }
  }
  if (data.length === 0) return;
  const decoded = JSON.parse(data.join("\n")) as PlaygroundStreamEvent;
  onEvent(name, decoded);
}

export async function streamPlaygroundChat(
  api: ApiContext,
  payload: Record<string, unknown>,
  signal: AbortSignal,
  onEvent: PlaygroundEventHandler,
) {
  const response = await adminFetch(api, "/api/admin/playground/chat/stream", {
    method: "POST",
    body: JSON.stringify(payload),
    signal,
  });
  if (!response.ok) {
    throw new Error(await readAPIError(response));
  }
  if (!response.body) {
    throw new Error(playgroundMissingStreamBodyCode);
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  while (true) {
    const { done, value } = await reader.read();
    buffer += decoder.decode(value, { stream: !done }).replace(/\r\n/g, "\n");
    let boundary = buffer.indexOf("\n\n");
    while (boundary >= 0) {
      consumeSSEBlock(buffer.slice(0, boundary), onEvent);
      buffer = buffer.slice(boundary + 2);
      boundary = buffer.indexOf("\n\n");
    }
    if (done) break;
  }
  if (buffer.trim()) {
    consumeSSEBlock(buffer, onEvent);
  }
}

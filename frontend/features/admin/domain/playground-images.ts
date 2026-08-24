import type { Model } from "../core/types";

export const playgroundImageMIMETypes = ["image/jpeg", "image/png", "image/webp"] as const;
const playgroundImageMIMETypeSet: ReadonlySet<string> = new Set(playgroundImageMIMETypes);
export const playgroundMaxImagesPerMessage = 4;
export const playgroundMaxImageBytes = 5 * 1024 * 1024;
export const playgroundMaxConversationImageBytes = 12 * 1024 * 1024;

export type PlaygroundImageAttachment = {
  id: string;
  name: string;
  mediaType: string;
  sizeBytes: number;
  dataURL: string;
};

export type PlaygroundImageSelectionError = "too_many_images" | "unsupported_type" | "image_too_large" | "conversation_too_large";

export type PlaygroundImageCandidate = {
  readonly mediaType: string;
  readonly sizeBytes: number;
};

export type PlaygroundRequestContent = string | Array<
  | { type: "text"; text: string }
  | { type: "image_url"; image_url: { url: string } }
>;

export function modelSupportsPlaygroundImages(model?: Pick<Model, "input_modalities">) {
  return model?.input_modalities?.includes("image") === true;
}

export function playgroundMessageContent(text: string, images: readonly PlaygroundImageAttachment[]): PlaygroundRequestContent {
  const normalized = text.trim();
  if (images.length === 0) return normalized;
  return [
    ...(normalized ? [{ type: "text" as const, text: normalized }] : []),
    ...images.map((image) => ({
      type: "image_url" as const,
      image_url: { url: image.dataURL },
    })),
  ];
}

export function playgroundAttachmentBytes(images: readonly PlaygroundImageAttachment[]) {
  return images.reduce((total, image) => total + image.sizeBytes, 0);
}

export function validatePlaygroundImageSelection(
  current: readonly PlaygroundImageAttachment[],
  conversationBytes: number,
  selected: readonly PlaygroundImageCandidate[],
): PlaygroundImageSelectionError | undefined {
  if (current.length + selected.length > playgroundMaxImagesPerMessage) return "too_many_images";
  if (selected.some((image) => !playgroundImageMIMETypeSet.has(image.mediaType))) return "unsupported_type";
  if (selected.some((image) => image.sizeBytes > playgroundMaxImageBytes)) return "image_too_large";
  if (conversationBytes + playgroundAttachmentBytes(current) + selected.reduce((total, image) => total + image.sizeBytes, 0) > playgroundMaxConversationImageBytes) {
    return "conversation_too_large";
  }
  return undefined;
}

export function playgroundImagesForExport(images: readonly PlaygroundImageAttachment[]) {
  return images.map(({ id, name, mediaType, sizeBytes }) => ({
    id,
    name,
    media_type: mediaType,
    size_bytes: sizeBytes,
    content: "[image data omitted]",
  }));
}

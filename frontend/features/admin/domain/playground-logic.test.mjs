import assert from "node:assert/strict";
import test from "node:test";

import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  clampPlaygroundMaxTokens,
  hasPlaygroundUsage,
  playgroundMaxTokenLimit,
  selectPlaygroundCandidateBranch,
} = await importTypeScript(new URL("./playground-logic.ts", import.meta.url));
const {
  modelSupportsPlaygroundImages,
  playgroundAttachmentBytes,
  playgroundImagesForExport,
  playgroundMessageContent,
  validatePlaygroundImageSelection,
} = await importTypeScript(new URL("./playground-images.ts", import.meta.url));

test("playground max tokens honor model limits", () => {
  assert.equal(playgroundMaxTokenLimit(2048, 32768), 2048);
  assert.equal(playgroundMaxTokenLimit(undefined, 8192), 8192);
  assert.equal(clampPlaygroundMaxTokens(4096, 2048), 2048);
  assert.equal(clampPlaygroundMaxTokens(undefined, 1024), 1024);
});

test("unknown usage is not presented as zero usage", () => {
  assert.equal(hasPlaygroundUsage(undefined), false);
  assert.equal(hasPlaygroundUsage({ prompt_tokens: 0, completion_tokens: 0 }), false);
  assert.equal(hasPlaygroundUsage({ prompt_tokens: 12, completion_tokens: 3 }), true);
});

test("switching an earlier candidate removes descendants", () => {
  const turns = [
    { id: "one", selectedCandidateID: "a", value: 1 },
    { id: "two", selectedCandidateID: "c", value: 2 },
  ];
  assert.deepEqual(selectPlaygroundCandidateBranch(turns, "one", "b"), [
    { id: "one", selectedCandidateID: "b", value: 1 },
  ]);
  assert.equal(selectPlaygroundCandidateBranch(turns, "one", "a"), turns);
});

test("playground image support honors explicit model metadata", () => {
  assert.equal(modelSupportsPlaygroundImages({ input_modalities: ["text", "image"] }), true);
  assert.equal(modelSupportsPlaygroundImages({ input_modalities: ["text"] }), false);
  assert.equal(modelSupportsPlaygroundImages({}), false);
});

test("playground messages preserve the text-only wire shape", () => {
  assert.equal(playgroundMessageContent(" hello ", []), "hello");
  const image = { id: "image-1", name: "campus.png", mediaType: "image/png", sizeBytes: 3, dataURL: "data:image/png;base64,YWJj" };
  assert.deepEqual(playgroundMessageContent("describe", [image]), [
    { type: "text", text: "describe" },
    { type: "image_url", image_url: { url: image.dataURL } },
  ]);
  assert.deepEqual(playgroundMessageContent("", [image]), [
    { type: "image_url", image_url: { url: image.dataURL } },
  ]);
});

test("playground exports omit image payloads", () => {
  const images = [{ id: "image-1", name: "campus.png", mediaType: "image/png", sizeBytes: 3, dataURL: "data:image/png;base64,YWJj" }];
  assert.equal(playgroundAttachmentBytes(images), 3);
  assert.deepEqual(playgroundImagesForExport(images), [{
    id: "image-1", name: "campus.png", media_type: "image/png", size_bytes: 3, content: "[image data omitted]",
  }]);
});

test("playground image limits are revalidated against current attachments", () => {
  const image = (id, sizeBytes = 1, mediaType = "image/png") => ({ id, name: `${id}.png`, mediaType, sizeBytes, dataURL: "data:" });
  assert.equal(validatePlaygroundImageSelection([], 0, [image("one")]), undefined);
  assert.equal(validatePlaygroundImageSelection([image("one"), image("two"), image("three"), image("four")], 0, [image("five")]), "too_many_images");
  assert.equal(validatePlaygroundImageSelection([], 0, [image("gif", 1, "image/gif")]), "unsupported_type");
  assert.equal(validatePlaygroundImageSelection([], 0, [image("large", 5 * 1024 * 1024 + 1)]), "image_too_large");
  assert.equal(validatePlaygroundImageSelection([], 12 * 1024 * 1024, [image("overflow")]), "conversation_too_large");
});

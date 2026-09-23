type ClipboardWriter = {
  writeText: (value: string) => Promise<void>;
};

export type ClipboardEnvironment = {
  clipboard?: ClipboardWriter;
  document?: Document;
};

function browserClipboardEnvironment(): ClipboardEnvironment {
  let clipboard: ClipboardWriter | undefined;
  try {
    clipboard = typeof navigator === "undefined" ? undefined : navigator.clipboard;
  } catch {
    clipboard = undefined;
  }
  return {
    clipboard,
    document: typeof document === "undefined" ? undefined : document,
  };
}

function copyWithDocument(value: string, targetDocument?: Document): boolean {
  if (!targetDocument?.body || typeof targetDocument.execCommand !== "function") return false;

  const textarea = targetDocument.createElement("textarea");
  const activeElement = targetDocument.activeElement as HTMLElement | null;
  textarea.value = value;
  textarea.setAttribute("readonly", "");
  textarea.setAttribute("aria-hidden", "true");
  textarea.style.position = "fixed";
  textarea.style.inset = "0 auto auto 0";
  textarea.style.opacity = "0";
  textarea.style.pointerEvents = "none";
  targetDocument.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  textarea.setSelectionRange(0, value.length);

  try {
    return targetDocument.execCommand("copy");
  } catch {
    return false;
  } finally {
    textarea.remove();
    activeElement?.focus();
  }
}

export async function copyText(value: string, environment = browserClipboardEnvironment()): Promise<boolean> {
  if (environment.clipboard?.writeText) {
    try {
      await environment.clipboard.writeText(value);
      return true;
    } catch {
      // The legacy command remains available in insecure contexts and older browsers.
    }
  }
  return copyWithDocument(value, environment.document);
}

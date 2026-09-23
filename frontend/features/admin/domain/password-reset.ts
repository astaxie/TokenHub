export function consumePasswordResetTokenFromURL(href: string, replaceURL: (url: string) => void): string {
  const target = new URL(href);
  const fragment = new URLSearchParams(target.hash.replace(/^#/, ""));
  const fragmentToken = fragment.get("reset_token")?.trim() ?? "";
  let changed = false;

  if (target.searchParams.has("reset_token")) {
    target.searchParams.delete("reset_token");
    changed = true;
  }
  if (fragment.has("reset_token")) {
    fragment.delete("reset_token");
    target.hash = fragment.toString();
    changed = true;
  }
  if (changed) {
    replaceURL(`${target.pathname}${target.search}${target.hash}`);
  }
  return fragmentToken;
}

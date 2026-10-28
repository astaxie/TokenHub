export function supportsLocalSubscriptionCallback(providerType: string, apiBaseURL: string, browserURL?: string): boolean {
  if (providerType !== "chatgpt-subscription" || !browserURL) return false;
  const isLoopback = (raw: string) => {
    try {
      const url = new URL(raw);
      return url.protocol === "http:" && !url.username && !url.password && ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
    } catch { return false; }
  };
  return isLoopback(browserURL) && isLoopback(apiBaseURL);
}

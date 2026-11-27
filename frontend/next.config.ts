import type { NextConfig } from "next";

// Build-time only: Next inlines basePath into the client bundles.
function frontendBasePath() {
  const raw = process.env.TOKENHUB_FRONTEND_BASE_PATH?.trim() ?? "";
  if (!raw || raw === "/") return "";
  return `/${raw.replace(/^\/+|\/+$/g, "")}`;
}

const basePath = frontendBasePath();

const nextConfig: NextConfig = {
  reactStrictMode: true,
  output: "standalone",
  basePath,
  // Public files are served under basePath too, but raw <img> sources are not
  // rewritten by Next; expose the same value so views can prefix them.
  env: { NEXT_PUBLIC_TOKENHUB_BASE_PATH: basePath },
  async redirects() {
    if (!basePath) return [];
    // Keep bare-root entries and old unprefixed bookmarks working when the console
    // is served under a sub-path. Prefixed URLs themselves must not be rewritten.
    const prefix = basePath.slice(1).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    return [
      { source: "/", destination: basePath, basePath: false, permanent: false },
      {
        source: `/:path((?!${prefix}(?:/|$)).*)`,
        destination: `${basePath}/:path`,
        basePath: false,
        permanent: false,
      },
    ];
  },
  // Keep isolated UI runs independent of unrelated lockfiles above the checkout.
  turbopack: process.env.TOKENHUB_UI_RUN === "1" ? { root: process.cwd() } : undefined,
  allowedDevOrigins: ["127.0.0.1", "localhost"],
  distDir: process.env.TOKENHUB_NEXT_DIST_DIR || ".next",
  async headers() {
    return [
      {
        source: "/(.*)",
        headers: [
          { key: "Permissions-Policy", value: "camera=(), geolocation=(), microphone=()" },
          { key: "Referrer-Policy", value: "no-referrer" },
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "X-Frame-Options", value: "DENY" },
        ],
      },
    ];
  },
};

export default nextConfig;

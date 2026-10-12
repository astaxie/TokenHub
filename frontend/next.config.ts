import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  output: "standalone",
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

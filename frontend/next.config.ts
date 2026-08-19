import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  output: "standalone",
  allowedDevOrigins: ["127.0.0.1", "localhost"],
  distDir: process.env.TOKENHUB_NEXT_DIST_DIR || ".next",
};

export default nextConfig;

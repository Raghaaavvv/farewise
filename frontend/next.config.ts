// Forward same-origin API requests to Go while keeping backend access out of browser code.
import type { NextConfig } from "next";

const config: NextConfig = {
  devIndicators: false,
  // Polling also works where macOS native file events are unavailable.
  webpack(config, { dev }) {
    if (dev) {
      config.watchOptions = {
        ...config.watchOptions,
        poll: 1000,
        aggregateTimeout: 300,
      };
    }

    return config;
  },
  // The browser uses one origin. Next forwards /api to the independent Go API.
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${process.env.API_BASE_URL ?? "http://127.0.0.1:8080"}/api/:path*`,
      },
    ];
  },
};

export default config;

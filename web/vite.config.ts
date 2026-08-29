import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Builds straight into internal/webui/dist, which go:embed picks up
// (ADR-0001). See internal/webui/embed.go and web/README.md.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      // `npm run dev` talks to a real hub on 8081 for API calls, so the
      // dashboard can be developed without rebuilding the Go binary.
      "/v1": "http://127.0.0.1:8081",
    },
  },
});

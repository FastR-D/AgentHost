import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In development, run the Go server on :8080 and `npm run dev` here.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://127.0.0.1:8080", changeOrigin: false },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    chunkSizeWarningLimit: 2000,
    rollupOptions: {
      output: {
        manualChunks(id) {
          const path = id.replaceAll("\\", "/");
          if (/\/node_modules\/(react|react-dom)\//.test(path)) return "react-core";
          if (/\/node_modules\/@assistant-ui\/(react|react-markdown)\//.test(path)) return "assistant-ui";
          if (/\/node_modules\/(react-markdown|remark-gfm)\//.test(path)) return "markdown";
        },
      },
    },
  },
});

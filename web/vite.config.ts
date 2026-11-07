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
        manualChunks: {
          // React core — cached independently, almost never changes
          "react-core": ["react", "react-dom"],
          // assistant-ui — large, infrequently updated
          "assistant-ui": ["@assistant-ui/react", "@assistant-ui/react-markdown"],
          // Markdown pipeline
          "markdown": ["react-markdown", "remark-gfm"],
        },
      },
    },
  },
});

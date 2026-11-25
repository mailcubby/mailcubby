import { fileURLToPath, URL } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  base: "/",
  plugins: [
    tanstackRouter({
      target: "react",
      autoCodeSplitting: true,
      routeFileIgnorePattern: "\\.test\\.tsx?$",
    }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    port: 5173,
    // changeOrigin must stay false: the backend's cross-origin protection in
    // local mode (FR-045) relies on the proxied Host matching the page Origin.
    proxy: {
      "/api": {
        target: "http://localhost:8025",
        changeOrigin: false,
      },
      "/render": {
        target: "http://localhost:8025",
        changeOrigin: false,
      },
    },
  },
});

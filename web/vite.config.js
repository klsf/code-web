import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  plugins: [vue()],
  server: {
    host: "127.0.0.1",
    port: 5173,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true
      },
      "/ws": {
        target: "ws://127.0.0.1:8080",
        ws: true,
        changeOrigin: true
      },
      "/uploads": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true
      },
      "/app-config.js": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true
      }
    }
  },
  build: {
    outDir: "../static",
    assetsDir: "app",
    emptyOutDir: true,
    rollupOptions: {
      output: {
        entryFileNames: "app/index.js",
        chunkFileNames: "app/[name].js",
        assetFileNames: (assetInfo) => {
          if (assetInfo.name && assetInfo.name.endsWith(".css")) {
            return "app/index.css";
          }
          return "app/[name][extname]";
        }
      }
    }
  }
});

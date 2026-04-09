import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  plugins: [vue()],
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

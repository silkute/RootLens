import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import tailwind from "@tailwindcss/vite";

export default defineConfig({
  plugins: [vue(), tailwind()],
  build: {
    outDir: "../internal/hub/static",
    emptyOutDir: true,
    sourcemap: false,
  },
  server: { proxy: { "/api": "http://127.0.0.1:8765" } },
});

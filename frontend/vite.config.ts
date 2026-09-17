import { defineConfig, type Plugin } from "vite";
import vue from "@vitejs/plugin-vue";
import wails from "@wailsio/runtime/plugins/vite";

function developmentContentSecurityPolicy(): Plugin {
  return {
    name: "desktop-guard-development-csp",
    apply: "serve",
    enforce: "pre",
    transformIndexHtml(html) {
      return html.replace(
        "style-src 'self';",
        "style-src 'self' 'unsafe-inline';",
      );
    },
  };
}

export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [developmentContentSecurityPolicy(), vue(), wails("./bindings")],
});

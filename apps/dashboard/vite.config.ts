import tailwindcss from "@tailwindcss/vite"
import { tanstackStart } from "@tanstack/react-start/plugin/vite"
import viteReact from "@vitejs/plugin-react"
import { nitro } from "nitro/vite"
import { defineConfig } from "vite"

const APP_BASE = "/app"

export default defineConfig({
  resolve: { tsconfigPaths: true, dedupe: ["react", "react-dom"] },
  server: { port: 4871 },
  plugins: [
    tailwindcss(),
    // Everything the dashboard serves, its auth and health endpoints included,
    // lives under /app, so one proxy rule covers it.
    tanstackStart({ router: { basepath: APP_BASE } }),
    // Assets go to /app/assets. Vite's `base` isn't used: Nitro would take it
    // as its own baseURL and redirect everything else under it.
    nitro({ preset: "node-server", buildAssetsDir: `${APP_BASE.slice(1)}/assets` }),
    viteReact(),
  ],
})

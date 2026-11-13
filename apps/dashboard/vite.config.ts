import tailwindcss from "@tailwindcss/vite"
import { tanstackStart } from "@tanstack/react-start/plugin/vite"
import viteReact from "@vitejs/plugin-react"
import { nitro } from "nitro/vite"
import { defineConfig } from "vite"

export default defineConfig({
  resolve: { tsconfigPaths: true, dedupe: ["react", "react-dom"] },
  server: { port: 4871 },
  plugins: [tailwindcss(), tanstackStart(), nitro({ preset: "node-server" }), viteReact()],
})

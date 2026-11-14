import { createFileRoute } from "@tanstack/react-router"
import { getAuth } from "@/server/auth"
import { getDb } from "@/server/db"

// 200 when the dashboard can serve: its own database opens and its tables
// exist. The engine has its own /health.
export const Route = createFileRoute("/api/health")({
  server: {
    handlers: {
      GET: async () => {
        try {
          await getAuth()
          getDb().prepare("SELECT 1").get()
          return Response.json({ status: "ok" })
        } catch {
          return Response.json({ status: "unavailable" }, { status: 503 })
        }
      },
    },
  },
})

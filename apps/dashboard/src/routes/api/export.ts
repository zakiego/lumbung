import { createFileRoute } from "@tanstack/react-router"
import { exportRoute } from "@/server/transfer"

// GET /app/api/export?db=&branch=&format=db|sql streams the branch as a download.
export const Route = createFileRoute("/api/export")({
  server: {
    handlers: {
      GET: ({ request }: { request: Request }) => exportRoute(request),
    },
  },
})

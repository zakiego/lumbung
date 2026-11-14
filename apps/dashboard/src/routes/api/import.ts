import { createFileRoute } from "@tanstack/react-router"
import { importRoute } from "@/server/transfer"

// POST /app/api/import?ws=&db=&branch=&format=db|sql&mode=empty|replace, the file as the body.
export const Route = createFileRoute("/api/import")({
  server: {
    handlers: {
      POST: ({ request }: { request: Request }) => importRoute(request),
    },
  },
})

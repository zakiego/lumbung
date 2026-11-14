import { createFileRoute } from "@tanstack/react-router"
import { downloadObject, uploadObject } from "@/server/objects"

// GET /app/api/objects?bucket=&key= downloads; PUT with the file as the body uploads.
export const Route = createFileRoute("/api/objects")({
  server: {
    handlers: {
      GET: ({ request }: { request: Request }) => downloadObject(request),
      PUT: ({ request }: { request: Request }) => uploadObject(request),
    },
  },
})

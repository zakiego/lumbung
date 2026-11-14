import { createFileRoute } from "@tanstack/react-router"
import { abortUpload, downloadObject, multipartObject, uploadObject } from "@/server/objects"

// GET /app/api/objects?ws=&bucket=&key= downloads; PUT with the file as the body
// uploads (or one part, with &uploadId=&partNumber=); POST starts (&uploads)
// or completes (&uploadId=) a multipart upload; DELETE &uploadId= aborts one.
export const Route = createFileRoute("/api/objects")({
  server: {
    handlers: {
      GET: ({ request }: { request: Request }) => downloadObject(request),
      PUT: ({ request }: { request: Request }) => uploadObject(request),
      POST: ({ request }: { request: Request }) => multipartObject(request),
      DELETE: ({ request }: { request: Request }) => abortUpload(request),
    },
  },
})

// An S3 client for a Lumbung instance: the endpoint is the engine's URL plus
// /v1/s3, with path-style addressing. Any region works.
import { S3Client } from "@aws-sdk/client-s3"

export function connect(engineURL: string, accessKeyId: string, secretAccessKey: string): S3Client {
  return new S3Client({
    endpoint: `${engineURL}/v1/s3`,
    forcePathStyle: true,
    region: "us-east-1",
    credentials: { accessKeyId, secretAccessKey },
  })
}

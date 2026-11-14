package api

// REST endpoints for buckets and objects (PLAN 16.3). The SDK, CLI,
// dashboard and MCP use these with bearer tokens; S3 clients use /v1/s3.
// Keys travel in a query parameter, so path cleaning never touches them.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/s3proto"
)

// BucketOut is a bucket.
type BucketOut struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	PublicRead  bool      `json:"public_read" doc:"Anyone may read objects without a token."`
	CORSOrigins []string  `json:"cors_origins" doc:"Origins a browser may call the S3 API from."`
	SizeBytes   int64     `json:"size_bytes" doc:"Bytes the objects take."`
	ObjectCount int64     `json:"object_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func bucketOut(b meta.Bucket) BucketOut {
	return BucketOut{ID: b.ID, Name: b.Name, PublicRead: b.PublicRead, CORSOrigins: b.CORSOrigins, SizeBytes: b.SizeBytes,
		ObjectCount: b.ObjectCount, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
}

// ObjectOut is an object's record.
type ObjectOut struct {
	Key          string            `json:"key"`
	Size         int64             `json:"size"`
	ETag         string            `json:"etag" doc:"MD5 of the content, or for a multipart object the MD5 of the parts' MD5s, a dash and the number of parts. Without quotes."`
	ContentType  string            `json:"content_type"`
	LastModified time.Time         `json:"last_modified"`
	Headers      map[string]string `json:"headers" doc:"Stored Cache-Control, Content-Disposition, Content-Encoding, Content-Language and Expires."`
	Metadata     map[string]string `json:"metadata" doc:"Custom metadata (x-amz-meta-* headers), lowercase names without the prefix."`
}

func objectOut(o meta.Object) ObjectOut {
	return ObjectOut{Key: o.Key, Size: o.Size, ETag: o.ETag, ContentType: o.ContentType, LastModified: o.ModifiedAt,
		Headers: o.Headers, Metadata: o.Metadata}
}

// BucketPath is the bucket path parameter.
type BucketPath struct {
	Bucket string `path:"bucket" doc:"Bucket name or id."`
}

// ObjectPath names one object.
type ObjectPath struct {
	Bucket string `path:"bucket" doc:"Bucket name or id."`
	Key    string `query:"key" required:"true" minLength:"1" maxLength:"1024" doc:"Object key."`
}

type bucketBody struct{ Body BucketOut }

type bucketList struct {
	Body struct {
		Data       []BucketOut `json:"data"`
		NextCursor string      `json:"next_cursor" doc:"Pass as cursor for the next page; empty on the last page."`
	}
}

type createBucketIn struct {
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"200" doc:"Retrying with the same key returns the first response instead of creating twice."`
	Body           struct {
		Name string `json:"name" pattern:"^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$" doc:"3 to 63 lowercase letters, digits and hyphens."`
	}
}

type updateBucketIn struct {
	BucketPath
	Body struct {
		Name        *string   `json:"name,omitempty" pattern:"^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$" doc:"Rename the bucket. S3 paths and URLs that name it stop working."`
		PublicRead  *bool     `json:"public_read,omitempty"`
		CORSOrigins *[]string `json:"cors_origins,omitempty" maxItems:"20" doc:"* or scheme://host[:port] each."`
	}
}

type listObjectsIn struct {
	BucketPath
	Prefix    string `query:"prefix" maxLength:"1024" doc:"Only keys that start with this."`
	Delimiter string `query:"delimiter" maxLength:"16" doc:"Roll up keys that contain this after the prefix into prefixes, like folders. Usually /."`
	Limit     int    `query:"limit" minimum:"1" maximum:"1000" default:"100" doc:"Most objects and prefixes to return together."`
	Cursor    string `query:"cursor" doc:"The next_cursor from the previous page."`
}

type objectList struct {
	Body struct {
		Data       []ObjectOut `json:"data"`
		Prefixes   []string    `json:"prefixes" doc:"Common prefixes under the delimiter, like folders."`
		NextCursor string      `json:"next_cursor" doc:"Pass as cursor for the next page; empty on the last page."`
	}
}

type objectBody struct{ Body ObjectOut }

type copyIn struct {
	BucketPath
	Body struct {
		FromBucket  string            `json:"from_bucket,omitempty" doc:"Source bucket name or id; defaults to this bucket."`
		FromKey     string            `json:"from_key" minLength:"1" maxLength:"1024"`
		Key         string            `json:"key" minLength:"1" maxLength:"1024" doc:"Key of the copy in this bucket."`
		ContentType string            `json:"content_type,omitempty" doc:"Set this or metadata to give the copy new attributes instead of the source's. Without it the source's content type is kept."`
		Metadata    map[string]string `json:"metadata,omitempty" doc:"The copy's metadata, replacing the source's."`
	}
}

type presignIn struct {
	BucketPath
	Body struct {
		Key       string `json:"key" minLength:"1" maxLength:"1024"`
		Method    string `json:"method,omitempty" enum:"GET,PUT" default:"GET" doc:"GET to download, PUT to upload."`
		ExpiresIn int    `json:"expires_in,omitempty" minimum:"1" maximum:"604800" default:"3600" doc:"Seconds the URL stays valid, at most LUMBUNG_PRESIGN_MAX_EXPIRY."`
	}
}

type presignOut struct {
	Body struct {
		URL       string    `json:"url" doc:"The presigned S3 URL. Anyone holding it can use it until it expires or the token is revoked."`
		Method    string    `json:"method"`
		ExpiresAt time.Time `json:"expires_at"`
	}
}

type objectGetIn struct {
	ObjectPath
	Range       string `header:"Range" doc:"One byte range, bytes=start-end."`
	IfNoneMatch string `header:"If-None-Match"`
	IfMatch     string `header:"If-Match"`
}

// bucketFor resolves a bucket the principal can see and checks its access.
// A bucket it can't see is not found, so tokens don't learn which exist.
func (s *Server) bucketFor(ctx context.Context, p auth.Principal, ref, access string) (meta.Bucket, error) {
	b, err := s.eng.GetBucket(ctx, ref)
	if err != nil || !p.SeesBucket(b.ID) {
		if err != nil && apierr.As(err).Code != apierr.BucketNotFound {
			return meta.Bucket{}, err
		}
		return meta.Bucket{}, apierr.Newf(apierr.BucketNotFound, "no bucket %q", ref)
	}
	if !p.BucketAtLeast(access, b.ID) {
		return meta.Bucket{}, apierr.New(apierr.Forbidden, "this token can't do that on this bucket")
	}
	return b, nil
}

type keyHTTP struct{}

// requestInfoMW hands the request's host and scheme to an operation, for
// building presigned URLs.
func requestInfoMW(ctx huma.Context, next func(huma.Context)) {
	next(huma.WithValue(ctx, keyHTTP{}, ctx))
}

// requestBase is the scheme and host clients used to reach the engine. The
// scheme comes from X-Forwarded-Proto only when the connection is from a
// trusted proxy.
func (s *Server) requestBase(tls bool, remoteAddr, forwardedProto, host string) (string, string) {
	scheme := "http"
	if tls {
		scheme = "https"
	}
	addr, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		addr = remoteAddr
	}
	if p := strings.ToLower(forwardedProto); (p == "https" || p == "http") && s.trusted(net.ParseIP(addr)) {
		scheme = p
	}
	return scheme, host
}

func (s *Server) registerBuckets(api huma.API) {
	huma.Register(api, op("list-buckets", http.MethodGet, "/v1/buckets", "List buckets", "Buckets", ""),
		func(ctx context.Context, in *PageIn) (*bucketList, error) {
			p := principalFrom(ctx)
			all, err := s.eng.ListBuckets(ctx)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			visible := []meta.Bucket{}
			for _, b := range all {
				if p.SeesBucket(b.ID) {
					visible = append(visible, b)
				}
			}
			items, next, err := page(visible, func(b meta.Bucket) string { return b.Name }, *in)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			out := &bucketList{}
			out.Body.Data = make([]BucketOut, 0, len(items))
			out.Body.NextCursor = next
			for _, b := range items {
				out.Body.Data = append(out.Body.Data, bucketOut(b))
			}
			return out, nil
		})

	huma.Register(api, created(op("create-bucket", http.MethodPost, "/v1/buckets", "Create a bucket", "Buckets", "bucket.create")),
		func(ctx context.Context, in *createBucketIn) (*bucketBody, error) {
			if err := requireInstanceAdmin(principalFrom(ctx)); err != nil {
				return nil, toProblem(ctx, err)
			}
			return idempotent(ctx, s, "create-bucket", in.IdempotencyKey, in.Body, func() (*bucketBody, error) {
				b, err := s.eng.CreateBucket(ctx, in.Body.Name)
				if err != nil {
					return nil, toProblem(ctx, err)
				}
				setTarget(ctx, b.ID)
				return &bucketBody{Body: bucketOut(b)}, nil
			})
		})

	huma.Register(api, op("get-bucket", http.MethodGet, "/v1/buckets/{bucket}", "Get a bucket", "Buckets", ""),
		func(ctx context.Context, in *BucketPath) (*bucketBody, error) {
			b, err := s.bucketFor(ctx, principalFrom(ctx), in.Bucket, auth.AccessRead)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &bucketBody{Body: bucketOut(b)}, nil
		})

	huma.Register(api, op("update-bucket", http.MethodPatch, "/v1/buckets/{bucket}", "Change a bucket's name or settings", "Buckets", "bucket.update"),
		func(ctx context.Context, in *updateBucketIn) (*bucketBody, error) {
			b, err := s.bucketFor(ctx, principalFrom(ctx), in.Bucket, auth.AccessAdmin)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, b.ID)
			b, err = s.eng.UpdateBucket(ctx, b.ID, meta.BucketUpdate{Name: in.Body.Name, PublicRead: in.Body.PublicRead, CORSOrigins: in.Body.CORSOrigins})
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &bucketBody{Body: bucketOut(b)}, nil
		})

	huma.Register(api, noContent(op("delete-bucket", http.MethodDelete, "/v1/buckets/{bucket}", "Delete an empty bucket", "Buckets", "bucket.delete")),
		func(ctx context.Context, in *BucketPath) (*struct{}, error) {
			p := principalFrom(ctx)
			b, err := s.bucketFor(ctx, p, in.Bucket, auth.AccessRead)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, b.ID)
			if err := requireInstanceAdmin(p); err != nil {
				return nil, toProblem(ctx, err)
			}
			if err := s.eng.DeleteBucket(ctx, b.ID); err != nil {
				return nil, toProblem(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("list-objects", http.MethodGet, "/v1/buckets/{bucket}/objects", "List objects", "Objects", ""),
		func(ctx context.Context, in *listObjectsIn) (*objectList, error) {
			b, err := s.bucketFor(ctx, principalFrom(ctx), in.Bucket, auth.AccessRead)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			after := ""
			if in.Cursor != "" {
				raw, err := base64URL.DecodeString(in.Cursor)
				if err != nil {
					return nil, toProblem(ctx, apierr.New(apierr.BadRequest, "invalid cursor"))
				}
				after = string(raw)
			}
			res, err := s.eng.ListObjects(ctx, engine.ListObjectsInput{BucketID: b.ID, Prefix: in.Prefix, Delimiter: in.Delimiter, After: after, Max: in.Limit})
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			out := &objectList{}
			out.Body.Data = make([]ObjectOut, 0, len(res.Objects))
			for _, o := range res.Objects {
				out.Body.Data = append(out.Body.Data, objectOut(o))
			}
			out.Body.Prefixes = res.Prefixes
			if res.Truncated {
				out.Body.NextCursor = base64URL.EncodeToString([]byte(res.Next))
			}
			return out, nil
		})

	putOp := op("put-object", http.MethodPut, "/v1/buckets/{bucket}/object", "Upload an object", "Objects", "")
	putOp.Description = "Send the content as the raw request body with a Content-Length. Content-Type, Cache-Control, Content-Disposition, Content-Encoding, Content-Language and Expires are stored, and x-amz-meta-* headers become metadata. An existing object with the key is replaced."
	putOp.RequestBody = &huma.RequestBody{Required: false, Content: map[string]*huma.MediaType{
		"application/octet-stream": {Schema: &huma.Schema{Type: "string", Format: "binary"}},
	}}
	putOp.MaxBodyBytes = -1
	putOp.BodyReadTimeout = -1
	putOp.Middlewares = huma.Middlewares{streamBody, requestInfoMW}
	huma.Register(api, putOp, func(ctx context.Context, in *ObjectPath) (*objectBody, error) {
		b, err := s.bucketFor(ctx, principalFrom(ctx), in.Bucket, auth.AccessWrite)
		if err != nil {
			return nil, toProblem(ctx, err)
		}
		hctx, _ := ctx.Value(keyHTTP{}).(huma.Context)
		body, _ := ctx.Value(keyBody{}).(io.Reader)
		if hctx == nil || body == nil {
			return nil, toProblem(ctx, apierr.New(apierr.BadRequest, "send the content as the request body"))
		}
		size, err := strconv.ParseInt(hctx.Header("Content-Length"), 10, 64)
		if err != nil || size < 0 {
			return nil, toProblem(ctx, apierr.New(apierr.LengthRequired, "send a Content-Length"))
		}
		h := http.Header{}
		hctx.EachHeader(func(name, value string) { h.Add(name, value) })
		o, err := s.eng.PutObject(ctx, engine.PutObjectInput{BucketID: b.ID, Key: in.Key, Size: size, Body: body, Attrs: s3Attrs(h)})
		if err != nil {
			return nil, toProblem(ctx, err)
		}
		return &objectBody{Body: objectOut(o)}, nil
	})

	getOp := op("get-object", http.MethodGet, "/v1/buckets/{bucket}/object", "Download an object", "Objects", "")
	getOp.Description = "Streams the content with its stored headers, ETag and Last-Modified. A Range header returns 206 with that part. HEAD returns the headers only."
	getOp.Responses = map[string]*huma.Response{
		"200": {Description: "The content, with its stored Content-Type.", Content: map[string]*huma.MediaType{"*/*": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}},
		"206": {Description: "The requested range.", Content: map[string]*huma.MediaType{"*/*": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}},
		"304": {Description: "Not modified (If-None-Match)."},
	}
	huma.Register(api, getOp, func(ctx context.Context, in *objectGetIn) (*huma.StreamResponse, error) {
		b, err := s.bucketFor(ctx, principalFrom(ctx), in.Bucket, auth.AccessRead)
		if err != nil {
			return nil, toProblem(ctx, err)
		}
		o, f, err := s.eng.OpenObject(ctx, b.ID, in.Key)
		if err != nil {
			return nil, toProblem(ctx, err)
		}
		cond := http.Header{}
		if in.IfMatch != "" {
			cond.Set("If-Match", in.IfMatch)
		}
		if in.IfNoneMatch != "" {
			cond.Set("If-None-Match", in.IfNoneMatch)
		}
		status := checkConditions(cond, o, false)
		if status == http.StatusPreconditionFailed {
			f.Close()
			return nil, toProblem(ctx, apierr.New(apierr.PreconditionFailed, "If-Match doesn't hold"))
		}
		start, end, ranged := int64(0), o.Size-1, false
		if in.Range != "" && status == 0 {
			st, en, ok, bad := parseRange(in.Range, o.Size)
			if bad {
				f.Close()
				return nil, toProblem(ctx, apierr.Newf(apierr.InvalidRange, "the object is %d bytes", o.Size))
			}
			start, end, ranged = st, en, ok
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			defer f.Close()
			hctx.SetHeader("ETag", quoteETag(o.ETag))
			hctx.SetHeader("Last-Modified", o.ModifiedAt.UTC().Format(http.TimeFormat))
			safety := http.Header{}
			setContentSafety(safety)
			for k := range safety {
				hctx.SetHeader(k, safety.Get(k))
			}
			if status == http.StatusNotModified {
				hctx.SetStatus(http.StatusNotModified)
				return
			}
			hctx.SetHeader("Content-Type", o.ContentType)
			hctx.SetHeader("Accept-Ranges", "bytes")
			for k, v := range o.Headers {
				hctx.SetHeader(k, v)
			}
			for k, v := range o.Metadata {
				hctx.SetHeader("X-Amz-Meta-"+k, v)
			}
			n := end - start + 1
			if o.Size == 0 {
				n = 0
			}
			hctx.SetHeader("Content-Length", strconv.FormatInt(n, 10))
			if ranged {
				hctx.SetHeader("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, o.Size))
				hctx.SetStatus(http.StatusPartialContent)
			} else {
				hctx.SetStatus(http.StatusOK)
			}
			if hctx.Method() == http.MethodHead || n == 0 {
				return
			}
			if _, err := io.Copy(hctx.BodyWriter(), io.NewSectionReader(f, start, n)); err != nil {
				panic(http.ErrAbortHandler)
			}
		}}, nil
	})

	huma.Register(api, noContent(op("delete-object", http.MethodDelete, "/v1/buckets/{bucket}/object", "Delete an object", "Objects", "")),
		func(ctx context.Context, in *ObjectPath) (*struct{}, error) {
			b, err := s.bucketFor(ctx, principalFrom(ctx), in.Bucket, auth.AccessWrite)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			if _, err := s.eng.DeleteObject(ctx, b.ID, in.Key); err != nil {
				return nil, toProblem(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("copy-object", http.MethodPost, "/v1/buckets/{bucket}/copy", "Copy an object into this bucket", "Objects", ""),
		func(ctx context.Context, in *copyIn) (*objectBody, error) {
			p := principalFrom(ctx)
			dst, err := s.bucketFor(ctx, p, in.Bucket, auth.AccessWrite)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			src := dst
			if in.Body.FromBucket != "" {
				if src, err = s.bucketFor(ctx, p, in.Body.FromBucket, auth.AccessRead); err != nil {
					return nil, toProblem(ctx, err)
				}
			}
			o, err := s.eng.CopyObject(ctx, engine.CopyObjectInput{SrcBucketID: src.ID, SrcKey: in.Body.FromKey, BucketID: dst.ID, Key: in.Body.Key,
				Replace: in.Body.ContentType != "" || in.Body.Metadata != nil, Attrs: engine.ObjectAttrs{ContentType: in.Body.ContentType, Metadata: in.Body.Metadata}})
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &objectBody{Body: objectOut(o)}, nil
		})

	presignOp := op("presign-object", http.MethodPost, "/v1/buckets/{bucket}/presign", "Make a presigned S3 URL", "Objects", "")
	presignOp.Description = "Signs a GET or PUT URL for one key with the calling token's access key, so the token must have one. GET needs read access, PUT needs write. The URL stops working when it expires or the token is revoked."
	presignOp.Middlewares = huma.Middlewares{requestInfoMW}
	huma.Register(api, presignOp, func(ctx context.Context, in *presignIn) (*presignOut, error) {
		p := principalFrom(ctx)
		method := in.Body.Method
		if method == "" {
			method = http.MethodGet
		}
		need := auth.AccessRead
		if method == http.MethodPut {
			need = auth.AccessWrite
		}
		b, err := s.bucketFor(ctx, p, in.Bucket, need)
		if err != nil {
			return nil, toProblem(ctx, err)
		}
		hctx, _ := ctx.Value(keyHTTP{}).(huma.Context)
		if hctx == nil {
			return nil, toProblem(ctx, errors.New("api: no request for presign"))
		}
		scheme, host := s.requestBase(hctx.TLS() != nil, hctx.RemoteAddr(), hctx.Header("X-Forwarded-Proto"), hctx.Host())
		url, expiresAt, err := s.presignURL(ctx, p, scheme, host, b.Name, in.Body.Key, method, time.Duration(in.Body.ExpiresIn)*time.Second)
		if err != nil {
			return nil, toProblem(ctx, err)
		}
		out := &presignOut{}
		out.Body.URL = url
		out.Body.ExpiresAt = expiresAt
		out.Body.Method = method
		return out, nil
	})
}

var base64URL = base64.RawURLEncoding

// presignURL signs a GET or PUT URL for one object with the principal's
// access key. expires 0 means an hour.
func (s *Server) presignURL(ctx context.Context, p auth.Principal, scheme, host, bucket, key, method string, expires time.Duration) (string, time.Time, error) {
	if !engine.ValidKey(key) {
		return "", time.Time{}, apierr.New(apierr.InvalidKey, "an object key must be 1 to 1024 bytes of UTF-8 without control characters")
	}
	if expires == 0 {
		expires = time.Hour
	}
	if max := s.eng.Config().PresignMaxExpiry; expires < 0 || expires > max {
		return "", time.Time{}, apierr.Newf(apierr.BadRequest, "the expiry must be 1 to %d seconds", int(max.Seconds()))
	}
	rec, err := s.store.GetToken(ctx, p.TokenID)
	if err != nil {
		return "", time.Time{}, err
	}
	if len(rec.AccessSecret) == 0 {
		return "", time.Time{}, apierr.New(apierr.NoAccessKey, "this token has no access key; create one with access_key: true")
	}
	secret, err := s.eng.Sealer().Open(rec.ID, rec.AccessSecret)
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	url := s3proto.Presign(s3proto.PresignInput{Method: method, Scheme: scheme, Host: host, Path: S3Prefix + "/" + bucket + "/" + key,
		AccessKeyID: auth.AccessKeyID(rec.ID), Secret: secret, Region: "us-east-1", Time: now, Expires: expires})
	return url, now.Add(expires), nil
}

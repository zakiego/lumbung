package api

// MCP tools for buckets and objects (PLAN 16.3), with the same annotations
// and context-sized caps as the database tools.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
)

// mcpPutMax caps put_object content: a model sends it inline.
const mcpPutMax = 1 << 20

var overwrite = &mcp.ToolAnnotations{DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(false)}

type mcpBucket struct {
	Name        string   `json:"name"`
	PublicRead  bool     `json:"public_read"`
	CORSOrigins []string `json:"cors_origins,omitempty"`
	ObjectCount int64    `json:"object_count"`
	SizeBytes   int64    `json:"size_bytes"`
	CreatedAt   string   `json:"created_at"`
}

type mcpObject struct {
	Key          string            `json:"key"`
	Size         int64             `json:"size"`
	ETag         string            `json:"etag"`
	ContentType  string            `json:"content_type"`
	LastModified string            `json:"last_modified"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type objectIn struct {
	Bucket string `json:"bucket" jsonschema:"bucket name"`
	Key    string `json:"key" jsonschema:"object key"`
}

// textPrefix returns the longest prefix of b that is valid UTF-8 when only
// an incomplete character at the end (at most 3 bytes, where a read was
// cut) is invalid, and whether b is text at all. NUL bytes mean binary.
func textPrefix(b []byte, cut bool) ([]byte, bool) {
	if bytes.IndexByte(b, 0) >= 0 {
		return nil, false
	}
	if utf8.Valid(b) {
		return b, true
	}
	if cut {
		for n := 1; n <= 3 && n <= len(b); n++ {
			if p := b[:len(b)-n]; utf8.Valid(p) {
				return p, true
			}
		}
	}
	return nil, false
}

// skipContinuation moves an offset that lands inside a character forward
// to the next character's start, at most 3 bytes.
func skipContinuation(b []byte) int {
	n := 0
	for n < len(b) && n < 3 && !utf8.RuneStart(b[n]) {
		n++
	}
	return n
}

// fitJSON shortens text until its JSON encoding fits max bytes, on
// character boundaries: escapes can make JSON several times the raw size.
func fitJSON(text []byte, max int) []byte {
	for len(text) > 0 {
		enc, _ := json.Marshal(string(text))
		if len(enc) <= max {
			return text
		}
		cut := len(text) * max / len(enc)
		if cut >= len(text) {
			cut = len(text) - 1
		}
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	return text
}

func (s *Server) addObjectTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "list_buckets", Description: "List the buckets this token can see, with object counts and sizes.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct {
			Buckets   []mcpBucket `json:"buckets"`
			Truncated bool        `json:"truncated"`
			Note      string      `json:"note,omitempty"`
		}, error) {
			var out struct {
				Buckets   []mcpBucket `json:"buckets"`
				Truncated bool        `json:"truncated"`
				Note      string      `json:"note,omitempty"`
			}
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, out, err
			}
			all, err := s.eng.ListBuckets(ctx)
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			list := []mcpBucket{}
			for _, b := range all {
				if p.SeesBucket(b.ID) {
					list = append(list, mcpBucket{b.Name, b.PublicRead, b.CORSOrigins, b.ObjectCount, b.SizeBytes, timeStr(b.CreatedAt)})
				}
			}
			out.Buckets, out.Truncated = capList(list)
			out.Note = listNote(out.Truncated, len(out.Buckets), "")
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "create_bucket", Description: "Create an empty bucket. Needs an instance admin token. Names: 3 to 63 lowercase letters, digits and hyphens.", Annotations: additive},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Name string `json:"name" jsonschema:"3 to 63 lowercase letters, digits and hyphens, starting and ending with a letter or digit"`
		}) (*mcp.CallToolResult, mcpBucket, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, mcpBucket{}, err
			}
			var out mcpBucket
			err = s.audited(req, "bucket.create", func() (string, error) {
				if err := requireInstanceAdmin(p); err != nil {
					return "", err
				}
				b, err := s.eng.CreateBucket(ctx, in.Name)
				if err != nil {
					return "", err
				}
				out = mcpBucket{Name: b.Name, CreatedAt: timeStr(b.CreatedAt)}
				return b.ID, nil
			})
			if err != nil {
				return nil, mcpBucket{}, s.toolErr(req, err)
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "list_objects", Description: "List objects in a bucket in key order. With delimiter \"/\", keys below the prefix roll up into folder-like prefixes. Pass the returned cursor to see more.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Bucket    string `json:"bucket" jsonschema:"bucket name"`
			Prefix    string `json:"prefix,omitempty" jsonschema:"only keys starting with this, e.g. photos/2026/"`
			Delimiter string `json:"delimiter,omitempty" jsonschema:"usually /, to list one folder level"`
			Cursor    string `json:"cursor,omitempty" jsonschema:"the cursor from the previous call"`
		}) (*mcp.CallToolResult, struct {
			Objects  []mcpObject `json:"objects"`
			Prefixes []string    `json:"prefixes"`
			Cursor   string      `json:"cursor,omitempty"`
			Note     string      `json:"note,omitempty"`
		}, error) {
			var out struct {
				Objects  []mcpObject `json:"objects"`
				Prefixes []string    `json:"prefixes"`
				Cursor   string      `json:"cursor,omitempty"`
				Note     string      `json:"note,omitempty"`
			}
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, out, err
			}
			b, err := s.bucketFor(ctx, p, in.Bucket, auth.AccessRead)
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			after := ""
			if in.Cursor != "" {
				raw, err := base64URL.DecodeString(in.Cursor)
				if err != nil {
					return nil, out, s.toolErr(req, apierr.New(apierr.BadRequest, "invalid cursor"))
				}
				after = string(raw)
			}
			res, err := s.eng.ListObjects(ctx, engine.ListObjectsInput{BucketID: b.ID, Prefix: in.Prefix, Delimiter: in.Delimiter, After: after, Max: mcpListMax})
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			list := make([]mcpObject, 0, len(res.Objects))
			for _, o := range res.Objects {
				list = append(list, mcpObject{o.Key, o.Size, o.ETag, o.ContentType, timeStr(o.ModifiedAt), o.Metadata})
			}
			var cut bool
			out.Objects, cut = capList(list)
			out.Prefixes = res.Prefixes
			next := res.Next
			if cut && len(out.Objects) > 0 {
				// Resume after the last object shown. Prefixes sort among the
				// keys, so a prefix past it comes back on the next page.
				next = out.Objects[len(out.Objects)-1].Key
				kept := []string{}
				for _, pfx := range res.Prefixes {
					if pfx <= next {
						kept = append(kept, pfx)
					}
				}
				out.Prefixes = kept
			}
			if res.Truncated || cut {
				out.Cursor = base64URL.EncodeToString([]byte(next))
				out.Note = "More objects follow: call list_objects again with this cursor."
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "get_object", Description: "Read an object. Text comes back as text, up to about 64 KB from offset; for binary content only the metadata comes back (use presign_object for a download URL).", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Bucket string `json:"bucket" jsonschema:"bucket name"`
			Key    string `json:"key" jsonschema:"object key"`
			Offset int64  `json:"offset,omitempty" jsonschema:"byte to start reading at, for long text"`
		}) (*mcp.CallToolResult, struct {
			mcpObject
			Text      string `json:"text,omitempty"`
			Binary    bool   `json:"binary"`
			Truncated bool   `json:"truncated"`
			Note      string `json:"note,omitempty"`
		}, error) {
			var out struct {
				mcpObject
				Text      string `json:"text,omitempty"`
				Binary    bool   `json:"binary"`
				Truncated bool   `json:"truncated"`
				Note      string `json:"note,omitempty"`
			}
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, out, err
			}
			b, err := s.bucketFor(ctx, p, in.Bucket, auth.AccessRead)
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			o, f, err := s.eng.OpenObject(ctx, b.ID, in.Key)
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			defer f.Close()
			out.mcpObject = mcpObject{o.Key, o.Size, o.ETag, o.ContentType, timeStr(o.ModifiedAt), o.Metadata}
			if in.Offset < 0 || (in.Offset > 0 && in.Offset >= o.Size) {
				return nil, out, s.toolErr(req, apierr.Newf(apierr.InvalidRange, "offset must be under the object's %d bytes", o.Size))
			}
			buf := make([]byte, mcpMaxBytes)
			n, err := f.ReadAt(buf, in.Offset)
			if err != nil && err != io.EOF {
				return nil, out, s.toolErr(req, err)
			}
			buf = buf[:n]
			start := in.Offset
			if start > 0 {
				skip := skipContinuation(buf)
				buf, start = buf[skip:], start+int64(skip)
			}
			text, ok := textPrefix(buf, start+int64(len(buf)) < o.Size)
			if !ok {
				out.Binary = true
				out.Note = "Binary content isn't shown. Use presign_object for a download URL."
				return nil, out, nil
			}
			text = fitJSON(text, mcpMaxBytes)
			end := start + int64(len(text))
			if out.Truncated = end < o.Size; out.Truncated {
				out.Note = fmt.Sprintf("Only bytes %d to %d of %d are shown. Call again with offset %d for more.", start, end, o.Size, end)
			}
			out.Text = string(text)
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "put_object", Description: "Write an object from text or base64, at most 1 MB. Replaces an object with the same key. Needs a write token.", Annotations: overwrite},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Bucket        string            `json:"bucket" jsonschema:"bucket name"`
			Key           string            `json:"key" jsonschema:"object key"`
			Text          string            `json:"text,omitempty" jsonschema:"the content as text (UTF-8)"`
			ContentBase64 string            `json:"content_base64,omitempty" jsonschema:"the content as standard base64, for binary data; not with text"`
			ContentType   string            `json:"content_type,omitempty" jsonschema:"e.g. text/markdown; default text/plain for text, application/octet-stream for base64"`
			Metadata      map[string]string `json:"metadata,omitempty" jsonschema:"custom metadata, lowercase names"`
		}) (*mcp.CallToolResult, mcpObject, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, mcpObject{}, err
			}
			b, err := s.bucketFor(ctx, p, in.Bucket, auth.AccessWrite)
			if err != nil {
				return nil, mcpObject{}, s.toolErr(req, err)
			}
			data := []byte(in.Text)
			ct := in.ContentType
			if in.ContentBase64 != "" {
				if in.Text != "" {
					return nil, mcpObject{}, s.toolErr(req, apierr.New(apierr.BadRequest, "send text or content_base64, not both"))
				}
				if data, err = base64.StdEncoding.DecodeString(in.ContentBase64); err != nil {
					return nil, mcpObject{}, s.toolErr(req, apierr.New(apierr.BadRequest, "content_base64 isn't valid base64"))
				}
				if ct == "" {
					ct = "application/octet-stream"
				}
			} else if ct == "" {
				ct = "text/plain; charset=utf-8"
			}
			if len(data) > mcpPutMax {
				return nil, mcpObject{}, s.toolErr(req, apierr.Newf(apierr.ObjectTooLarge, "put_object takes at most %d bytes; use the S3 API or a presigned PUT URL for more", mcpPutMax))
			}
			o, err := s.eng.PutObject(ctx, engine.PutObjectInput{BucketID: b.ID, Key: in.Key, Size: int64(len(data)), Body: bytes.NewReader(data),
				Attrs: engine.ObjectAttrs{ContentType: ct, Metadata: in.Metadata}})
			if err != nil {
				return nil, mcpObject{}, s.toolErr(req, err)
			}
			return nil, mcpObject{o.Key, o.Size, o.ETag, o.ContentType, timeStr(o.ModifiedAt), o.Metadata}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "delete_object", Description: "Delete an object. This can't be undone. Deleting a key that doesn't exist succeeds.", Annotations: idempotentD},
		func(ctx context.Context, req *mcp.CallToolRequest, in objectIn) (*mcp.CallToolResult, struct {
			Deleted bool `json:"deleted" jsonschema:"false when there was no object with that key"`
		}, error) {
			var out struct {
				Deleted bool `json:"deleted" jsonschema:"false when there was no object with that key"`
			}
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, out, err
			}
			b, err := s.bucketFor(ctx, p, in.Bucket, auth.AccessWrite)
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			if out.Deleted, err = s.eng.DeleteObject(ctx, b.ID, in.Key); err != nil {
				return nil, out, s.toolErr(req, err)
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "presign_object", Description: "Make a URL anyone can use to download (GET) or upload (PUT) one object until it expires. Needs a token with an access key; PUT needs write access.", Annotations: additive},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Bucket           string `json:"bucket" jsonschema:"bucket name"`
			Key              string `json:"key" jsonschema:"object key"`
			Method           string `json:"method,omitempty" jsonschema:"GET (default) or PUT"`
			ExpiresInSeconds int    `json:"expires_in_seconds,omitempty" jsonschema:"how long the URL works; default 3600"`
		}) (*mcp.CallToolResult, struct {
			URL       string `json:"url"`
			Method    string `json:"method"`
			ExpiresAt string `json:"expires_at"`
		}, error) {
			var out struct {
				URL       string `json:"url"`
				Method    string `json:"method"`
				ExpiresAt string `json:"expires_at"`
			}
			c, err := mcpCallOf(req)
			if err != nil {
				return nil, out, err
			}
			method := strings.ToUpper(in.Method)
			if method == "" {
				method = http.MethodGet
			}
			need := auth.AccessRead
			switch method {
			case http.MethodGet:
			case http.MethodPut:
				need = auth.AccessWrite
			default:
				return nil, out, s.toolErr(req, apierr.New(apierr.BadRequest, "method must be GET or PUT"))
			}
			b, err := s.bucketFor(ctx, c.p, in.Bucket, need)
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			url, expiresAt, err := s.presignURL(ctx, c.p, c.scheme, c.host, b.Name, in.Key, method, time.Duration(in.ExpiresInSeconds)*time.Second)
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			out.URL, out.Method, out.ExpiresAt = url, method, timeStr(expiresAt)
			return nil, out, nil
		})
}

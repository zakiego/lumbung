package cli

// Bucket and object commands (PLAN 16.3). Objects are named BUCKET/KEY.

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zakiego/lumbung/apps/engine/internal/client"
)

func bucketPath(b string) string { return "/v1/buckets/" + url.PathEscape(b) }

// splitObject reads BUCKET/KEY. With keyOptional, BUCKET alone or BUCKET/
// gives an empty key.
func splitObject(arg string, keyOptional bool) (bucket, key string, err error) {
	bucket, key, _ = strings.Cut(arg, "/")
	if bucket == "" || (key == "" && !keyOptional) {
		return "", "", fmt.Errorf("%q: name an object as BUCKET/KEY", arg)
	}
	return bucket, key, nil
}

func (a *app) bucketCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "bucket", Short: "List, create, change and delete buckets"}
	cmd.AddCommand(&cobra.Command{
		Use: "list", Short: "List buckets", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := a.client()
			if err != nil {
				return err
			}
			bs, err := client.List[client.Bucket](cmd.Context(), c, "/v1/buckets")
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(bs)
			}
			rows := [][]string{}
			for _, b := range bs {
				public := "private"
				if b.PublicRead {
					public = "public read"
				}
				rows = append(rows, []string{b.Name, strconv.FormatInt(b.ObjectCount, 10), humanBytes(b.SizeBytes), public, ts(b.CreatedAt)})
			}
			a.table([]string{"NAME", "OBJECTS", "SIZE", "ACCESS", "CREATED"}, rows)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "create NAME", Short: "Create a bucket (needs an instance admin token)", Args: cobra.ExactArgs(1),
		Example: "  lumbung bucket create photos",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := a.client()
			if err != nil {
				return err
			}
			var b client.Bucket
			if err := c.Do(cmd.Context(), http.MethodPost, "/v1/buckets", nil, map[string]string{"name": args[0]}, &b); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(b)
			}
			fmt.Fprintf(a.out, "Created bucket %s\n", b.Name)
			return nil
		},
	})
	var public, private bool
	var cors []string
	var rename string
	update := &cobra.Command{
		Use: "update NAME", Short: "Change a bucket's name, public read access or CORS origins", Args: cobra.ExactArgs(1),
		Example: "  lumbung bucket update photos --public\n  lumbung bucket update photos --cors https://app.example.com",
		RunE: func(cmd *cobra.Command, args []string) error {
			if public && private {
				return errors.New("pass --public or --private, not both")
			}
			body := map[string]any{}
			if public || private {
				body["public_read"] = public
			}
			if cmd.Flags().Changed("cors") {
				if cors == nil {
					cors = []string{}
				}
				body["cors_origins"] = cors
			}
			if rename != "" {
				body["name"] = rename
			}
			if len(body) == 0 {
				return errors.New("nothing to change: pass --public, --private, --cors or --name")
			}
			c, _, err := a.client()
			if err != nil {
				return err
			}
			var b client.Bucket
			if err := c.Do(cmd.Context(), http.MethodPatch, bucketPath(args[0]), nil, body, &b); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(b)
			}
			access := "private"
			if b.PublicRead {
				access = "public read"
			}
			fmt.Fprintf(a.out, "Updated bucket %s: %s, CORS origins %v\n", b.Name, access, b.CORSOrigins)
			return nil
		},
	}
	update.Flags().BoolVar(&public, "public", false, "let anyone read objects without a token")
	update.Flags().BoolVar(&private, "private", false, "require a token to read objects")
	update.Flags().StringSliceVar(&cors, "cors", nil, "origins a browser may call the S3 API from (comma-separated; empty clears)")
	update.Flags().StringVar(&rename, "name", "", "rename the bucket")
	cmd.AddCommand(update, &cobra.Command{
		Use: "delete NAME", Short: "Delete an empty bucket", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := a.client()
			if err != nil {
				return err
			}
			if err := a.confirm("delete bucket "+args[0], args[0]); err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), http.MethodDelete, bucketPath(args[0]), nil, nil, nil); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(map[string]string{"deleted": args[0]})
			}
			fmt.Fprintf(a.out, "Deleted bucket %s\n", args[0])
			return nil
		},
	})
	return cmd
}

func (a *app) objectCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "object", Short: "List, upload, download, copy, delete and presign objects"}
	cmd.AddCommand(a.objectLsCmd(), a.objectPutCmd(), a.objectGetCmd(), a.objectRmCmd(), a.objectCpCmd(), a.objectPresignCmd())
	return cmd
}

func (a *app) objectLsCmd() *cobra.Command {
	var recursive bool
	var limit int
	cmd := &cobra.Command{
		Use: "ls BUCKET[/PREFIX]", Short: "List objects, with folders from /", Args: cobra.ExactArgs(1),
		Example: "  lumbung object ls photos\n  lumbung object ls photos/2026/ --recursive",
		RunE: func(cmd *cobra.Command, args []string) error {
			bucket, prefix, err := splitObject(args[0], true)
			if err != nil {
				return err
			}
			c, _, err := a.client()
			if err != nil {
				return err
			}
			q := url.Values{"prefix": {prefix}, "limit": {"1000"}}
			if !recursive {
				q.Set("delimiter", "/")
			}
			var objects []client.Object
			var prefixes []string
			for {
				var page client.ObjectPage
				if err := c.Do(cmd.Context(), http.MethodGet, bucketPath(bucket)+"/objects", q, nil, &page); err != nil {
					return err
				}
				objects = append(objects, page.Data...)
				prefixes = append(prefixes, page.Prefixes...)
				if page.NextCursor == "" || (limit > 0 && len(objects)+len(prefixes) >= limit) {
					break
				}
				q.Set("cursor", page.NextCursor)
			}
			if a.json {
				return a.printJSON(map[string]any{"objects": objects, "prefixes": prefixes})
			}
			rows := [][]string{}
			for _, p := range prefixes {
				rows = append(rows, []string{"", "", p})
			}
			for _, o := range objects {
				rows = append(rows, []string{ts(o.LastModified), humanBytes(o.Size), o.Key})
			}
			a.table([]string{"MODIFIED", "SIZE", "KEY"}, rows)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "list every key under the prefix instead of rolling folders up")
	cmd.Flags().IntVar(&limit, "limit", 0, "stop after about this many entries (0: all)")
	return cmd
}

// contentTypeOf guesses a file's content type from its extension, then its
// first bytes.
func contentTypeOf(path string, f *os.File) string {
	if t := mime.TypeByExtension(filepath.Ext(path)); t != "" {
		return t
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	_, _ = f.Seek(0, io.SeekStart)
	return http.DetectContentType(head[:n])
}

func (a *app) objectPutCmd() *cobra.Command {
	var contentType, cacheControl string
	var metadata []string
	cmd := &cobra.Command{
		Use: "put FILE BUCKET/KEY", Short: "Upload a file as an object", Args: cobra.ExactArgs(2),
		Long:    "Uploads FILE. When the destination ends with / or names only the bucket, the file's name is appended as the key.",
		Example: "  lumbung object put cat.jpg photos/2026/\n  lumbung object put report.pdf docs/q3.pdf --meta owner=finance",
		RunE: func(cmd *cobra.Command, args []string) error {
			bucket, key, err := splitObject(args[1], true)
			if err != nil {
				return err
			}
			if key == "" || strings.HasSuffix(key, "/") {
				key += filepath.Base(args[0])
			}
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			fi, err := f.Stat()
			if err != nil {
				return err
			}
			if !fi.Mode().IsRegular() {
				return fmt.Errorf("%s isn't a regular file", args[0])
			}
			if contentType == "" {
				contentType = contentTypeOf(args[0], f)
			}
			hdr := http.Header{}
			if cacheControl != "" {
				hdr.Set("Cache-Control", cacheControl)
			}
			for _, m := range metadata {
				k, v, ok := strings.Cut(m, "=")
				if !ok || k == "" {
					return fmt.Errorf("--meta %q: use name=value", m)
				}
				hdr.Set("X-Amz-Meta-"+k, v)
			}
			c, _, err := a.client()
			if err != nil {
				return err
			}
			var o client.Object
			if err := c.Upload(cmd.Context(), bucketPath(bucket)+"/object", url.Values{"key": {key}}, f, fi.Size(), contentType, hdr, &o); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(o)
			}
			fmt.Fprintf(a.out, "Uploaded %s to %s/%s (%s)\n", args[0], bucket, o.Key, humanBytes(o.Size))
			return nil
		},
	}
	cmd.Flags().StringVar(&contentType, "content-type", "", "the object's content type (default: guessed from the file)")
	cmd.Flags().StringVar(&cacheControl, "cache-control", "", "a Cache-Control header to store with the object")
	cmd.Flags().StringArrayVar(&metadata, "meta", nil, "custom metadata as name=value (repeatable)")
	return cmd
}

func (a *app) objectGetCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use: "get BUCKET/KEY", Short: "Download an object", Args: cobra.ExactArgs(1),
		Example: "  lumbung object get photos/2026/cat.jpg -o cat.jpg\n  lumbung object get docs/notes.txt",
		RunE: func(cmd *cobra.Command, args []string) error {
			bucket, key, err := splitObject(args[0], false)
			if err != nil {
				return err
			}
			if a.json && (output == "" || output == "-") {
				return errors.New("--json needs -o FILE: the download itself goes to standard output otherwise")
			}
			c, _, err := a.client()
			if err != nil {
				return err
			}
			res, err := c.Raw(cmd.Context(), http.MethodGet, bucketPath(bucket)+"/object", url.Values{"key": {key}}, nil, "")
			if err != nil {
				return err
			}
			defer res.Body.Close()
			if output == "" || output == "-" {
				_, err = io.Copy(a.out, res.Body)
				return err
			}
			tmp := output + ".partial"
			f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			n, err := io.Copy(f, res.Body)
			if err != nil {
				f.Close()
				os.Remove(tmp)
				return fmt.Errorf("download cut off: %w", err)
			}
			if err := f.Close(); err != nil {
				return err
			}
			if err := os.Rename(tmp, output); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(map[string]any{"saved": output, "bytes": n, "etag": strings.Trim(res.Header.Get("ETag"), `"`)})
			}
			fmt.Fprintf(a.errOut, "Saved %s (%s)\n", output, humanBytes(n))
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to this file instead of standard output")
	return cmd
}

func (a *app) objectRmCmd() *cobra.Command {
	return &cobra.Command{
		Use: "rm BUCKET/KEY", Short: "Delete an object", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bucket, key, err := splitObject(args[0], false)
			if err != nil {
				return err
			}
			c, _, err := a.client()
			if err != nil {
				return err
			}
			if err := a.confirm("delete "+args[0], key); err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), http.MethodDelete, bucketPath(bucket)+"/object", url.Values{"key": {key}}, nil, nil); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(map[string]string{"deleted": args[0]})
			}
			fmt.Fprintf(a.out, "Deleted %s\n", args[0])
			return nil
		},
	}
}

func (a *app) objectCpCmd() *cobra.Command {
	return &cobra.Command{
		Use: "cp SRC_BUCKET/KEY DST_BUCKET/KEY", Short: "Copy an object, within a bucket or to another", Args: cobra.ExactArgs(2),
		Example: "  lumbung object cp photos/cat.jpg backups/2026/cat.jpg",
		RunE: func(cmd *cobra.Command, args []string) error {
			sb, sk, err := splitObject(args[0], false)
			if err != nil {
				return err
			}
			db, dk, err := splitObject(args[1], true)
			if err != nil {
				return err
			}
			if dk == "" || strings.HasSuffix(dk, "/") {
				dk += filepath.Base(sk)
			}
			c, _, err := a.client()
			if err != nil {
				return err
			}
			var o client.Object
			if err := c.Do(cmd.Context(), http.MethodPost, bucketPath(db)+"/copy", nil, map[string]string{"from_bucket": sb, "from_key": sk, "key": dk}, &o); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(o)
			}
			fmt.Fprintf(a.out, "Copied %s to %s/%s\n", args[0], db, o.Key)
			return nil
		},
	}
}

func (a *app) objectPresignCmd() *cobra.Command {
	var method string
	var expires time.Duration
	cmd := &cobra.Command{
		Use: "presign BUCKET/KEY", Short: "Print a presigned S3 URL for one object", Args: cobra.ExactArgs(1),
		Long:    "Prints a URL anyone can use to GET (or PUT) one object until it expires. It is signed with the token's access key, so the token must have one (`lumbung token create --access-key`).",
		Example: "  lumbung object presign photos/cat.jpg --expires 1h\n  lumbung object presign uploads/new.bin --method PUT",
		RunE: func(cmd *cobra.Command, args []string) error {
			bucket, key, err := splitObject(args[0], false)
			if err != nil {
				return err
			}
			method = strings.ToUpper(method)
			if method != http.MethodGet && method != http.MethodPut {
				return errors.New("--method must be GET or PUT")
			}
			c, _, err := a.client()
			if err != nil {
				return err
			}
			var out struct {
				URL       string    `json:"url"`
				Method    string    `json:"method"`
				ExpiresAt time.Time `json:"expires_at"`
			}
			body := map[string]any{"key": key, "method": method, "expires_in": int(expires / time.Second)}
			if err := c.Do(cmd.Context(), http.MethodPost, bucketPath(bucket)+"/presign", nil, body, &out); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(out)
			}
			fmt.Fprintln(a.out, out.URL)
			return nil
		},
	}
	cmd.Flags().StringVar(&method, "method", "GET", "GET to download or PUT to upload")
	cmd.Flags().DurationVar(&expires, "expires", time.Hour, "how long the URL stays valid")
	return cmd
}

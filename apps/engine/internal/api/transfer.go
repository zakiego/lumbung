package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
)

type keyBody struct{}

type importIn struct {
	BranchPath
	Format string `query:"format" enum:"db,sql" default:"db" doc:"db: a SQLite database file. sql: a SQL dump."`
	Mode   string `query:"mode" enum:"empty,replace" default:"empty" doc:"empty refuses a branch that already has tables. replace throws the branch's content away (not on a protected branch)."`
}

type importOut struct {
	Body BranchOut
}

type exportIn struct {
	BranchPath
	Format string `query:"format" enum:"db,sql" default:"db" doc:"db: a consistent SQLite file. sql: a SQL dump."`
}

// streamBody hands the raw request body to the operation, which reads it
// itself instead of letting Huma buffer it (imports can be large). Reads
// carry a deadline renewed each time bytes arrive, so a stalled upload is cut
// off instead of holding the import open.
func streamBody(ctx huma.Context, next func(huma.Context)) {
	r := &idleReader{r: ctx.BodyReader(), set: ctx.SetReadDeadline, idle: uploadIdle}
	next(huma.WithValue(ctx, keyBody{}, io.Reader(r)))
}

// uploadIdle is how long an upload may send nothing before it is cut off.
var uploadIdle = time.Minute

type idleReader struct {
	r    io.Reader
	set  func(time.Time) error
	idle time.Duration
}

func (r *idleReader) Read(p []byte) (int, error) {
	_ = r.set(time.Now().Add(r.idle))
	return r.r.Read(p)
}

func (s *Server) registerTransfer(api huma.API) {
	o := op("import", http.MethodPost, "/v1/databases/{db}/branches/{branch}/import", "Import a database file or SQL dump into a branch", "Import and download", "branch.import")
	o.Description = "Send the file as the raw request body. It is built into a new file and checked first: the header, PRAGMA integrity_check, the branch size limit and the same isolation rules as user SQL. The branch only changes when everything passed."
	o.RequestBody = &huma.RequestBody{
		Required: true,
		Content: map[string]*huma.MediaType{
			"application/octet-stream": {Schema: &huma.Schema{Type: "string", Format: "binary"}},
			"application/sql":          {Schema: &huma.Schema{Type: "string"}},
		},
	}
	o.MaxBodyBytes = -1
	o.BodyReadTimeout = -1
	o.Middlewares = huma.Middlewares{streamBody}
	huma.Register(api, o, func(ctx context.Context, in *importIn) (*importOut, error) {
		p := principalFrom(ctx)
		need := auth.AccessWrite
		if in.Mode == "replace" {
			need = auth.AccessAdmin
		}
		d, b, err := s.branch(ctx, p, in.DB, in.Branch, need)
		if err != nil {
			return nil, toProblem(ctx, err)
		}
		setTarget(ctx, d.ID+"/"+b.ID)
		body, _ := ctx.Value(keyBody{}).(io.Reader)
		if body == nil {
			return nil, toProblem(ctx, apierr.New(apierr.BadRequest, "send the file as the request body"))
		}
		format := engine.ImportDB
		if in.Format == "sql" {
			format = engine.ImportSQL
		}
		mode := engine.ImportIntoEmpty
		if in.Mode == "replace" {
			mode = engine.ImportReplace
		}
		if err := s.eng.Import(ctx, engine.Target{Workspace: d.WorkspaceID, Database: d.ID, Branch: b.ID}, format, mode, body); err != nil {
			return nil, toProblem(ctx, err)
		}
		b, err = s.eng.GetBranch(ctx, d.WorkspaceID, d.ID, b.ID)
		if err != nil {
			return nil, toProblem(ctx, err)
		}
		return &importOut{Body: s.branchOut(d, b)}, nil
	})

	x := op("download", http.MethodGet, "/v1/databases/{db}/branches/{branch}/export", "Download a branch as a SQLite file or SQL dump", "Import and download", "")
	x.Responses = map[string]*huma.Response{
		"200": {
			Description: "The file.",
			Content: map[string]*huma.MediaType{
				"application/vnd.sqlite3": {Schema: &huma.Schema{Type: "string", Format: "binary"}},
				"application/sql":         {Schema: &huma.Schema{Type: "string"}},
			},
		},
	}
	huma.Register(api, x, func(ctx context.Context, in *exportIn) (*huma.StreamResponse, error) {
		d, b, err := s.branch(ctx, principalFrom(ctx), in.DB, in.Branch, auth.AccessRead)
		if err != nil {
			return nil, toProblem(ctx, err)
		}
		t := engine.Target{Workspace: d.WorkspaceID, Database: d.ID, Branch: b.ID}
		if in.Format == "sql" {
			// Fail before streaming when the dump can't be made at all.
			if err := s.eng.SQLDumpable(ctx, t); err != nil {
				return nil, toProblem(ctx, err)
			}
		}
		stamp := time.Now().UTC().Format("20060102-150405")
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			name := fmt.Sprintf("%s-%s-%s.%s", d.Name, b.Name, stamp, map[string]string{"db": "db", "sql": "sql"}[in.Format])
			hctx.SetHeader("Content-Disposition", `attachment; filename="`+name+`"`)
			w := hctx.BodyWriter()
			var err error
			if in.Format == "sql" {
				hctx.SetHeader("Content-Type", "application/sql; charset=utf-8")
				err = s.eng.ExportSQL(hctx.Context(), t, w)
			} else {
				hctx.SetHeader("Content-Type", "application/vnd.sqlite3")
				err = s.eng.ExportDB(hctx.Context(), t, w)
			}
			if err != nil {
				// Headers are gone by now; the cut connection tells the client.
				s.log.Error("download", "err", err, "branch", b.ID)
				panic(http.ErrAbortHandler)
			}
		}}, nil
	})
}

package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/recovery"
)

type queryBody struct {
	StatementIn
	ReadOnly bool `json:"read_only,omitempty" doc:"Run on a read-only connection even if the token can write."`
}

type queryIn struct {
	DB     string `path:"db"`
	Branch string `path:"branch"`
	Body   queryBody
}

type defaultQueryIn struct {
	DB   string `path:"db"`
	Body queryBody
}

type batchBody struct {
	Statements []StatementIn `json:"statements" minItems:"1" maxItems:"1000" doc:"Run in order in one transaction: all take effect or none do."`
	ReadOnly   bool          `json:"read_only,omitempty" doc:"Run on a read-only connection even if the token can write."`
	Migration  bool          `json:"migration,omitempty" doc:"Turn foreign key enforcement off for the batch and check every foreign key once before committing. Use it for schema migrations: rebuilding a table (create, copy, drop, rename) would otherwise fire ON DELETE actions and delete child rows. Needs write access."`
}

type batchIn struct {
	DB     string `path:"db"`
	Branch string `path:"branch"`
	Body   batchBody
}

type defaultBatchIn struct {
	DB   string `path:"db"`
	Body batchBody
}

type queryOut struct{ Body ResultOut }

type batchOut struct {
	Body struct {
		Results []ResultOut `json:"results"`
	}
}

// access picks the connection: the writer when the token can write and the
// request didn't ask for read-only.
func access(p auth.Principal, d meta.Database, branchID string, readOnly bool) engine.Access {
	if !readOnly && p.AtLeast(auth.AccessWrite, d, branchID) {
		return engine.Write
	}
	return engine.Read
}

func (s *Server) runQuery(ctx context.Context, dbRef, branchRef string, stmts []StatementIn, readOnly, single bool) ([]*engine.Result, error) {
	return s.runBatch(ctx, dbRef, branchRef, stmts, readOnly, single, false)
}

func (s *Server) runBatch(ctx context.Context, dbRef, branchRef string, stmts []StatementIn, readOnly, single, migration bool) ([]*engine.Result, error) {
	p := principalFrom(ctx)
	d, b, err := s.branch(ctx, p, dbRef, branchRef, auth.AccessRead)
	if err != nil {
		return nil, toProblem(ctx, err)
	}
	t := engine.Target{Workspace: d.WorkspaceID, Database: d.ID, Branch: b.ID}
	acc := access(p, d, b.ID, readOnly)
	start := time.Now()
	var results []*engine.Result
	if single {
		var r *engine.Result
		r, err = s.eng.Query(ctx, t, acc, stmts[0].engine())
		if err == nil {
			results = []*engine.Result{r}
		}
	} else {
		es := make([]engine.Statement, len(stmts))
		for i, st := range stmts {
			es[i] = st.engine()
		}
		if migration && acc != engine.Write {
			err = apierr.New(apierr.ReadOnly, "a migration needs a token that can write")
		} else {
			results, err = s.eng.BatchWith(ctx, t, acc, es, engine.BatchOptions{Migration: migration})
		}
	}
	s.record(b.ID, stmts, start, results, err)
	if err != nil {
		return nil, toProblem(ctx, err)
	}
	return results, nil
}

// record counts a request in the branch's stats and the slow-query log.
func (s *Server) record(branchID string, stmts []StatementIn, start time.Time, results []*engine.Result, err error) {
	s.recordWith(branchID, stmts, start, results, err, false)
}

// recordQueued is record with the slow-query row written by a background
// worker, for callers that may hold the branch's writer meanwhile. When the
// queue is full the row is dropped and logged.
func (s *Server) recordQueued(branchID string, stmts []StatementIn, start time.Time, results []*engine.Result, err error) {
	s.recordWith(branchID, stmts, start, results, err, true)
}

type slowEntry struct {
	branchID string
	q        meta.SlowQuery
}

// slowWorker writes queued slow-query rows until the server closes.
func (s *Server) slowWorker() {
	for {
		select {
		case e := <-s.slowQ:
			s.addSlow(e.branchID, e.q)
		case <-s.stop:
			return
		}
	}
}

func (s *Server) addSlow(branchID string, q meta.SlowQuery) {
	defer recovery.Log(s.log, "slow query log")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.AddSlowQuery(ctx, branchID, q, 50); err != nil {
		s.log.Error("slow query log", "err", err)
	}
}

func (s *Server) recordWith(branchID string, stmts []StatementIn, start time.Time, results []*engine.Result, err error, queued bool) {
	d := time.Since(start)
	var read, written int64
	for _, r := range results {
		read += int64(len(r.Rows))
		written += r.RowsAffected
	}
	s.stats.Record(branchID, start, d, read, written, err != nil)
	if d < s.eng.Config().SlowQuery {
		return
	}
	sql := stmts[0].SQL
	if len(stmts) > 1 {
		sql = "-- batch of " + strconv.Itoa(len(stmts)) + " statements, first:\n" + sql
	}
	if len(sql) > 2000 {
		sql = sql[:2000] + "..."
	}
	code := ""
	if err != nil {
		code = apierr.As(err).Code
	}
	q := meta.SlowQuery{At: start, DurationMS: d.Milliseconds(), SQL: sql, ErrorCode: code}
	if !queued {
		s.addSlow(branchID, q)
		return
	}
	select {
	case s.slowQ <- slowEntry{branchID, q}:
	default:
		s.log.Warn("slow query log queue full, dropping a row", "branch_id", branchID)
	}
}

func (s *Server) registerQuery(api huma.API) {
	queryOp := func(id, path, summary string) huma.Operation {
		o := op(id, http.MethodPost, path, summary, "Query", "")
		o.MaxBodyBytes = s.eng.Config().MaxRequestBytes
		o.Description = "Runs one statement with bound parameters in its own transaction. A token that can write runs it on the branch's writer; a read-only token runs it on a read-only connection, where SQLite refuses writes."
		return o
	}
	batchOp := func(id, path, summary string) huma.Operation {
		o := op(id, http.MethodPost, path, summary, "Query", "")
		o.MaxBodyBytes = s.eng.Config().MaxRequestBytes
		o.Description = "Runs statements in order in one transaction. If any fails, none take effect."
		return o
	}
	huma.Register(api, queryOp("query", "/v1/databases/{db}/branches/{branch}/query", "Run a statement on a branch"),
		func(ctx context.Context, in *queryIn) (*queryOut, error) {
			r, err := s.runQuery(ctx, in.DB, in.Branch, []StatementIn{in.Body.StatementIn}, in.Body.ReadOnly, true)
			if err != nil {
				return nil, err
			}
			return &queryOut{Body: resultOut(r[0])}, nil
		})
	huma.Register(api, queryOp("query-default", "/v1/databases/{db}/query", "Run a statement on the default branch"),
		func(ctx context.Context, in *defaultQueryIn) (*queryOut, error) {
			r, err := s.runQuery(ctx, in.DB, "", []StatementIn{in.Body.StatementIn}, in.Body.ReadOnly, true)
			if err != nil {
				return nil, err
			}
			return &queryOut{Body: resultOut(r[0])}, nil
		})
	huma.Register(api, batchOp("batch", "/v1/databases/{db}/branches/{branch}/batch", "Run statements on a branch in one transaction"),
		func(ctx context.Context, in *batchIn) (*batchOut, error) {
			rs, err := s.runBatch(ctx, in.DB, in.Branch, in.Body.Statements, in.Body.ReadOnly, false, in.Body.Migration)
			if err != nil {
				return nil, err
			}
			return batchResults(rs), nil
		})
	huma.Register(api, batchOp("batch-default", "/v1/databases/{db}/batch", "Run statements on the default branch in one transaction"),
		func(ctx context.Context, in *defaultBatchIn) (*batchOut, error) {
			rs, err := s.runBatch(ctx, in.DB, "", in.Body.Statements, in.Body.ReadOnly, false, in.Body.Migration)
			if err != nil {
				return nil, err
			}
			return batchResults(rs), nil
		})
}

func batchResults(rs []*engine.Result) *batchOut {
	out := &batchOut{}
	out.Body.Results = make([]ResultOut, len(rs))
	for i, r := range rs {
		out.Body.Results[i] = resultOut(r)
	}
	return out
}

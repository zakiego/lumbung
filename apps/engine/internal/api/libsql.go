package api

// The libSQL remote protocol over HTTP (ADR 0005): Hrana v2 and v3 with JSON
// at /libsql/{db}/{branch}/, so @libsql/client and Drizzle's libsql driver
// work against a branch. Same tokens, scopes, limits and isolation as REST.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/recovery"
	"github.com/zakiego/lumbung/apps/engine/internal/sqlsplit"
	"github.com/zakiego/lumbung/apps/engine/internal/value"
)

const (
	hranaMaxRequests  = 1000 // requests in one pipeline
	hranaMaxSteps     = 1000 // steps in one batch
	hranaMaxStoredSQL = 128  // stored SQL texts per stream
	hranaMaxCondDepth = 64   // nesting of batch conditions
)

// mountLibSQL adds the /libsql routes.
func (s *Server) mountLibSQL() {
	s.libsql = &libsqlStreams{log: s.log, m: map[string]*libsqlStream{}, perToken: map[string]int{}}
	for _, v := range []string{"v2", "v3"} {
		s.mux.HandleFunc("GET /libsql/{db}/{branch}/"+v, libsqlVersion)
		s.mux.Handle("POST /libsql/{db}/{branch}/"+v+"/pipeline", s.libsqlHandler(false))
		s.mux.HandleFunc("POST /libsql/{db}/"+v+"/pipeline", libsqlNoBranch)
	}
	s.mux.Handle("POST /libsql/{db}/{branch}/v3/cursor", s.libsqlHandler(true))
	s.mux.HandleFunc("POST /libsql/{db}/v3/cursor", libsqlNoBranch)
}

// Close closes every libSQL stream, rolling back open transactions. Call it
// before closing the engine.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.Drain()
		s.libsql.closeAll()
		close(s.stop)
	})
}

// libsqlVersion answers the protocol's version check. It needs no token and
// looks nothing up, so it tells a caller only that the version is served.
func libsqlVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// libsqlNoBranch catches a client URL without the branch or the trailing
// slash: the client resolves "v2/pipeline" against it and drops the last
// path segment.
func libsqlNoBranch(w http.ResponseWriter, _ *http.Request) {
	writeHranaHTTP(w, apierr.New(apierr.NotFound, "this URL names no branch: use http(s)://<host>/libsql/<database>/<branch>/ as the client URL, with the trailing slash"))
}

// writeHranaHTTP sends an error for the whole request in the protocol's
// Error shape. The client reads it only when the content type is exactly
// application/json.
func writeHranaHTTP(w http.ResponseWriter, e *apierr.Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(hError{Message: e.Message, Code: e.Code})
}

// hranaCall is one HTTP request on a stream.
type hranaCall struct {
	ctx       context.Context
	p         auth.Principal
	db        meta.Database // the database the stream is on, for access checks
	target    engine.Target // workspace, database and branch ids
	budget    *engine.Budget
	requestID string
}

func (s *Server) libsqlHandler(cursor bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, id, ae := s.gateRequest(w, r)
		if ae != nil {
			writeHranaHTTP(w, ae)
			return
		}
		ctx := r.Context()
		d, b, err := s.branch(ctx, p, r.PathValue("db"), r.PathValue("branch"), auth.AccessRead)
		if err != nil {
			writeHranaHTTP(w, s.publicErr(err, id))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, s.eng.Config().MaxRequestBytes)
		var baton *string
		var pipe hPipelineReq
		var cur hCursorReq
		if cursor {
			err = json.NewDecoder(r.Body).Decode(&cur)
			baton = cur.Baton
			if err == nil && cur.Batch == nil {
				err = errors.New("no batch")
			}
		} else {
			err = json.NewDecoder(r.Body).Decode(&pipe)
			baton = pipe.Baton
			if err == nil && len(pipe.Requests) > hranaMaxRequests {
				err = fmt.Errorf("a pipeline holds at most %d requests", hranaMaxRequests)
			}
		}
		if err != nil {
			var mb *http.MaxBytesError
			if errors.As(err, &mb) {
				writeHranaHTTP(w, apierr.Newf(apierr.RequestTooLarge, "the body is larger than %d bytes", mb.Limit))
				return
			}
			writeHranaHTTP(w, apierr.New(apierr.BadRequest, "the body isn't a valid request: "+err.Error()))
			return
		}

		var st *libsqlStream
		if baton == nil {
			st, ae = s.libsql.open(p.TokenID, streamOwner(p), b.ID, s.eng.Config())
		} else {
			st, ae = s.libsql.lookup(*baton, streamOwner(p), b.ID)
		}
		if ae != nil {
			writeHranaHTTP(w, ae)
			return
		}
		// lookup and open return the stream locked; finish unlocks it.
		if st.tx != nil && time.Now().After(st.txDeadline) {
			s.libsql.closeStream(st)
			st.mu.Unlock()
			writeHranaHTTP(w, apierr.New(apierr.InvalidBaton, "the transaction passed the transaction timeout and was rolled back"))
			return
		}
		c := &hranaCall{
			ctx: ctx, p: p, db: d, target: engine.Target{Workspace: d.WorkspaceID, Database: d.ID, Branch: b.ID},
			budget: s.eng.NewBudget(), requestID: id,
		}
		if cursor {
			res, berr := s.hranaBatch(c, st, cur.Batch)
			if berr != nil {
				// An HTTP error carries no baton, so the client loses the
				// stream: close it now rather than leave a transaction
				// holding the writer until the idle timeout.
				s.libsql.closeStream(st)
				st.mu.Unlock()
				writeHranaHTTP(w, s.publicErr(berr, id))
				return
			}
			next := s.libsql.finish(st, s.eng.Config())
			writeCursor(w, next, cur.Batch, res)
			return
		}
		out := hPipelineResp{Results: make([]hStreamResult, len(pipe.Requests))}
		for i := range pipe.Requests {
			resp, err := s.hranaRequest(c, st, &pipe.Requests[i])
			if err != nil {
				out.Results[i] = hStreamResult{Type: "error", Error: s.hranaErr(err, id)}
			} else {
				out.Results[i] = hStreamResult{Type: "ok", Response: resp}
			}
		}
		out.Baton = s.libsql.finish(st, s.eng.Config())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}

// publicErr is an error to show a client. Internal errors are logged with
// the request id and shown without detail.
func (s *Server) publicErr(err error, requestID string) *apierr.Error {
	ae := apierr.As(err)
	if ae.Code == apierr.Internal {
		s.log.Error("libsql", "err", err, "request_id", requestID)
		return apierr.New(apierr.Internal, "something failed inside the engine (request id "+requestID+")")
	}
	return ae
}

func (s *Server) hranaErr(err error, requestID string) *hError {
	ae := s.publicErr(err, requestID)
	msg := ae.Message
	if ae.Detail != "" {
		msg += ": " + ae.Detail
	}
	return &hError{Message: msg, Code: ae.Code}
}

func writeCursor(w http.ResponseWriter, baton *string, b *hBatch, res *hBatchResult) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	_ = enc.Encode(hCursorResp{Baton: baton})
	for i := range b.Steps {
		if e := res.StepErrors[i]; e != nil {
			_ = enc.Encode(hStepError{Type: "step_error", Step: i, Error: *e})
			continue
		}
		r := res.StepResults[i]
		if r == nil {
			continue // skipped by its condition
		}
		_ = enc.Encode(hStepBegin{Type: "step_begin", Step: i, Cols: r.Cols})
		for _, row := range r.Rows {
			_ = enc.Encode(hRow{Type: "row", Row: row})
		}
		_ = enc.Encode(hStepEnd{Type: "step_end", AffectedRowCount: r.AffectedRowCount, LastInsertRowID: r.LastInsertRowID})
	}
	_ = bw.Flush()
}

// hranaRequest runs one request of a pipeline.
func (s *Server) hranaRequest(c *hranaCall, st *libsqlStream, req *hStreamReq) (*hStreamResponse, error) {
	if st.closed {
		return nil, apierr.New(apierr.BadRequest, "the stream was closed earlier in this pipeline")
	}
	cfg := s.eng.Config()
	switch req.Type {
	case "execute":
		if req.Stmt == nil {
			return nil, apierr.New(apierr.BadRequest, "execute needs a stmt")
		}
		r, err := s.hranaStmt(c, st, req.Stmt)
		if err != nil {
			return nil, err
		}
		return &hStreamResponse{Type: "execute", Result: r}, nil
	case "batch":
		if req.Batch == nil {
			return nil, apierr.New(apierr.BadRequest, "batch needs a batch")
		}
		r, err := s.hranaBatch(c, st, req.Batch)
		if err != nil {
			return nil, err
		}
		return &hStreamResponse{Type: "batch", Result: r}, nil
	case "sequence":
		sql, err := st.text(req.SQL, req.SQLID)
		if err != nil {
			return nil, err
		}
		for _, one := range sqlsplit.Statements(sql) {
			if _, err := s.hranaRun(c, st, engine.Statement{SQL: one}); err != nil {
				return nil, err
			}
		}
		return &hStreamResponse{Type: "sequence"}, nil
	case "describe":
		sql, err := st.text(req.SQL, req.SQLID)
		if err != nil {
			return nil, err
		}
		r, err := s.hranaDescribe(c, st, sql)
		if err != nil {
			return nil, err
		}
		return &hStreamResponse{Type: "describe", Result: r}, nil
	case "store_sql":
		if req.SQLID == nil || req.SQL == nil {
			return nil, apierr.New(apierr.BadRequest, "store_sql needs sql_id and sql")
		}
		if _, ok := st.sqls[*req.SQLID]; ok {
			return nil, apierr.Newf(apierr.BadRequest, "sql_id %d is already in use", *req.SQLID)
		}
		if len(st.sqls) >= hranaMaxStoredSQL {
			return nil, apierr.Newf(apierr.BadRequest, "a stream stores at most %d SQL texts; close some with close_sql", hranaMaxStoredSQL)
		}
		n := int64(len(*req.SQL))
		if n > cfg.MaxSQLBytes {
			return nil, apierr.Newf(apierr.ValueTooLarge, "the SQL text is longer than %d bytes", cfg.MaxSQLBytes)
		}
		if st.sqlBytes+n > cfg.MaxRequestBytes {
			return nil, apierr.Newf(apierr.ValueTooLarge, "the stream's stored SQL would pass %d bytes", cfg.MaxRequestBytes)
		}
		st.sqls[*req.SQLID] = *req.SQL
		st.sqlBytes += n
		return &hStreamResponse{Type: "store_sql"}, nil
	case "close_sql":
		if req.SQLID == nil {
			return nil, apierr.New(apierr.BadRequest, "close_sql needs sql_id")
		}
		if sql, ok := st.sqls[*req.SQLID]; ok {
			st.sqlBytes -= int64(len(sql))
			delete(st.sqls, *req.SQLID)
		}
		return &hStreamResponse{Type: "close_sql"}, nil
	case "get_autocommit":
		auto := st.autocommit()
		return &hStreamResponse{Type: "get_autocommit", IsAutocommit: &auto}, nil
	case "close":
		s.libsql.closeStream(st)
		return &hStreamResponse{Type: "close"}, nil
	}
	return nil, apierr.Newf(apierr.BadRequest, "unknown request type %q", req.Type)
}

// hranaStmt runs one protocol statement and records it in the branch's
// stats.
func (s *Server) hranaStmt(c *hranaCall, st *libsqlStream, hs *hStmt) (*hStmtResult, error) {
	sql, err := st.text(hs.SQL, hs.SQLID)
	if err != nil {
		return nil, err
	}
	es := engine.Statement{SQL: sql, Args: hs.Args}
	if len(hs.NamedArgs) > 0 {
		es.Named = make(map[string]value.Value, len(hs.NamedArgs))
		for _, a := range hs.NamedArgs {
			es.Named[a.Name] = a.Value
		}
	}
	start := time.Now()
	r, err := s.hranaRun(c, st, es)
	if err != nil {
		return nil, err
	}
	out := &hStmtResult{
		Cols: make([]hCol, len(r.Columns)), Rows: r.Rows,
		AffectedRowCount: r.RowsAffected, LastInsertRowID: strPtr(fmt.Sprint(r.LastInsertRowID)),
		RowsRead: int64(len(r.Rows)), RowsWritten: r.RowsAffected,
		QueryDurationMS: float64(time.Since(start).Microseconds()) / 1000,
	}
	for i, col := range r.Columns {
		out.Cols[i] = hCol{Name: strPtr(col.Name), DeclType: optStr(col.DeclType)}
	}
	if hs.WantRows != nil && !*hs.WantRows {
		out.Rows = [][]value.Value{}
	}
	return out, nil
}

var emptyResult = &engine.Result{Columns: []engine.Column{}, Rows: [][]value.Value{}}

// hranaRun runs one statement on the stream. The engine owns transaction
// control: BEGIN, COMMIT and ROLLBACK are recognized here and turned into an
// interactive transaction, never passed to SQLite. Anything else runs in the
// open transaction, or in its own when none is open.
func (s *Server) hranaRun(c *hranaCall, st *libsqlStream, es engine.Statement) (*engine.Result, error) {
	ctl := classify(es.SQL)
	if ctl.kind != ctlNone && (len(es.Args) > 0 || len(es.Named) > 0) {
		return nil, apierr.New(apierr.InvalidParams, "the statement has 0 parameters")
	}
	st.syncTx()
	switch ctl.kind {
	case ctlBegin:
		if st.tx != nil || st.aborted {
			return nil, apierr.New(apierr.SQLError, "SQL error").WithDetail("cannot start a transaction within a transaction")
		}
		acc := access(c.p, c.db, c.target.Branch, ctl.readOnly)
		deadline := time.Now().Add(s.eng.Config().LibSQLTxTimeout)
		tx, err := s.eng.BeginTx(c.ctx, c.target, acc, engine.TxOptions{Migration: st.fkOff && acc == engine.Write, Deadline: deadline})
		if err != nil {
			return nil, err
		}
		st.tx = tx
		st.txDeadline = deadline
		return emptyResult, nil
	case ctlCommit:
		if st.aborted {
			st.aborted = false
			return nil, apierr.New(apierr.TransactionAborted, "the transaction was rolled back earlier; nothing was committed")
		}
		if st.tx == nil {
			return nil, apierr.New(apierr.SQLError, "SQL error").WithDetail("cannot commit - no transaction is active")
		}
		tx := st.tx
		st.tx = nil
		if err := tx.Commit(c.ctx); err != nil {
			return nil, err
		}
		return emptyResult, nil
	case ctlRollback:
		if st.aborted {
			st.aborted = false
			return emptyResult, nil
		}
		if st.tx == nil {
			return nil, apierr.New(apierr.SQLError, "SQL error").WithDetail("cannot rollback - no transaction is active")
		}
		st.tx.Rollback()
		st.tx = nil
		return emptyResult, nil
	case ctlForeignKeys:
		// Outside a transaction it shapes the next one, as migrate() expects.
		// Inside one SQLite ignores it, so it runs as written.
		if st.tx == nil && !st.aborted {
			st.fkOff = !ctl.on
			return emptyResult, nil
		}
	}
	if st.aborted {
		return nil, apierr.New(apierr.TransactionAborted, "the transaction was rolled back after an error; send ROLLBACK")
	}
	// Only statements that reach the engine count in the branch's stats.
	// The slow-query log is written off the request path, since an open
	// transaction may be holding the branch's writer meanwhile.
	start := time.Now()
	var r *engine.Result
	var err error
	if st.tx != nil {
		r, err = st.tx.Exec(c.ctx, es, c.budget)
		// SQLite, a limit, the deadline or maintenance on the branch may
		// have ended it. Later statements must not run outside the
		// transaction the client thinks it has.
		st.syncTx()
	} else {
		// After PRAGMA foreign_keys = OFF, a write outside a transaction
		// would either cascade or, with the check before each commit, fail
		// halfway through a table rebuild. So writes need a transaction.
		var refuse error
		if st.fkOff {
			refuse = apierr.New(apierr.BadRequest, "PRAGMA foreign_keys = OFF on this stream applies to the next transaction only: send BEGIN before the writes, or PRAGMA foreign_keys = ON")
		}
		r, err = s.eng.QueryWith(c.ctx, c.target, access(c.p, c.db, c.target.Branch, false), es, c.budget, refuse)
	}
	var results []*engine.Result
	if r != nil {
		results = []*engine.Result{r}
	}
	s.recordQueued(c.target.Branch, []StatementIn{{SQL: es.SQL}}, start, results, err)
	return r, err
}

func (s *Server) hranaDescribe(c *hranaCall, st *libsqlStream, sql string) (*hDescribeResult, error) {
	out := &hDescribeResult{Params: []hParam{}, Cols: []hCol{}}
	if classify(sql).kind != ctlNone {
		// Transaction control doesn't modify the database by itself, which
		// is how SQLite describes it too.
		out.IsReadonly = true
		return out, nil
	}
	var d *engine.Description
	var err error
	st.syncTx()
	if st.tx != nil {
		d, err = st.tx.Describe(sql)
	} else {
		d, err = s.eng.Describe(c.ctx, c.target, sql)
	}
	if err != nil {
		return nil, err
	}
	for _, p := range d.Params {
		out.Params = append(out.Params, hParam{Name: optStr(p)})
	}
	for _, col := range d.Columns {
		out.Cols = append(out.Cols, hCol{Name: strPtr(col.Name), DeclType: optStr(col.DeclType)})
	}
	out.IsExplain, out.IsReadonly = d.Explain, d.ReadOnly
	return out, nil
}

// hranaBatch runs a batch's steps in order, each only when its condition
// holds. A failed step is reported in step_errors and doesn't stop the
// batch; the conditions decide what runs after it.
func (s *Server) hranaBatch(c *hranaCall, st *libsqlStream, b *hBatch) (*hBatchResult, error) {
	if len(b.Steps) > hranaMaxSteps {
		return nil, apierr.Newf(apierr.BadRequest, "a batch holds at most %d steps", hranaMaxSteps)
	}
	// Check every condition before running any step, so a bad one can't
	// fail the batch after earlier steps ran.
	for i := range b.Steps {
		if c := b.Steps[i].Condition; c != nil {
			if err := checkCond(c, i, 0); err != nil {
				return nil, apierr.Newf(apierr.BadRequest, "step %d: %s", i, apierr.As(err).Message)
			}
		}
	}
	n := len(b.Steps)
	res := &hBatchResult{StepResults: make([]*hStmtResult, n), StepErrors: make([]*hError, n)}
	for i := range b.Steps {
		step := &b.Steps[i]
		if step.Condition != nil && !evalCond(step.Condition, res, st) {
			continue
		}
		r, err := s.hranaStmt(c, st, &step.Stmt)
		if err != nil {
			res.StepErrors[i] = s.hranaErr(err, c.requestID)
			continue
		}
		res.StepResults[i] = r
	}
	return res, nil
}

// checkCond validates a condition for the step at index current: known
// types, their fields, step references to earlier steps, and the nesting.
func checkCond(cond *hCond, current, depth int) error {
	if depth > hranaMaxCondDepth {
		return apierr.Newf(apierr.BadRequest, "conditions nest at most %d deep", hranaMaxCondDepth)
	}
	switch cond.Type {
	case "ok", "error":
		if cond.Step == nil || int64(*cond.Step) >= int64(current) {
			return apierr.New(apierr.BadRequest, "a condition must name an earlier step")
		}
	case "not":
		if cond.Cond == nil {
			return apierr.New(apierr.BadRequest, "a not condition needs cond")
		}
		return checkCond(cond.Cond, current, depth+1)
	case "and", "or":
		for i := range cond.Conds {
			if err := checkCond(&cond.Conds[i], current, depth+1); err != nil {
				return err
			}
		}
	case "is_autocommit":
	default:
		return apierr.Newf(apierr.BadRequest, "unknown condition type %q", cond.Type)
	}
	return nil
}

// evalCond evaluates a condition checkCond accepted.
func evalCond(cond *hCond, res *hBatchResult, st *libsqlStream) bool {
	switch cond.Type {
	case "ok":
		return res.StepResults[*cond.Step] != nil
	case "error":
		return res.StepErrors[*cond.Step] != nil
	case "not":
		return !evalCond(cond.Cond, res, st)
	case "and", "or":
		all := cond.Type == "and"
		for i := range cond.Conds {
			if evalCond(&cond.Conds[i], res, st) != all {
				return !all
			}
		}
		return all
	case "is_autocommit":
		return st.autocommit()
	}
	return false
}

// ---- transaction control

type ctlKind int

const (
	ctlNone ctlKind = iota
	ctlBegin
	ctlCommit
	ctlRollback
	ctlForeignKeys
)

type control struct {
	kind     ctlKind
	readOnly bool // BEGIN ... READONLY
	on       bool // PRAGMA foreign_keys = ON
}

// classify recognizes the statements the engine handles itself:
//
//	BEGIN [DEFERRED | IMMEDIATE | EXCLUSIVE] [TRANSACTION] [READONLY]
//	COMMIT [TRANSACTION], END [TRANSACTION]
//	ROLLBACK [TRANSACTION]          (not ROLLBACK TO, a savepoint)
//	PRAGMA foreign_keys = <value>
//
// Anything else, including these with a name, a schema or any other token,
// is ctlNone and goes to SQLite, whose authorizer denies transaction
// control. So a form missed here fails closed.
func classify(sql string) control {
	w, ok := controlWords(sql)
	if !ok || len(w) == 0 {
		return control{}
	}
	at := func(i int) string {
		if i < len(w) {
			return w[i]
		}
		return ""
	}
	switch w[0] {
	case "BEGIN":
		i := 1
		switch at(i) {
		case "DEFERRED", "IMMEDIATE", "EXCLUSIVE":
			i++
		}
		if at(i) == "TRANSACTION" {
			i++
		}
		c := control{kind: ctlBegin}
		if at(i) == "READONLY" {
			c.readOnly = true
			i++
		}
		if i == len(w) {
			return c
		}
	case "COMMIT", "END", "ROLLBACK":
		if len(w) == 1 || len(w) == 2 && w[1] == "TRANSACTION" {
			if w[0] == "ROLLBACK" {
				return control{kind: ctlRollback}
			}
			return control{kind: ctlCommit}
		}
	case "PRAGMA":
		if len(w) == 4 && w[1] == "FOREIGN_KEYS" && w[2] == "=" {
			switch w[3] {
			case "ON", "TRUE", "YES", "1":
				return control{kind: ctlForeignKeys, on: true}
			case "OFF", "FALSE", "NO", "0":
				return control{kind: ctlForeignKeys}
			}
		}
	}
	return control{}
}

// controlWords returns the upper-cased words of SQL text, and "=" as a word,
// skipping comments and whitespace and allowing one trailing semicolon. It
// fails on anything else (quotes, dots, parentheses), which no statement
// classify recognizes contains.
func controlWords(sql string) ([]string, bool) {
	var out []string
	rest := sqlsplit.StripComments(sql)
	for rest != "" {
		c := rest[0]
		switch {
		case c == ';':
			// Only comments and whitespace may follow.
			return out, sqlsplit.StripComments(rest[1:]) == ""
		case c == '=':
			out = append(out, "=")
			rest = rest[1:]
		case sqlsplit.IsWordByte(c):
			j := 1
			for j < len(rest) && sqlsplit.IsWordByte(rest[j]) {
				j++
			}
			out = append(out, strings.ToUpper(rest[:j]))
			rest = rest[j:]
		default:
			return nil, false
		}
		rest = sqlsplit.StripComments(rest)
	}
	return out, true
}

// ---- streams

// libsqlStreams holds the open streams.
type libsqlStreams struct {
	log      *slog.Logger
	mu       sync.Mutex
	m        map[string]*libsqlStream
	perToken map[string]int
	closed   bool
}

// libsqlStream is server-side stream state. mu is held for a whole HTTP
// request, which serializes the stream's requests.
type libsqlStream struct {
	id, tokenID, branchID string
	// owner is who opened the stream (streamOwner). Only the same caller,
	// acting in the same workspace at the same level, may continue it.
	owner string
	reg   *libsqlStreams

	mu         sync.Mutex
	secret     string
	closed     bool
	sqls       map[int32]string
	sqlBytes   int64
	tx         *engine.Tx
	txDeadline time.Time
	aborted    bool // SQLite or a limit ended the transaction; waiting for ROLLBACK
	fkOff      bool // PRAGMA foreign_keys = OFF for the next transaction
	idleAt     time.Time
	timer      *time.Timer
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// open makes a stream under the caps and returns it locked.
func (r *libsqlStreams) open(tokenID, owner, branchID string, cfg config.Config) (*libsqlStream, *apierr.Error) {
	maxAll, maxToken, idle := cfg.LibSQLMaxStreams, cfg.LibSQLStreamsPerToken, cfg.LibSQLStreamIdle
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, apierr.New(apierr.Unavailable, "the engine is shutting down")
	}
	if len(r.m) >= maxAll {
		return nil, apierr.Newf(apierr.TooManyStreams, "the instance has %d open libSQL streams, the most allowed", maxAll)
	}
	if r.perToken[tokenID] >= maxToken {
		return nil, apierr.Newf(apierr.TooManyStreams, "this token has %d open libSQL streams, the most allowed", maxToken)
	}
	st := &libsqlStream{id: randomHex(16), tokenID: tokenID, owner: owner, branchID: branchID, reg: r, sqls: map[int32]string{}}
	st.mu.Lock()
	st.idleAt = time.Now().Add(idle)
	st.timer = time.AfterFunc(idle, st.expire)
	r.m[st.id] = st
	r.perToken[tokenID]++
	return st, nil
}

// streamOwner names who a stream belongs to. For an ordinary token it is
// the token. The dashboard's service token acts for many users, each
// narrowed to their own level in one workspace (PLAN 17.8), so the
// workspace, level and user are part of it too: a transaction a user began
// with write access can't be continued by a request narrowed to read, or
// by another user, even with the baton.
func streamOwner(p auth.Principal) string {
	return strings.Join([]string{p.TokenID, p.Scope, p.WorkspaceID, p.Access, p.Actor}, "\x00")
}

func badBaton() *apierr.Error {
	return apierr.New(apierr.InvalidBaton, "the baton is stale or names no open stream; the stream may have expired and its transaction been rolled back")
}

// lookup finds the stream a baton names and returns it locked. A baton that
// is stale, made for another token or branch, or for a closed stream is
// refused the same way.
func (r *libsqlStreams) lookup(baton, owner, branchID string) (*libsqlStream, *apierr.Error) {
	id, secret, ok := strings.Cut(baton, ".")
	if !ok {
		return nil, badBaton()
	}
	r.mu.Lock()
	st := r.m[id]
	r.mu.Unlock()
	if st == nil || subtle.ConstantTimeCompare([]byte(st.owner), []byte(owner)) != 1 || st.branchID != branchID {
		return nil, badBaton()
	}
	st.mu.Lock()
	if st.closed || subtle.ConstantTimeCompare([]byte(secret), []byte(st.secret)) != 1 {
		st.mu.Unlock()
		return nil, badBaton()
	}
	return st, nil
}

// finish ends a request on a locked stream: it hands out a fresh baton (nil
// once the stream is closed), re-arms the expiry and unlocks.
func (r *libsqlStreams) finish(st *libsqlStream, cfg config.Config) *string {
	defer st.mu.Unlock()
	if st.closed {
		return nil
	}
	st.secret = randomHex(32)
	st.idleAt = time.Now().Add(cfg.LibSQLStreamIdle)
	st.timer.Reset(st.nextExpiry(time.Now()))
	return strPtr(st.id + "." + st.secret)
}

// nextExpiry is how long until the stream goes idle or its transaction
// passes the transaction timeout. Call with mu.
func (st *libsqlStream) nextExpiry(now time.Time) time.Duration {
	d := st.idleAt.Sub(now)
	if st.tx != nil {
		d = min(d, st.txDeadline.Sub(now))
	}
	return max(d, 0)
}

// expire runs from the stream's timer. A request in flight holds mu, so this
// waits for it, then finds the stream used again and re-arms.
func (st *libsqlStream) expire() {
	defer recovery.Log(st.reg.log, "libsql stream expiry")
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return
	}
	now := time.Now()
	if now.Before(st.idleAt) && (st.tx == nil || now.Before(st.txDeadline)) {
		st.timer.Reset(st.nextExpiry(now))
		return
	}
	st.reg.closeStream(st)
}

// closeStream closes a stream, rolling back its transaction. Call with the
// stream's mu.
func (r *libsqlStreams) closeStream(st *libsqlStream) {
	if st.closed {
		return
	}
	st.closed = true
	if st.tx != nil {
		st.tx.Rollback()
		st.tx = nil
	}
	st.sqls = nil
	st.timer.Stop()
	r.mu.Lock()
	delete(r.m, st.id)
	if r.perToken[st.tokenID]--; r.perToken[st.tokenID] <= 0 {
		delete(r.perToken, st.tokenID)
	}
	r.mu.Unlock()
}

func (r *libsqlStreams) closeAll() {
	r.mu.Lock()
	r.closed = true
	var all []*libsqlStream
	for _, st := range r.m {
		all = append(all, st)
	}
	r.mu.Unlock()
	for _, st := range all {
		st.mu.Lock()
		r.closeStream(st)
		st.mu.Unlock()
	}
}

// syncTx notices a transaction the engine ended on its own (its deadline,
// maintenance on the branch, SQLite after an error): the stream then refuses
// statements until ROLLBACK, as if inside a failed transaction. Call with mu.
func (st *libsqlStream) syncTx() {
	if st.tx != nil && st.tx.Done() {
		st.tx = nil
		st.aborted = true
	}
}

// autocommit is what get_autocommit and is_autocommit report: false while a
// transaction is open or a failed one waits for ROLLBACK, matching what the
// stream accepts. Call with mu.
func (st *libsqlStream) autocommit() bool {
	st.syncTx()
	return st.tx == nil && !st.aborted
}

// text resolves a statement's SQL: exactly one of the text and a stored id.
func (st *libsqlStream) text(sql *string, id *int32) (string, error) {
	switch {
	case sql != nil && id != nil:
		return "", apierr.New(apierr.BadRequest, "send sql or sql_id, not both")
	case sql != nil:
		return *sql, nil
	case id != nil:
		t, ok := st.sqls[*id]
		if !ok {
			return "", apierr.Newf(apierr.BadRequest, "no stored SQL with sql_id %d", *id)
		}
		return t, nil
	}
	return "", apierr.New(apierr.BadRequest, "the statement needs sql or sql_id")
}

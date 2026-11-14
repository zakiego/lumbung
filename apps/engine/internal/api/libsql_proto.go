package api

// JSON shapes of the libSQL remote protocol over HTTP (Hrana v2 and v3,
// docs/HRANA_3_SPEC.md in the libsql repository). Values are ADR 0003's
// typed objects, which are the protocol's value objects.

import (
	"github.com/zakiego/lumbung/apps/engine/internal/value"
)

type hPipelineReq struct {
	Baton    *string      `json:"baton"`
	Requests []hStreamReq `json:"requests"`
}

type hCursorReq struct {
	Baton *string `json:"baton"`
	Batch *hBatch `json:"batch"`
}

type hStreamReq struct {
	Type  string  `json:"type"`
	Stmt  *hStmt  `json:"stmt"`
	Batch *hBatch `json:"batch"`
	SQL   *string `json:"sql"`
	SQLID *int32  `json:"sql_id"`
}

type hStmt struct {
	SQL       *string       `json:"sql"`
	SQLID     *int32        `json:"sql_id"`
	Args      []value.Value `json:"args"`
	NamedArgs []hNamedArg   `json:"named_args"`
	WantRows  *bool         `json:"want_rows"`
}

type hNamedArg struct {
	Name  string      `json:"name"`
	Value value.Value `json:"value"`
}

type hBatch struct {
	Steps []hStep `json:"steps"`
}

type hStep struct {
	Condition *hCond `json:"condition"`
	Stmt      hStmt  `json:"stmt"`
}

type hCond struct {
	Type  string  `json:"type"`
	Step  *uint32 `json:"step"`
	Cond  *hCond  `json:"cond"`
	Conds []hCond `json:"conds"`
}

type hPipelineResp struct {
	Baton   *string         `json:"baton"`
	BaseURL *string         `json:"base_url"`
	Results []hStreamResult `json:"results"`
}

type hCursorResp struct {
	Baton   *string `json:"baton"`
	BaseURL *string `json:"base_url"`
}

type hStreamResult struct {
	Type     string           `json:"type"` // "ok" or "error"
	Response *hStreamResponse `json:"response,omitempty"`
	Error    *hError          `json:"error,omitempty"`
}

type hStreamResponse struct {
	Type         string `json:"type"`
	Result       any    `json:"result,omitempty"`
	IsAutocommit *bool  `json:"is_autocommit,omitempty"`
}

type hError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

type hCol struct {
	Name     *string `json:"name"`
	DeclType *string `json:"decltype"`
}

type hStmtResult struct {
	Cols             []hCol          `json:"cols"`
	Rows             [][]value.Value `json:"rows"`
	AffectedRowCount int64           `json:"affected_row_count"`
	LastInsertRowID  *string         `json:"last_insert_rowid"`
	RowsRead         int64           `json:"rows_read"`
	RowsWritten      int64           `json:"rows_written"`
	QueryDurationMS  float64         `json:"query_duration_ms"`
}

type hBatchResult struct {
	StepResults []*hStmtResult `json:"step_results"`
	StepErrors  []*hError      `json:"step_errors"`
}

type hDescribeResult struct {
	Params     []hParam `json:"params"`
	Cols       []hCol   `json:"cols"`
	IsExplain  bool     `json:"is_explain"`
	IsReadonly bool     `json:"is_readonly"`
}

type hParam struct {
	Name *string `json:"name"`
}

// Cursor entries (v3/cursor), one JSON object per line.
type hStepBegin struct {
	Type string `json:"type"` // step_begin
	Step int    `json:"step"`
	Cols []hCol `json:"cols"`
}

type hRow struct {
	Type string        `json:"type"` // row
	Row  []value.Value `json:"row"`
}

type hStepEnd struct {
	Type             string  `json:"type"` // step_end
	AffectedRowCount int64   `json:"affected_row_count"`
	LastInsertRowID  *string `json:"last_insert_rowid"`
}

type hStepError struct {
	Type  string `json:"type"` // step_error
	Step  int    `json:"step"`
	Error hError `json:"error"`
}

func strPtr(s string) *string { return &s }

// optStr is nil for "", which the protocol sends as null.
func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

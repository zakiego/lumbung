// Package apierr defines the engine's errors. Each has a stable machine
// readable code and an HTTP status; the REST API sends them as
// application/problem+json. Codes is the documented list: add to it, never
// rename or reuse a code.
package apierr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ncruces/go-sqlite3"
)

// Error is an engine error a client can act on.
type Error struct {
	Code    string
	Status  int
	Message string
	Detail  string // optional, e.g. the SQLite message
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return e.Code + ": " + e.Message + ": " + e.Detail
	}
	return e.Code + ": " + e.Message
}

// CodeInfo documents one code.
type CodeInfo struct {
	Code   string
	Status int
	Doc    string
}

// The error codes.
const (
	BadRequest            = "bad_request"
	InvalidName           = "invalid_name"
	EmptyStatement        = "empty_statement"
	MultipleStatements    = "multiple_statements"
	InvalidParams         = "invalid_params"
	SQLError              = "sql_error"
	ConstraintViolation   = "constraint_violation"
	StatementDenied       = "statement_denied"
	ReadOnly              = "read_only"
	StatementTimeout      = "statement_timeout"
	ValueTooLarge         = "value_too_large"
	ResultTooLarge        = "result_too_large"
	MemoryLimit           = "memory_limit"
	BranchSizeLimit       = "branch_size_limit"
	InstanceQuotaExceeded = "instance_quota_exceeded"
	DiskFull              = "insufficient_disk"
	DatabaseNotFound      = "database_not_found"
	BranchNotFound        = "branch_not_found"
	NameTaken             = "name_taken"
	LimitReached          = "limit_reached"
	SnapshotNotFound      = "snapshot_not_found"
	BranchProtected       = "branch_protected"
	ParentGone            = "parent_gone"
	BranchNotEmpty        = "branch_not_empty"
	InvalidImport         = "invalid_import"
	ImportTooLarge        = "import_too_large"
	Unauthorized          = "unauthorized"
	Forbidden             = "forbidden"
	RateLimited           = "rate_limited"
	NotFound              = "not_found"
	MethodNotAllowed      = "method_not_allowed"
	InvalidRequest        = "invalid_request"
	RequestTooLarge       = "request_too_large"
	RequestTimeout        = "request_timeout"
	IdempotencyMismatch   = "idempotency_mismatch"
	TokenNotFound         = "token_not_found"
	TransactionAborted    = "transaction_aborted"
	InvalidBaton          = "invalid_baton"
	TooManyStreams        = "too_many_streams"
	Busy                  = "busy"
	Unavailable           = "unavailable"
	Internal              = "internal"
)

// Codes lists every code with its status, for the Error codes docs page.
var Codes = []CodeInfo{
	{BadRequest, http.StatusBadRequest, "The request body or a parameter is malformed."},
	{InvalidName, http.StatusBadRequest, "A name doesn't match `[a-z0-9][a-z0-9-]{0,62}`."},
	{EmptyStatement, http.StatusBadRequest, "The SQL text holds no statement."},
	{MultipleStatements, http.StatusBadRequest, "`query` got more than one statement. Use `batch` for several."},
	{InvalidParams, http.StatusBadRequest, "The parameters don't match the statement's placeholders."},
	{SQLError, http.StatusBadRequest, "SQLite rejected the statement. `detail` has SQLite's message."},
	{ConstraintViolation, http.StatusConflict, "A UNIQUE, NOT NULL, CHECK or foreign key constraint failed."},
	{StatementDenied, http.StatusForbidden, "The statement uses something Lumbung doesn't allow: ATTACH, VACUUM INTO, transaction control, temp objects, a pragma off the allowlist or a denied function."},
	{ReadOnly, http.StatusForbidden, "The token can only read, and the statement writes."},
	{StatementTimeout, http.StatusRequestTimeout, "The statement ran past the statement timeout and was cancelled. Nothing it wrote was kept."},
	{ValueTooLarge, http.StatusRequestEntityTooLarge, "A string, blob or statement is larger than its limit."},
	{ResultTooLarge, http.StatusRequestEntityTooLarge, "The result has more rows or bytes than the limit. Nothing partial is returned and writes are rolled back."},
	{MemoryLimit, http.StatusUnprocessableEntity, "The statement needed more memory than one connection may use."},
	{BranchSizeLimit, http.StatusInsufficientStorage, "The write would grow the branch past its size limit."},
	{InstanceQuotaExceeded, http.StatusInsufficientStorage, "The write would grow the instance past its total size quota."},
	{DiskFull, http.StatusInsufficientStorage, "The host disk has less free space than the configured minimum."},
	{DatabaseNotFound, http.StatusNotFound, "No database with that name or id."},
	{BranchNotFound, http.StatusNotFound, "No branch with that name or id in this database."},
	{NameTaken, http.StatusConflict, "Another database or branch already has that name."},
	{LimitReached, http.StatusConflict, "The instance already has the most databases, or the database the most branches, allowed."},
	{SnapshotNotFound, http.StatusNotFound, "No snapshot with that id in this database."},
	{BranchProtected, http.StatusConflict, "The branch is protected, so it can't be deleted, reset or overwritten by an import. The default branch is always protected."},
	{ParentGone, http.StatusConflict, "The branch can't be reset because the branch or snapshot it was made from no longer exists."},
	{BranchNotEmpty, http.StatusConflict, "An import into a branch that already has tables needs `mode=replace`."},
	{InvalidImport, http.StatusBadRequest, "The uploaded file isn't a valid SQLite database or SQL dump, or fails its integrity check."},
	{ImportTooLarge, http.StatusRequestEntityTooLarge, "The upload is larger than the import limit or the branch size limit."},
	{Unauthorized, http.StatusUnauthorized, "The `Authorization: Bearer` token is missing, malformed, revoked or expired."},
	{Forbidden, http.StatusForbidden, "The token is valid but its scope doesn't allow this."},
	{RateLimited, http.StatusTooManyRequests, "Too many requests for this token, or too many failed logins from this address. `Retry-After` says when to try again."},
	{NotFound, http.StatusNotFound, "No such endpoint."},
	{MethodNotAllowed, http.StatusMethodNotAllowed, "The endpoint doesn't accept this HTTP method."},
	{InvalidRequest, http.StatusUnprocessableEntity, "The request doesn't match the API schema. `errors` lists each problem."},
	{RequestTooLarge, http.StatusRequestEntityTooLarge, "The request body is larger than the limit."},
	{RequestTimeout, http.StatusRequestTimeout, "The request body took too long to arrive."},
	{IdempotencyMismatch, http.StatusUnprocessableEntity, "This `Idempotency-Key` was already used for a different request."},
	{TokenNotFound, http.StatusNotFound, "No live token with that id."},
	{TransactionAborted, http.StatusConflict, "The open transaction was rolled back: SQLite ended it after an error, or it hit a limit or the transaction timeout. Send ROLLBACK, then start again. Interactive transactions only."},
	{InvalidBaton, http.StatusBadRequest, "The baton names no open stream: it was already used, belongs to another token or branch, or the stream was closed or expired and its transaction rolled back. libSQL protocol only."},
	{TooManyStreams, http.StatusTooManyRequests, "The token or the instance already has the most open libSQL streams allowed. Close streams, or wait for idle ones to expire."},
	{Busy, http.StatusServiceUnavailable, "The branch stayed locked longer than the busy timeout. Retry."},
	{Unavailable, http.StatusServiceUnavailable, "The engine can't serve: for example meta.db is newer than this binary."},
	{Internal, http.StatusInternalServerError, "Something failed inside the engine. The log has details."},
}

var statusOf = func() map[string]int {
	m := map[string]int{}
	for _, c := range Codes {
		m[c.Code] = c.Status
	}
	return m
}()

// New makes an error with a documented code.
func New(code, message string) *Error {
	status, ok := statusOf[code]
	if !ok {
		panic("apierr: undocumented code " + code)
	}
	return &Error{Code: code, Status: status, Message: message}
}

// Newf is New with formatting.
func Newf(code, format string, args ...any) *Error {
	return New(code, fmt.Sprintf(format, args...))
}

// WithDetail returns a copy with a detail line.
func (e *Error) WithDetail(detail string) *Error {
	c := *e
	c.Detail = detail
	return &c
}

// As returns err as an *Error, wrapping anything else as internal.
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return New(Internal, "internal error").WithDetail(err.Error())
}

// FromSQLite maps an error from running user SQL. An interrupt only happens
// when the request's context ends, which the statement timeout bounds.
func FromSQLite(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var se *sqlite3.Error
	msg := err.Error()
	if errors.As(err, &se) {
		msg = se.Error()
	}
	switch {
	case strings.Contains(msg, "cannot VACUUM from within a transaction"):
		// Every user statement runs inside the engine's transaction, so
		// SQLite refuses VACUUM and VACUUM INTO before touching any file.
		// The literal-path form is already denied by the authorizer.
		return New(StatementDenied, "VACUUM isn't allowed").WithDetail(msg)
	case errors.Is(err, sqlite3.INTERRUPT):
		return New(StatementTimeout, "statement cancelled")
	case errors.Is(err, sqlite3.AUTH):
		return New(StatementDenied, "statement not allowed").WithDetail(msg)
	case errors.Is(err, sqlite3.READONLY):
		return New(ReadOnly, "this access can't write").WithDetail(msg)
	case errors.Is(err, sqlite3.TOOBIG):
		return New(ValueTooLarge, "value or statement too large").WithDetail(msg)
	case errors.Is(err, sqlite3.FULL):
		return New(BranchSizeLimit, "branch size limit reached").WithDetail(msg)
	case errors.Is(err, sqlite3.NOMEM):
		return New(MemoryLimit, "statement needed too much memory").WithDetail(msg)
	case errors.Is(err, sqlite3.CONSTRAINT):
		return New(ConstraintViolation, "constraint failed").WithDetail(msg)
	case errors.Is(err, sqlite3.BUSY), errors.Is(err, sqlite3.LOCKED):
		return New(Busy, "branch is busy").WithDetail(msg)
	case errors.Is(err, sqlite3.RANGE):
		return New(InvalidParams, "parameter index out of range").WithDetail(msg)
	case errors.Is(err, sqlite3.CANTOPEN):
		// Only the confined VFS refusing a file outside the branch gets here
		// from user SQL.
		return New(StatementDenied, "statement not allowed").WithDetail(msg)
	case errors.Is(err, sqlite3.ERROR), errors.Is(err, sqlite3.MISMATCH), errors.Is(err, sqlite3.SCHEMA):
		return New(SQLError, "SQL error").WithDetail(msg)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return New(StatementTimeout, "statement cancelled")
	}
	return New(Internal, "internal error").WithDetail(msg)
}

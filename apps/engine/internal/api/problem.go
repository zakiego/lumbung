package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
)

// Problem is an error response, RFC 9457 application/problem+json with a
// stable machine-readable code.
type Problem struct {
	Type      string         `json:"type" doc:"Always about:blank: match on code instead."`
	Title     string         `json:"title" doc:"Short human-readable summary."`
	Status    int            `json:"status" doc:"HTTP status code."`
	Code      string         `json:"code" doc:"Stable machine-readable error code. See the Error codes reference."`
	Detail    string         `json:"detail,omitempty" doc:"More detail, such as SQLite's message."`
	RequestID string         `json:"request_id,omitempty" doc:"The request id, also sent as X-Request-Id."`
	Errors    []ProblemField `json:"errors,omitempty" doc:"Each schema problem, for invalid_request."`
}

// ProblemField is one schema validation problem.
type ProblemField struct {
	Message  string `json:"message"`
	Location string `json:"location,omitempty"`
	Value    any    `json:"value,omitempty"`
}

func (p *Problem) Error() string             { return p.Code + ": " + p.Title }
func (p *Problem) GetStatus() int            { return p.Status }
func (p *Problem) ContentType(string) string { return "application/problem+json" }
func problemFrom(e *apierr.Error) *Problem {
	return &Problem{Type: "about:blank", Title: e.Message, Status: e.Status, Code: e.Code, Detail: e.Detail}
}

// codeForStatus names errors Huma raises itself (validation, body limits).
func codeForStatus(status int) string {
	switch status {
	case http.StatusUnprocessableEntity:
		return apierr.InvalidRequest
	case http.StatusRequestEntityTooLarge:
		return apierr.RequestTooLarge
	case http.StatusRequestTimeout:
		return apierr.RequestTimeout
	case http.StatusNotFound:
		return apierr.NotFound
	case http.StatusMethodNotAllowed:
		return apierr.MethodNotAllowed
	case http.StatusUnauthorized:
		return apierr.Unauthorized
	case http.StatusForbidden:
		return apierr.Forbidden
	case http.StatusTooManyRequests:
		return apierr.RateLimited
	case http.StatusBadRequest:
		return apierr.BadRequest
	}
	return apierr.Internal
}

func init() {
	// Lists in responses are always arrays, never null.
	huma.DefaultArrayNullable = false
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		p := &Problem{Type: "about:blank", Title: msg, Status: status, Code: codeForStatus(status)}
		if status >= 500 {
			// Never echo internal details to clients.
			p.Title = "internal error"
		}
		for _, err := range errs {
			if err == nil {
				continue
			}
			var d *huma.ErrorDetail
			if errors.As(err, &d) {
				p.Errors = append(p.Errors, ProblemField{Message: d.Message, Location: d.Location, Value: d.Value})
			} else {
				p.Errors = append(p.Errors, ProblemField{Message: err.Error()})
			}
		}
		return p
	}
}

// toProblem turns an engine error into a problem response and notes its code
// for the audit log.
func toProblem(ctx context.Context, err error) error {
	var p *Problem
	if !errors.As(err, &p) {
		ae := apierr.As(err)
		if ae.Code == apierr.Internal {
			logFrom(ctx).Error("internal error", "err", err, "request_id", requestInfoFrom(ctx).id)
			ae = apierr.New(apierr.Internal, "internal error")
		}
		p = problemFrom(ae)
	}
	if info := requestInfoFrom(ctx); info != nil {
		p.RequestID = info.id
		info.code = p.Code
	}
	return p
}

// writeProblem writes a problem from middleware, before an operation runs.
func writeProblem(ctx huma.Context, e *apierr.Error, requestID string) {
	p := problemFrom(e)
	p.RequestID = requestID
	ctx.SetHeader("Content-Type", "application/problem+json")
	ctx.SetStatus(p.Status)
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(p)
}

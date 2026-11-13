package api

import (
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zakiego/lumbung/apps/engine/internal/engine"
	"github.com/zakiego/lumbung/apps/engine/internal/value"
)

// Value is a SQLite value in a response, in the typed form (ADR 0003).
type Value value.Value

func (v Value) MarshalJSON() ([]byte, error) { return value.Value(v).MarshalJSON() }

func typed(name string, valueSchema *huma.Schema, field string) *huma.Schema {
	props := map[string]*huma.Schema{"type": {Type: "string", Enum: []any{name}}}
	required := []string{"type"}
	if valueSchema != nil {
		props[field] = valueSchema
		required = append(required, field)
	}
	return &huma.Schema{Type: "object", Title: name, Properties: props, Required: required, AdditionalProperties: false}
}

// Schema documents the five typed shapes.
func (Value) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{
		Description: "A SQLite value. Integers are decimal strings so all 64 bits survive; floats are numbers, with \"Infinity\" and \"-Infinity\" as strings; blobs are standard base64.",
		OneOf: []*huma.Schema{
			typed("null", nil, ""),
			typed("integer", &huma.Schema{Type: "string", Pattern: `^-?(0|[1-9][0-9]*)$`}, "value"),
			typed("float", &huma.Schema{OneOf: []*huma.Schema{{Type: "number"}, {Type: "string", Enum: []any{"Infinity", "-Infinity"}}}}, "value"),
			typed("text", &huma.Schema{Type: "string"}, "value"),
			typed("blob", &huma.Schema{Type: "string", ContentEncoding: "base64"}, "base64"),
		},
	}
}

// Param is a parameter value in a request: the typed form, or plain JSON
// (null, boolean, string, number). The engine checks it; the schema only
// describes it.
type Param value.Value

func (p *Param) UnmarshalJSON(b []byte) error { return (*value.Value)(p).UnmarshalJSON(b) }

func (Param) Schema(r huma.Registry) *huma.Schema {
	return &huma.Schema{
		Description: "A parameter: a typed value object, or plain JSON. null binds NULL, true/false bind 1/0, a string binds text, a number without a fraction or exponent binds an integer (it must fit in 64 bits), any other number binds a float. Blobs need the typed form.",
		OneOf: []*huma.Schema{
			{Type: "null"}, {Type: "boolean"}, {Type: "string"}, {Type: "number"},
			Value{}.Schema(r),
		},
	}
}

// StatementIn is one statement in a request.
type StatementIn struct {
	SQL   string           `json:"sql" minLength:"1" doc:"One SQL statement."`
	Args  []Param          `json:"args,omitempty" doc:"Values for positional parameters (?, ?1), in order."`
	Named map[string]Param `json:"named,omitempty" doc:"Values for named parameters (:name, @name, $name), keyed with or without the prefix."`
}

func (s StatementIn) engine() engine.Statement {
	st := engine.Statement{SQL: s.SQL}
	for _, a := range s.Args {
		st.Args = append(st.Args, value.Value(a))
	}
	if len(s.Named) > 0 {
		st.Named = map[string]value.Value{}
		for k, v := range s.Named {
			st.Named[k] = value.Value(v)
		}
	}
	return st
}

// ColumnOut describes a result column.
type ColumnOut struct {
	Name     string `json:"name"`
	DeclType string `json:"decltype" doc:"The column's declared type, or empty for an expression."`
}

// ResultOut is the result of one statement.
type ResultOut struct {
	Columns         []ColumnOut `json:"columns"`
	Rows            [][]Value   `json:"rows" doc:"Rows as arrays in column order."`
	RowsAffected    int64       `json:"rows_affected" doc:"Rows changed by an INSERT, UPDATE or DELETE, not counting triggers."`
	LastInsertRowID string      `json:"last_insert_rowid" pattern:"^-?[0-9]+$" doc:"The rowid of the last row this statement inserted, or \"0\". A decimal string, since rowids can pass 2^53."`
}

func resultOut(r *engine.Result) ResultOut {
	out := ResultOut{Columns: make([]ColumnOut, len(r.Columns)), Rows: make([][]Value, len(r.Rows)),
		RowsAffected: r.RowsAffected, LastInsertRowID: strconv.FormatInt(r.LastInsertRowID, 10)}
	for i, c := range r.Columns {
		out.Columns[i] = ColumnOut{Name: c.Name, DeclType: c.DeclType}
	}
	for i, row := range r.Rows {
		vs := make([]Value, len(row))
		for j, v := range row {
			vs[j] = Value(v)
		}
		out.Rows[i] = vs
	}
	return out
}

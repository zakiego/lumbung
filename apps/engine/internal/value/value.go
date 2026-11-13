// Package value encodes SQLite values as JSON without losing type or
// precision (PLAN 6.4, ADR 0003).
//
// Responses always use the typed form:
//
//	{"type":"null"}
//	{"type":"integer","value":"9007199254740993"}   decimal string, any int64
//	{"type":"float","value":1.5}                     JSON number; ±Infinity as a string
//	{"type":"text","value":"hello"}
//	{"type":"blob","base64":"AAEC"}                  standard base64 with padding
//
// Parameters accept the same typed form, or plain JSON for convenience:
// null, true/false (integer 1/0), a string (text), and a number. A number
// written without a fraction or exponent is an integer and must fit in 64
// bits; any other number is a float.
package value

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// Kind is the SQLite storage class of a value.
type Kind uint8

const (
	Null Kind = iota
	Integer
	Float
	Text
	Blob
)

func (k Kind) String() string {
	switch k {
	case Null:
		return "null"
	case Integer:
		return "integer"
	case Float:
		return "float"
	case Text:
		return "text"
	case Blob:
		return "blob"
	}
	return fmt.Sprintf("kind(%d)", uint8(k))
}

// Value is one SQLite value.
type Value struct {
	Kind  Kind
	Int   int64
	Float float64
	Text  string
	Blob  []byte
}

// Constructors.
func NullValue() Value             { return Value{Kind: Null} }
func IntValue(v int64) Value       { return Value{Kind: Integer, Int: v} }
func FloatValue(v float64) Value   { return Value{Kind: Float, Float: v} }
func TextValue(v string) Value     { return Value{Kind: Text, Text: v} }
func BlobValue(v []byte) Value     { return Value{Kind: Blob, Blob: v} }
func (v Value) IsNull() bool       { return v.Kind == Null }
func (v Value) Equal(o Value) bool { return equal(v, o) }

func equal(a, b Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case Null:
		return true
	case Integer:
		return a.Int == b.Int
	case Float:
		return a.Float == b.Float || (math.IsNaN(a.Float) && math.IsNaN(b.Float))
	case Text:
		return a.Text == b.Text
	case Blob:
		return bytes.Equal(a.Blob, b.Blob)
	}
	return false
}

type wire struct {
	Type   string          `json:"type"`
	Value  json.RawMessage `json:"value,omitempty"`
	Base64 *string         `json:"base64,omitempty"`
}

// MarshalJSON writes the typed form.
func (v Value) MarshalJSON() ([]byte, error) {
	switch v.Kind {
	case Null:
		return []byte(`{"type":"null"}`), nil
	case Integer:
		return []byte(`{"type":"integer","value":"` + strconv.FormatInt(v.Int, 10) + `"}`), nil
	case Float:
		var num string
		switch {
		case math.IsInf(v.Float, 1):
			num = `"Infinity"`
		case math.IsInf(v.Float, -1):
			num = `"-Infinity"`
		case math.IsNaN(v.Float):
			// SQLite stores NaN as NULL, so this only guards against misuse.
			return nil, errors.New("value: NaN has no JSON form")
		default:
			b, err := json.Marshal(v.Float)
			if err != nil {
				return nil, err
			}
			num = string(b)
		}
		return []byte(`{"type":"float","value":` + num + `}`), nil
	case Text:
		b, err := json.Marshal(v.Text)
		if err != nil {
			return nil, err
		}
		return append(append([]byte(`{"type":"text","value":`), b...), '}'), nil
	case Blob:
		return []byte(`{"type":"blob","base64":"` + base64.StdEncoding.EncodeToString(v.Blob) + `"}`), nil
	}
	return nil, fmt.Errorf("value: unknown kind %d", v.Kind)
}

var integerLiteral = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// UnmarshalJSON reads a parameter: the typed form or plain JSON.
func (v *Value) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return errors.New("value: empty")
	}
	switch data[0] {
	case 'n':
		if string(data) != "null" {
			return fmt.Errorf("value: invalid JSON %q", data)
		}
		*v = NullValue()
		return nil
	case 't', 'f':
		var b bool
		if err := json.Unmarshal(data, &b); err != nil {
			return fmt.Errorf("value: %w", err)
		}
		*v = IntValue(0)
		if b {
			v.Int = 1
		}
		return nil
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("value: %w", err)
		}
		*v = TextValue(s)
		return nil
	case '{':
		return v.unmarshalTyped(data)
	case '[':
		return errors.New("value: arrays aren't values")
	}
	return v.unmarshalNumber(string(data))
}

func (v *Value) unmarshalNumber(s string) error {
	if integerLiteral.MatchString(s) {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("value: integer %s doesn't fit in 64 bits", s)
		}
		*v = IntValue(n)
		return nil
	}
	var f float64
	if err := json.Unmarshal([]byte(s), &f); err != nil {
		return fmt.Errorf("value: invalid number %q", s)
	}
	*v = FloatValue(f)
	return nil
}

func (v *Value) unmarshalTyped(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	var w wire
	if err := dec.Decode(&w); err != nil {
		return fmt.Errorf("value: %w", err)
	}
	needValue := func() error {
		if len(w.Value) == 0 || string(w.Value) == "null" {
			return fmt.Errorf("value: %s needs a value", w.Type)
		}
		if w.Base64 != nil {
			return fmt.Errorf("value: %s takes no base64", w.Type)
		}
		return nil
	}
	switch w.Type {
	case "null":
		if len(w.Value) != 0 || w.Base64 != nil {
			return errors.New("value: null takes no value")
		}
		*v = NullValue()
	case "integer":
		if err := needValue(); err != nil {
			return err
		}
		s := string(w.Value)
		if len(s) > 0 && s[0] == '"' {
			if err := json.Unmarshal(w.Value, &s); err != nil {
				return fmt.Errorf("value: %w", err)
			}
		}
		if !integerLiteral.MatchString(s) {
			return fmt.Errorf("value: %q isn't an integer", s)
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("value: integer %s doesn't fit in 64 bits", s)
		}
		*v = IntValue(n)
	case "float":
		if err := needValue(); err != nil {
			return err
		}
		switch string(w.Value) {
		case `"Infinity"`:
			*v = FloatValue(math.Inf(1))
			return nil
		case `"-Infinity"`:
			*v = FloatValue(math.Inf(-1))
			return nil
		}
		var f float64
		if err := json.Unmarshal(w.Value, &f); err != nil {
			return fmt.Errorf("value: invalid float %s", w.Value)
		}
		*v = FloatValue(f)
	case "text":
		if err := needValue(); err != nil {
			return err
		}
		var s string
		if err := json.Unmarshal(w.Value, &s); err != nil {
			return fmt.Errorf("value: text must be a string: %w", err)
		}
		*v = TextValue(s)
	case "blob":
		if w.Base64 == nil || len(w.Value) != 0 {
			return errors.New(`value: blob needs "base64" and no "value"`)
		}
		b, err := base64.StdEncoding.DecodeString(*w.Base64)
		if err != nil {
			b, err = base64.RawStdEncoding.DecodeString(*w.Base64)
		}
		if err != nil {
			return fmt.Errorf("value: invalid base64: %w", err)
		}
		*v = BlobValue(b)
	default:
		return fmt.Errorf("value: unknown type %q", w.Type)
	}
	return nil
}

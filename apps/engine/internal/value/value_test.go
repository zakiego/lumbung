package value

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		v    Value
		json string
	}{
		{"null", NullValue(), `{"type":"null"}`},
		{"zero", IntValue(0), `{"type":"integer","value":"0"}`},
		{"beyond 2^53", IntValue(9007199254740993), `{"type":"integer","value":"9007199254740993"}`},
		{"max int64", IntValue(math.MaxInt64), `{"type":"integer","value":"9223372036854775807"}`},
		{"min int64", IntValue(math.MinInt64), `{"type":"integer","value":"-9223372036854775808"}`},
		{"integral float stays float", FloatValue(1), `{"type":"float","value":1}`},
		{"float", FloatValue(1.5), `{"type":"float","value":1.5}`},
		{"tiny float", FloatValue(5e-324), `{"type":"float","value":5e-324}`},
		{"big float", FloatValue(1.7976931348623157e308), `{"type":"float","value":1.7976931348623157e+308}`},
		{"+inf", FloatValue(math.Inf(1)), `{"type":"float","value":"Infinity"}`},
		{"-inf", FloatValue(math.Inf(-1)), `{"type":"float","value":"-Infinity"}`},
		{"text", TextValue("héllo \"x\"\n"), `{"type":"text","value":"héllo \"x\"\n"}`},
		{"empty text", TextValue(""), `{"type":"text","value":""}`},
		{"blob", BlobValue([]byte{0, 1, 2, 255}), `{"type":"blob","base64":"AAEC/w=="}`},
		{"empty blob", BlobValue([]byte{}), `{"type":"blob","base64":""}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.json {
				t.Fatalf("marshal: got %s, want %s", got, c.json)
			}
			var back Value
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatal(err)
			}
			if !back.Equal(c.v) {
				t.Fatalf("round trip: got %#v, want %#v", back, c.v)
			}
		})
	}
}

func TestPlainParams(t *testing.T) {
	cases := []struct {
		in   string
		want Value
	}{
		{`null`, NullValue()},
		{`true`, IntValue(1)},
		{`false`, IntValue(0)},
		{`"x"`, TextValue("x")},
		{`42`, IntValue(42)},
		{`-7`, IntValue(-7)},
		{`9007199254740993`, IntValue(9007199254740993)},
		{`1.0`, FloatValue(1)},
		{`1e3`, FloatValue(1000)},
		{`-0.5`, FloatValue(-0.5)},
		{`{"type":"integer","value":12}`, IntValue(12)},
		{`{"type":"blob","base64":"AAEC"}`, BlobValue([]byte{0, 1, 2})},
		{`{"type":"blob","base64":"AAE"}`, BlobValue([]byte{0, 1})},
	}
	for _, c := range cases {
		var v Value
		if err := json.Unmarshal([]byte(c.in), &v); err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if !v.Equal(c.want) {
			t.Fatalf("%s: got %#v, want %#v", c.in, v, c.want)
		}
	}
}

func TestRejects(t *testing.T) {
	for _, in := range []string{
		`9223372036854775808`,              // integer past int64: no silent float
		`{"type":"integer","value":"1.5"}`, // not an integer
		`{"type":"integer","value":"99999999999999999999"}`,
		`{"type":"integer"}`,                    // missing value
		`{"type":"text","value":5}`,             // text must be a string
		`{"type":"blob","value":"AA"}`,          // blob takes base64
		`{"type":"blob","base64":"!!"}`,         // bad base64
		`{"type":"date","value":"x"}`,           // unknown type
		`{"type":"null","value":"x"}`,           // null with a value
		`{"type":"float","value":"NaN"}`,        // not a number
		`{"type":"text","value":"x","extra":1}`, // unknown field
		`[1]`,                                   // arrays aren't values
	} {
		var v Value
		if err := json.Unmarshal([]byte(in), &v); err == nil {
			t.Errorf("%s: accepted as %#v", in, v)
		}
	}
	if _, err := json.Marshal(FloatValue(math.NaN())); err == nil || !strings.Contains(err.Error(), "NaN") {
		t.Errorf("NaN marshal: got %v", err)
	}
}

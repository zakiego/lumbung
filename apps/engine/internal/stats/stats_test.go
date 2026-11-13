package stats

import (
	"testing"
	"time"
)

func TestRecordAndSummarize(t *testing.T) {
	r := NewRecorder()
	base := time.Date(2026, 1, 1, 10, 0, 30, 0, time.UTC)
	for i := 0; i < 98; i++ {
		r.Record("br_a", base, 3*time.Millisecond, 2, 0, false)
	}
	r.Record("br_a", base.Add(time.Minute), 700*time.Millisecond, 0, 5, true)
	r.Record("br_a", base.Add(time.Minute), 40*time.Second, 0, 0, true)
	r.Record("br_b", base, time.Millisecond, 1, 1, false)

	got := r.Pending("br_a")
	if len(got) != 2 {
		t.Fatalf("pending buckets: %d", len(got))
	}
	s := Summarize(got, base.Add(-time.Minute), base.Add(2*time.Minute), time.Minute)
	if s.Queries != 100 || s.RowsRead != 196 || s.RowsWritten != 5 || s.Errors != 2 {
		t.Fatalf("totals: %+v", s)
	}
	if s.P50MS != 5 || s.P99MS != 1000 {
		t.Fatalf("percentiles: p50=%d p99=%d", s.P50MS, s.P99MS)
	}
	if len(s.Series) != 4 || s.Series[1].Queries != 98 || s.Series[2].Errors != 2 || s.Series[0].Queries != 0 {
		t.Fatalf("series: %+v", s.Series)
	}
	if empty := Summarize(nil, base, base, time.Minute); empty.P50MS != 0 || len(empty.Series) != 1 {
		t.Fatalf("empty: %+v", empty)
	}
}

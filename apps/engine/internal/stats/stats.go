// Package stats counts per-branch activity in one-minute buckets (PLAN 6.9):
// queries, rows read and written, errors, and a duration histogram for
// percentiles. Counters build up in memory and are flushed to meta.db.
package stats

import (
	"context"
	"sync"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// Bounds are the upper edges, in milliseconds, of the duration buckets. A
// last bucket holds everything slower.
var Bounds = []int64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 30000}

func bucketOf(d time.Duration) int {
	ms := d.Milliseconds()
	for i, b := range Bounds {
		if ms < b {
			return i
		}
	}
	return len(Bounds)
}

type key struct {
	branch string
	minute int64
}

// Recorder collects counters until the next flush.
type Recorder struct {
	mu      sync.Mutex
	pending map[key]*meta.StatsBucket
}

// NewRecorder returns an empty recorder.
func NewRecorder() *Recorder { return &Recorder{pending: map[key]*meta.StatsBucket{}} }

// Record counts one request on a branch.
func (r *Recorder) Record(branchID string, at time.Time, d time.Duration, rowsRead, rowsWritten int64, failed bool) {
	minute := at.UTC().Truncate(time.Minute)
	k := key{branchID, minute.Unix()}
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.pending[k]
	if !ok {
		b = &meta.StatsBucket{BranchID: branchID, Minute: minute, Durations: make([]int64, len(Bounds)+1)}
		r.pending[k] = b
	}
	b.Queries++
	b.RowsRead += rowsRead
	b.RowsWritten += rowsWritten
	if failed {
		b.Errors++
	}
	b.Durations[bucketOf(d)]++
}

// Flush writes pending counters to meta.db and drops stored buckets older
// than retention. On failure the counters are kept for the next flush.
func (r *Recorder) Flush(ctx context.Context, store *meta.Store, retention time.Duration) error {
	r.mu.Lock()
	batch := make([]meta.StatsBucket, 0, len(r.pending))
	for _, b := range r.pending {
		batch = append(batch, *b)
	}
	r.pending = map[key]*meta.StatsBucket{}
	r.mu.Unlock()
	if err := store.AddStats(ctx, batch, time.Now().Add(-retention)); err != nil {
		r.mu.Lock()
		for _, b := range batch {
			k := key{b.BranchID, b.Minute.Unix()}
			if cur, ok := r.pending[k]; ok {
				cur.Queries += b.Queries
				cur.RowsRead += b.RowsRead
				cur.RowsWritten += b.RowsWritten
				cur.Errors += b.Errors
				for i := range cur.Durations {
					cur.Durations[i] += b.Durations[i]
				}
			} else {
				bb := b
				r.pending[k] = &bb
			}
		}
		r.mu.Unlock()
		return err
	}
	return nil
}

// Pending returns the unflushed buckets for a branch, so reads include the
// current minute.
func (r *Recorder) Pending(branchID string) []meta.StatsBucket {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []meta.StatsBucket
	for k, b := range r.pending {
		if k.branch == branchID {
			c := *b
			c.Durations = append([]int64(nil), b.Durations...)
			out = append(out, c)
		}
	}
	return out
}

// Point is one step of a summarized series.
type Point struct {
	At          time.Time `json:"at"`
	Queries     int64     `json:"queries"`
	RowsRead    int64     `json:"rows_read"`
	RowsWritten int64     `json:"rows_written"`
	Errors      int64     `json:"errors"`
}

// Summary is a branch's activity over a window.
type Summary struct {
	Since       time.Time `json:"since"`
	Step        string    `json:"step"`
	Queries     int64     `json:"queries"`
	RowsRead    int64     `json:"rows_read"`
	RowsWritten int64     `json:"rows_written"`
	Errors      int64     `json:"errors"`
	P50MS       int64     `json:"p50_ms"`
	P95MS       int64     `json:"p95_ms"`
	P99MS       int64     `json:"p99_ms"`
	Series      []Point   `json:"series"`
}

// Summarize folds buckets into totals, percentiles and a series with one
// point per step, empty steps included.
func Summarize(buckets []meta.StatsBucket, since, until time.Time, step time.Duration) Summary {
	since = since.UTC().Truncate(step)
	s := Summary{Since: since, Step: step.String(), Series: []Point{}}
	hist := make([]int64, len(Bounds)+1)
	points := map[int64]*Point{}
	for t := since; !t.After(until); t = t.Add(step) {
		points[t.Unix()] = &Point{At: t}
	}
	for _, b := range buckets {
		if b.Minute.Before(since) {
			continue
		}
		s.Queries += b.Queries
		s.RowsRead += b.RowsRead
		s.RowsWritten += b.RowsWritten
		s.Errors += b.Errors
		for i := range hist {
			if i < len(b.Durations) {
				hist[i] += b.Durations[i]
			}
		}
		if p, ok := points[b.Minute.UTC().Truncate(step).Unix()]; ok {
			p.Queries += b.Queries
			p.RowsRead += b.RowsRead
			p.RowsWritten += b.RowsWritten
			p.Errors += b.Errors
		}
	}
	for t := since; !t.After(until); t = t.Add(step) {
		s.Series = append(s.Series, *points[t.Unix()])
	}
	s.P50MS, s.P95MS, s.P99MS = percentile(hist, 0.50), percentile(hist, 0.95), percentile(hist, 0.99)
	return s
}

// percentile returns the upper bound of the bucket holding the q-th
// request, in milliseconds. The open last bucket reports twice the last
// bound.
func percentile(hist []int64, q float64) int64 {
	var total int64
	for _, n := range hist {
		total += n
	}
	if total == 0 {
		return 0
	}
	rank := int64(q*float64(total-1)) + 1
	var seen int64
	for i, n := range hist {
		seen += n
		if seen >= rank {
			if i < len(Bounds) {
				return Bounds[i]
			}
			return Bounds[len(Bounds)-1] * 2
		}
	}
	return 0
}

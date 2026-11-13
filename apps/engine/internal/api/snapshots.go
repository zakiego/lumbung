package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// SnapshotOut is a snapshot.
type SnapshotOut struct {
	ID         string    `json:"id"`
	DatabaseID string    `json:"database_id"`
	BranchID   string    `json:"branch_id" doc:"The branch it was taken from. It may since have been deleted."`
	BranchName string    `json:"branch_name" doc:"The branch's name when the snapshot was taken."`
	Kind       string    `json:"kind" enum:"manual,scheduled"`
	Label      string    `json:"label"`
	SizeBytes  int64     `json:"size_bytes"`
	CreatedAt  time.Time `json:"created_at"`
}

func snapshotOut(sn meta.Snapshot) SnapshotOut {
	return SnapshotOut{ID: sn.ID, DatabaseID: sn.DatabaseID, BranchID: sn.BranchID, BranchName: sn.BranchName,
		Kind: sn.Kind, Label: sn.Label, SizeBytes: sn.SizeBytes, CreatedAt: sn.CreatedAt}
}

type snapshotBody struct{ Body SnapshotOut }

type snapshotList struct {
	Body struct {
		Data       []SnapshotOut `json:"data"`
		NextCursor string        `json:"next_cursor"`
	}
}

type listSnapshotsIn struct {
	BranchPath
	PageIn
}

type createSnapshotIn struct {
	BranchPath
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"200"`
	Body           *struct {
		Label string `json:"label,omitempty" maxLength:"200"`
	}
}

type SnapshotPath struct {
	DB       string `path:"db" doc:"Database name or id."`
	Snapshot string `path:"snapshot" doc:"Snapshot id."`
}

type restoreSnapshotIn struct {
	SnapshotPath
	Body struct {
		Name string `json:"name" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" doc:"Name of the new branch the snapshot is restored into."`
	}
}

// ScheduleOut is a branch's snapshot schedule.
type ScheduleOut struct {
	EveryMinutes int        `json:"every_minutes"`
	KeepCount    int        `json:"keep_count" doc:"Scheduled snapshots kept, newest first. 0 keeps any number. Manual snapshots are never removed by retention."`
	KeepDays     int        `json:"keep_days" doc:"Scheduled snapshots older than this are removed. 0 keeps any age."`
	LastRunAt    *time.Time `json:"last_run_at"`
}

func scheduleOut(sc meta.Schedule) ScheduleOut {
	return ScheduleOut{EveryMinutes: int(sc.Every / time.Minute), KeepCount: sc.KeepCount, KeepDays: int(sc.KeepFor / (24 * time.Hour)),
		LastRunAt: sc.LastRunAt}
}

type scheduleBody struct{ Body ScheduleOut }

type setScheduleIn struct {
	BranchPath
	Body struct {
		EveryMinutes int `json:"every_minutes" minimum:"1" maximum:"525600"`
		KeepCount    int `json:"keep_count,omitempty" minimum:"0" maximum:"10000"`
		KeepDays     int `json:"keep_days,omitempty" minimum:"0" maximum:"3650"`
	}
}

func (s *Server) registerSnapshots(api huma.API) {
	huma.Register(api, op("list-snapshots", http.MethodGet, "/v1/databases/{db}/branches/{branch}/snapshots", "List a branch's snapshots", "Snapshots", ""),
		func(ctx context.Context, in *listSnapshotsIn) (*snapshotList, error) {
			d, b, err := s.branch(ctx, principalFrom(ctx), in.DB, in.Branch, auth.AccessRead)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			all, err := s.eng.ListSnapshots(ctx, engine.Target{Database: d.ID, Branch: b.ID})
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			// Newest first: sort by an inverted timestamp.
			items, next, err := page(all, func(sn meta.Snapshot) string {
				return invertedTime(sn.CreatedAt) + sn.ID
			}, in.PageIn)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			out := &snapshotList{}
			out.Body.Data = make([]SnapshotOut, 0, len(items))
			out.Body.NextCursor = next
			for _, sn := range items {
				out.Body.Data = append(out.Body.Data, snapshotOut(sn))
			}
			return out, nil
		})

	huma.Register(api, created(op("create-snapshot", http.MethodPost, "/v1/databases/{db}/branches/{branch}/snapshots", "Take a snapshot of a branch", "Snapshots", "snapshot.create")),
		func(ctx context.Context, in *createSnapshotIn) (*snapshotBody, error) {
			d, b, err := s.branch(ctx, principalFrom(ctx), in.DB, in.Branch, auth.AccessWrite)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			label := ""
			if in.Body != nil {
				label = in.Body.Label
			}
			return idempotent(ctx, s, "create-snapshot", in.IdempotencyKey, struct{ B, L string }{b.ID, label}, func() (*snapshotBody, error) {
				sn, err := s.eng.CreateSnapshot(ctx, engine.Target{Database: d.ID, Branch: b.ID}, label)
				if err != nil {
					return nil, toProblem(ctx, err)
				}
				setTarget(ctx, d.ID+"/"+b.ID+"/"+sn.ID)
				return &snapshotBody{Body: snapshotOut(sn)}, nil
			})
		})

	huma.Register(api, created(op("restore-snapshot", http.MethodPost, "/v1/databases/{db}/snapshots/{snapshot}/restore", "Restore a snapshot into a new branch", "Snapshots", "snapshot.restore")),
		func(ctx context.Context, in *restoreSnapshotIn) (*branchBody, error) {
			p := principalFrom(ctx)
			d, err := s.database(ctx, p, in.DB, auth.AccessWrite)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			b, err := s.eng.RestoreSnapshot(ctx, d.ID, in.Snapshot, in.Body.Name)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, d.ID+"/"+in.Snapshot+"/"+b.ID)
			return &branchBody{Body: s.branchOut(d, b)}, nil
		})

	huma.Register(api, noContent(op("delete-snapshot", http.MethodDelete, "/v1/databases/{db}/snapshots/{snapshot}", "Delete a snapshot", "Snapshots", "snapshot.delete")),
		func(ctx context.Context, in *SnapshotPath) (*struct{}, error) {
			d, err := s.database(ctx, principalFrom(ctx), in.DB, auth.AccessAdmin)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, d.ID+"/"+in.Snapshot)
			if err := s.eng.DeleteSnapshot(ctx, d.ID, in.Snapshot); err != nil {
				return nil, toProblem(ctx, err)
			}
			return nil, nil
		})

	schedPath := "/v1/databases/{db}/branches/{branch}/snapshot-schedule"
	huma.Register(api, op("get-snapshot-schedule", http.MethodGet, schedPath, "Get a branch's snapshot schedule", "Snapshots", ""),
		func(ctx context.Context, in *BranchPath) (*scheduleBody, error) {
			d, b, err := s.branch(ctx, principalFrom(ctx), in.DB, in.Branch, auth.AccessRead)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			sc, ok, err := s.eng.GetSchedule(ctx, engine.Target{Database: d.ID, Branch: b.ID})
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			if !ok {
				return nil, toProblem(ctx, apierr.New(apierr.NotFound, "this branch has no snapshot schedule"))
			}
			return &scheduleBody{Body: scheduleOut(sc)}, nil
		})
	huma.Register(api, op("set-snapshot-schedule", http.MethodPut, schedPath, "Set a branch's snapshot schedule", "Snapshots", "snapshot.schedule"),
		func(ctx context.Context, in *setScheduleIn) (*scheduleBody, error) {
			d, b, err := s.branch(ctx, principalFrom(ctx), in.DB, in.Branch, auth.AccessAdmin)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, d.ID+"/"+b.ID)
			sc, err := s.eng.SetSchedule(ctx, engine.Target{Database: d.ID, Branch: b.ID}, engine.ScheduleOptions{
				Every: time.Duration(in.Body.EveryMinutes) * time.Minute, KeepCount: in.Body.KeepCount,
				KeepFor: time.Duration(in.Body.KeepDays) * 24 * time.Hour,
			})
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &scheduleBody{Body: scheduleOut(sc)}, nil
		})
	huma.Register(api, noContent(op("delete-snapshot-schedule", http.MethodDelete, schedPath, "Remove a branch's snapshot schedule", "Snapshots", "snapshot.unschedule")),
		func(ctx context.Context, in *BranchPath) (*struct{}, error) {
			d, b, err := s.branch(ctx, principalFrom(ctx), in.DB, in.Branch, auth.AccessAdmin)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, d.ID+"/"+b.ID)
			if err := s.eng.DeleteSchedule(ctx, engine.Target{Database: d.ID, Branch: b.ID}); err != nil {
				return nil, toProblem(ctx, err)
			}
			return nil, nil
		})
}

// invertedTime sorts newer times first as text.
func invertedTime(t time.Time) string {
	const far = 1 << 62
	n := far - t.UnixNano()
	b := []byte("0000000000000000000")
	for i := len(b) - 1; i >= 0 && n > 0; i-- {
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b)
}

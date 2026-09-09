package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/piglor/loom/services/loom/ent"
	"github.com/piglor/loom/services/loom/ent/event"
	"github.com/piglor/loom/services/loom/ent/goal"
	"github.com/piglor/loom/services/loom/ent/run"
	"github.com/piglor/loom/services/loom/ent/session"
	"github.com/piglor/loom/services/loom/ent/wait"
	"github.com/piglor/loom/services/loom/ent/worker"
)

// Store is the transaction seam. Generated builders own ordinary persistence;
// SQL is restricted to migrations, database time and coordination locks.
type Store struct {
	DB           *sql.DB
	Organization string
	EnableCodex  bool
}

type GoalSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type transaction struct {
	client *ent.Client
	sql    *sql.Tx
	now    time.Time
}

// Ent may start a transaction internally for a generated mutation. Those
// operations join this Store transaction; only Store.tx may commit or roll back.
type joinedDriver struct{ dialect.Driver }

func (d joinedDriver) Tx(context.Context) (dialect.Tx, error) { return dialect.NopTx(d), nil }
func (d joinedDriver) Close() error                           { return nil }

func (s *Store) tx(ctx context.Context, f func(*transaction) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var now time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return err
	}
	driver := entsql.NewDriver(dialect.Postgres, entsql.Conn{ExecQuerier: tx})
	if err = f(&transaction{ent.NewClient(ent.Driver(joinedDriver{driver})), tx, now}); err != nil {
		return err
	}
	return tx.Commit()
}

func jsonObject(v any) map[string]any {
	// Callers pass validated domain structs with no fallible JSON field types.
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var result map[string]any
	if err = json.Unmarshal(b, &result); err != nil {
		panic(err)
	}
	return result
}

func audit(ctx context.Context, t *transaction, id, action string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	return t.client.Audit.Create().SetGoalID(id).SetAction(action).SetDetails(details).SetRecordedAt(t.now).Exec(ctx)
}

func transition(ctx context.Context, t *transaction, id, state string) error {
	q := t.client.Goal.UpdateOneID(id).SetState(state).SetUpdatedAt(t.now)
	if state == "COMPLETED" || state == "FAILED" || state == "CANCELLED" {
		q.SetEndedAt(t.now)
	} else {
		q.ClearEndedAt()
	}
	if err := q.Exec(ctx); err != nil {
		return err
	}
	return audit(ctx, t, id, "state_changed", map[string]any{"state": state})
}

func enqueue(ctx context.Context, t *transaction, id, kind string) error {
	return t.client.Outbox.Create().SetGoalID(id).SetKind(kind).SetAvailableAt(t.now).
		OnConflictColumns("goal_id", "kind").Update(func(u *ent.OutboxUpsert) { u.ClearDeliveredAt().SetAvailableAt(t.now) }).Exec(ctx)
}

func (s *Store) Create(ctx context.Context, r CreateGoal) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	if r.Runtime == "codex-container" && !s.EnableCodex {
		return "", conflict("Contained runtime is disabled")
	}
	id, runID := ID(), ID()
	err := s.tx(ctx, func(t *transaction) error {
		workerID := "demo-local"
		if r.WorkerID != nil {
			workerID = *r.WorkerID
			w, err := t.client.Worker.Query().Where(worker.IDEQ(workerID), worker.OrganizationEQ(s.Organization), worker.RevokedAtIsNil()).ForUpdate().Only(ctx)
			if ent.IsNotFound(err) {
				return conflict("Worker unavailable")
			}
			if err != nil {
				return err
			}
			if w.Runtime != r.Runtime {
				return conflict("Unauthorized runtime")
			}
			if r.CompletionCondition != nil && !contains(w.Capabilities, "event-driven-v1") {
				return conflict("Repeatable Goal requires protocol 2")
			}
			if w.Runtime == "codex-container" {
				bound, err := t.client.Session.Query().Where(session.WorkerIDEQ(workerID)).Exist(ctx)
				if err != nil {
					return err
				}
				if bound {
					return conflict("Contained preview worker is restricted to one Session")
				}
			}
		}
		final := r.Condition
		p := Policy{Runtime: r.Runtime, MaxAttempts: 2}
		if r.CompletionCondition != nil {
			final = *r.CompletionCondition
			p.Lifecycle = "event-driven-v1"
			p.MaxAttempts = 100
			if r.MaxAttempts != nil {
				p.MaxAttempts = *r.MaxAttempts
			}
		}
		if err := t.client.Goal.Create().SetID(id).SetOrganization(s.Organization).SetTitle(r.Title).SetObjective(r.Objective).SetState("READY").SetCompletionCriteria(map[string]any{"event_matches": jsonObject(final)}).SetCreatedAt(t.now).SetUpdatedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		if err := t.client.Run.Create().SetID(runID).SetGoalID(id).SetPolicy(jsonObject(p)).SetCreatedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		if err := t.client.Session.Create().SetRunID(runID).SetWorkerID(workerID).SetRuntime(r.Runtime).Exec(ctx); err != nil {
			return err
		}
		if err := t.client.Wait.Create().SetGoalID(id).SetCondition(jsonObject(r.Condition)).Exec(ctx); err != nil {
			return err
		}
		if err := audit(ctx, t, id, "goal_created", map[string]any{"runtime": r.Runtime}); err != nil {
			return err
		}
		return enqueue(ctx, t, id, "start")
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

// GoalRuntime returns the operator-authorized runtime for an active tenant Goal.
// Integration adapters use it to apply provider-specific privileged wake policy.
func (s *Store) GoalRuntime(ctx context.Context, id string) (string, error) {
	if !ValidID(id) {
		return "", invalid("Invalid Goal ID")
	}
	var runtime string
	err := s.tx(ctx, func(t *transaction) error {
		g, err := t.client.Goal.Query().Where(goal.IDEQ(strings.ToLower(id)), goal.OrganizationEQ(s.Organization)).Only(ctx)
		if ent.IsNotFound(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		r, err := t.client.Run.Query().Where(run.GoalIDEQ(g.ID)).Only(ctx)
		if err != nil {
			return err
		}
		se, err := t.client.Session.Query().Where(session.RunIDEQ(r.ID)).Only(ctx)
		if err == nil {
			runtime = se.Runtime
		}
		return err
	})
	return runtime, err
}

func (s *Store) ListGoals(ctx context.Context) ([]json.RawMessage, error) {
	result := []json.RawMessage{}
	err := s.tx(ctx, func(t *transaction) error {
		rows, err := t.client.Goal.Query().Where(goal.OrganizationEQ(s.Organization)).Order(ent.Desc(goal.FieldCreatedAt)).Limit(100).All(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			value, err := json.Marshal(GoalSummary{ID: row.ID, Title: row.Title, State: row.State, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
			if err != nil {
				return err
			}
			result = append(result, value)
		}
		return nil
	})
	return result, err
}

type EventResult struct {
	ID          string `json:"id"`
	Disposition string `json:"disposition"`
}

func eventLock(ctx context.Context, t *transaction, organization, source, delivery string) error {
	key, _ := canonicalJSON([]string{organization, source, delivery}, true)
	_, err := t.sql.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", string(key))
	return err
}

// Receive accepts an already-authorized operator event. Integration adapters
// must authenticate and authorize external receipts before invoking this seam.
func (s *Store) Receive(ctx context.Context, e Event) (EventResult, error) {
	var result EventResult
	if err := e.Validate(); err != nil {
		return result, err
	}
	err := s.tx(ctx, func(t *transaction) error { var err error; result, err = s.receive(ctx, t, e); return err })
	if err != nil {
		return EventResult{}, err
	}
	return result, nil
}

func (s *Store) receive(ctx context.Context, t *transaction, e Event) (EventResult, error) {
	result := EventResult{ID: ID(), Disposition: "unknown_goal"}
	body, err := canonicalJSON(e, false)
	if err != nil {
		return result, err
	}
	digest := hash(body)
	if err = eventLock(ctx, t, s.Organization, e.Source, e.DeliveryID); err != nil {
		return result, err
	}
	old, err := t.client.Event.Query().Where(event.OrganizationEQ(s.Organization), event.SourceEQ(e.Source), event.DeliveryIDEQ(e.DeliveryID)).Only(ctx)
	if err == nil {
		if old.Digest != digest {
			return result, conflict("Delivery ID reused with different content")
		}
		return EventResult{old.ID, "duplicate"}, nil
	}
	if !ent.IsNotFound(err) {
		return result, err
	}
	g, err := t.client.Goal.Query().Where(goal.IDEQ(e.GoalID), goal.OrganizationEQ(s.Organization)).ForUpdate().Only(ctx)
	exists := err == nil
	if err != nil && !ent.IsNotFound(err) {
		return result, err
	}
	var w *ent.Wait
	if exists {
		w, err = t.client.Wait.Query().Where(wait.GoalIDEQ(g.ID)).Only(ctx)
		if err != nil {
			return result, err
		}
		var condition Condition
		b, _ := json.Marshal(w.Condition)
		if err = json.Unmarshal(b, &condition); err != nil {
			return result, err
		}
		switch {
		case terminal(g.State):
			result.Disposition = "inactive"
		case e.Generation != w.Generation || e.Condition != condition:
			result.Disposition = "mismatch"
		case w.SatisfiedAt != nil:
			result.Disposition = "already_satisfied"
		default:
			result.Disposition = "accepted"
		}
	}
	if err = t.client.Event.Create().SetID(result.ID).SetOrganization(s.Organization).SetSource(e.Source).SetDeliveryID(e.DeliveryID).SetDigest(digest).SetBody(jsonObject(e)).SetDisposition(result.Disposition).SetReceivedAt(t.now).Exec(ctx); err != nil {
		return result, err
	}
	if exists {
		if err = audit(ctx, t, e.GoalID, "event_received", map[string]any{"event_id": result.ID, "disposition": result.Disposition}); err != nil {
			return result, err
		}
	}
	if result.Disposition == "accepted" {
		if err = t.client.Wait.UpdateOneID(w.ID).SetSatisfiedAt(t.now).SetEventID(result.ID).Exec(ctx); err != nil {
			return result, err
		}
		if g.State == "WAITING" && str(g.WaitingReason) != "worker" {
			if err = transition(ctx, t, g.ID, "READY"); err != nil {
				return result, err
			}
		}
		err = enqueue(ctx, t, g.ID, "wake")
	}
	return result, err
}

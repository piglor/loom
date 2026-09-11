package control

import (
	"context"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
)

func goalStatePhase(t *testing.T, store *Store, id string) (string, int) {
	t.Helper()
	ctx := context.Background()
	var state string
	var phase int
	if err := store.tx(ctx, func(tx *transaction) error {
		row, err := tx.client.Goal.Get(ctx, id)
		if err == nil {
			state, phase = row.State, row.Phase
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return state, phase
}

func TestOutboxTransferIsTenantScopedAndIdempotent(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	id, err := store.Create(ctx, CreateGoal{Title: "Dispatch", Objective: "Transfer exactly once", Condition: Condition{"timer", "expired", "timer-1", "1"}})
	if err != nil {
		t.Fatal(err)
	}
	foreign := &Store{DB: store.DB, Organization: "another-org"}
	if items, err := foreign.OutboxBatch(ctx); err != nil || len(items) != 0 {
		t.Fatalf("foreign batch=%v err=%v", items, err)
	}
	items, err := store.OutboxBatch(ctx)
	if err != nil || len(items) != 1 || items[0].GoalID != id || items[0].Kind != "start" {
		t.Fatalf("batch=%v err=%v", items, err)
	}
	if err = store.MarkOutboxDelivered(ctx, items[0], "hatchet-run-1"); err != nil {
		t.Fatal(err)
	}
	if retry, err := store.OutboxBatch(ctx); err != nil || len(retry) != 0 {
		t.Fatalf("delivered item replayed: %v %v", retry, err)
	}
	if err = store.MarkOutboxDelivered(ctx, items[0], "hatchet-run-duplicate"); err != nil {
		t.Fatal("delivery acknowledgement must be idempotent", err)
	}
}

func TestDefaultFiniteDemoDispatchesAndResumesWithoutModel(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	condition := Condition{"human", "approved", ID(), "1"}
	id, err := store.Create(ctx, CreateGoal{Title: "Finite default", Objective: "Wait without model", Condition: condition})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx, id); err != nil {
		t.Fatal(err)
	}
	state, phase := goalStatePhase(t, store, id)
	if state != "WAITING" || phase != 1 {
		t.Fatalf("after initial dispatch: state=%s phase=%d", state, phase)
	}
	if _, err = store.Receive(ctx, Event{Condition: condition, DeliveryID: ID(), GoalID: id, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx, id); err != nil {
		t.Fatal(err)
	}
	state, phase = goalStatePhase(t, store, id)
	if state != "COMPLETED" || phase != 2 {
		t.Fatalf("after continuation: state=%s phase=%d", state, phase)
	}
}

func TestOutboxBatchFiltersIneligibleWakeRowsBeforeLimit(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	for i := 0; i < 51; i++ {
		id, err := store.Create(ctx, CreateGoal{Title: "Historical", Objective: "Must not starve current transfers", Condition: Condition{"timer", "expired", ID(), "1"}})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.tx(ctx, func(tx *transaction) error {
			runRow, err := runRow(ctx, tx, id)
			if err != nil {
				return err
			}
			if _, err := tx.client.Outbox.Delete().Where(func(s *entsql.Selector) {
				s.Where(entsql.EQ(s.C("goal_id"), id))
			}).Exec(ctx); err != nil {
				return err
			}
			if err := tx.client.Goal.UpdateOneID(id).SetState("WAITING").Exec(ctx); err != nil {
				return err
			}
			return tx.client.Outbox.Create().SetGoalID(id).SetRunID(runRow.ID).SetKind("wake").SetAvailableAt(tx.now.Add(-time.Hour)).SetDeliveredAt(tx.now).Exec(ctx)
		}); err != nil {
			t.Fatal(err)
		}
	}
	current, err := store.Create(ctx, CreateGoal{Title: "Current", Objective: "Must be returned", Condition: Condition{"timer", "expired", ID(), "1"}})
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.OutboxBatch(ctx)
	if err != nil || len(items) != 1 || items[0].GoalID != current || items[0].Kind != "start" {
		t.Fatalf("batch=%v err=%v", items, err)
	}
}

func TestOutboxAcknowledgementCannotCrossTenantOrChangeGoal(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	id, err := store.Create(ctx, CreateGoal{Title: "Owned", Objective: "Stay tenant scoped", Condition: Condition{"timer", "expired", ID(), "1"}})
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.OutboxBatch(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("batch=%v err=%v", items, err)
	}
	foreign := &Store{DB: store.DB, Organization: "another-org"}
	if err = foreign.MarkOutboxDelivered(ctx, items[0], "forged"); err != nil {
		t.Fatal(err)
	}
	if err = foreign.MarkOutboxFailed(ctx, items[0]); err != nil {
		t.Fatal(err)
	}
	forged := items[0]
	forged.GoalID = ID()
	if err = store.MarkOutboxDelivered(ctx, forged, "forged"); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.OutboxBatch(ctx)
	if err != nil || len(remaining) != 1 || remaining[0].GoalID != id {
		t.Fatalf("outbox was changed across trust boundary: %v %v", remaining, err)
	}
	var workflow *string
	if err = store.tx(ctx, func(tx *transaction) error {
		run, err := tx.client.Run.Query().Where(func(s *entsql.Selector) {
			s.Where(entsql.EQ(s.C("goal_id"), id))
		}).Only(ctx)
		if err == nil {
			workflow = run.WorkflowID
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if workflow != nil {
		t.Fatalf("workflow ID changed: %q", *workflow)
	}
}

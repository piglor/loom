package control

import (
	"context"

	"github.com/piglor/loom/services/loom/ent"
	"github.com/piglor/loom/services/loom/ent/attempt"
	"github.com/piglor/loom/services/loom/ent/command"
	"github.com/piglor/loom/services/loom/ent/wait"
	"github.com/piglor/loom/services/loom/ent/worker"
)

// Cancel closes a Goal only when Loom has no admitted execution whose stop is
// unknown. Delivery of a cancellation request is not proof that a runtime died.
func (s *Store) Cancel(ctx context.Context, id string) error {
	if !ValidID(id) {
		return invalid("Invalid Goal ID")
	}
	return s.tx(ctx, func(t *transaction) error {
		g, err := s.goalRow(ctx, t, id)
		if err != nil {
			return err
		}
		active, err := t.client.Attempt.Query().Where(attempt.GoalIDEQ(g.ID), attempt.StateIn("RUNNING", "UNKNOWN")).Exist(ctx)
		if err != nil {
			return err
		}
		if g.State == "RUNNING" || active {
			return conflict("Runtime stop is unconfirmed; reconcile before cancellation")
		}
		if terminal(g.State) {
			return nil
		}
		if err = t.client.Command.Update().Where(command.GoalIDEQ(g.ID), command.StateEQ("QUEUED")).SetState("STOPPED").SetStoppedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		if err = t.client.Attempt.Update().Where(attempt.GoalIDEQ(g.ID), attempt.StateEQ("QUEUED")).SetState("STOPPED").SetOutcome("cancelled_unclaimed").SetDurationMs(0).SetStoppedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		if err = transition(ctx, t, g.ID, "CANCELLED"); err != nil {
			return err
		}
		if err = t.client.Wait.Update().Where(wait.GoalIDEQ(g.ID)).SetClosedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		return enqueue(ctx, t, g.ID, "wake")
	})
}

func (s *Store) RevokeWorker(ctx context.Context, id string) error {
	if !ValidID(id) {
		return invalid("Invalid Worker ID")
	}
	return s.tx(ctx, func(t *transaction) error {
		w, err := t.client.Worker.Query().Where(worker.IDEQ(id), worker.OrganizationEQ(s.Organization), worker.RevokedAtIsNil()).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		return t.client.Worker.UpdateOneID(w.ID).SetRevokedAt(t.now).Exec(ctx)
	})
}

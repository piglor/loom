package control

import (
	"context"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/piglor/loom/services/loom/ent"
	"github.com/piglor/loom/services/loom/ent/goal"
	"github.com/piglor/loom/services/loom/ent/outbox"
	"github.com/piglor/loom/services/loom/ent/run"
	"github.com/piglor/loom/services/loom/ent/workflowversion"
)

type OutboxItem struct {
	ID                    string
	GoalID                string
	RunID                 string
	WorkflowVersionID     string
	WorkflowDefinitionID  string
	WorkflowVersionNumber int
	WorkflowSpec          WorkflowSpec
	StepKey               string
	Kind                  string
	AvailableAt           time.Time
}

func tenantGoals(organization string) func(*entsql.Selector) {
	return func(q *entsql.Selector) {
		g := entsql.Table(goal.Table)
		q.Where(entsql.In(q.C(outbox.FieldGoalID), entsql.Select(g.C(goal.FieldID)).From(g).Where(entsql.EQ(g.C(goal.FieldOrganization), organization))))
	}
}

func readyTenantGoals(organization string) func(*entsql.Selector) {
	return func(q *entsql.Selector) {
		g := entsql.Table(goal.Table)
		q.Where(entsql.In(q.C(outbox.FieldGoalID), entsql.Select(g.C(goal.FieldID)).From(g).Where(entsql.And(
			entsql.EQ(g.C(goal.FieldOrganization), organization),
			entsql.EQ(g.C(goal.FieldState), "READY"),
		))))
	}
}

// OutboxBatch returns only transfer intents owned by this Store's tenant. A
// delivered wake remains eligible while its Goal is READY, making a lost
// Hatchet signal recoverable without launching an execution itself.
func (s *Store) OutboxBatch(ctx context.Context) ([]OutboxItem, error) {
	items := []OutboxItem{}
	err := s.tx(ctx, func(t *transaction) error {
		rows, err := t.client.Outbox.Query().Where(
			tenantGoals(s.Organization),
			outbox.AvailableAtLTE(t.now),
			outbox.Or(
				outbox.DeliveredAtIsNil(),
				outbox.And(outbox.KindEQ("wake"), readyTenantGoals(s.Organization)),
			),
		).Order(ent.Asc(outbox.FieldAvailableAt), ent.Asc(outbox.FieldID)).Limit(50).All(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			runRow, runErr := t.client.Run.Query().Where(run.GoalIDEQ(row.GoalID), run.ParentRunIDIsNil()).Only(ctx)
			if runErr != nil {
				return runErr
			}
			item := OutboxItem{ID: row.ID, GoalID: row.GoalID, RunID: runRow.ID, Kind: row.Kind, AvailableAt: row.AvailableAt}
			if runRow.WorkflowVersionID != nil {
				item.WorkflowVersionID = *runRow.WorkflowVersionID
				versionRow, versionErr := t.client.WorkflowVersion.Query().Where(workflowversion.IDEQ(item.WorkflowVersionID), workflowversion.OrganizationEQ(s.Organization)).Only(ctx)
				if versionErr != nil {
					return versionErr
				}
				item.WorkflowDefinitionID = versionRow.DefinitionID
				item.WorkflowVersionNumber = versionRow.Version
				item.WorkflowSpec, versionErr = decodeSpec(versionRow.Spec)
				if versionErr != nil {
					return versionErr
				}
			}
			if runRow.CurrentStepKey != nil {
				item.StepKey = *runRow.CurrentStepKey
			}
			items = append(items, item)
		}
		return nil
	})
	return items, err
}

func (s *Store) MarkOutboxDelivered(ctx context.Context, item OutboxItem, workflowID string) error {
	return s.tx(ctx, func(t *transaction) error {
		updated, err := t.client.Outbox.Update().Where(
			outbox.IDEQ(item.ID),
			outbox.GoalIDEQ(item.GoalID),
			outbox.AvailableAtEQ(item.AvailableAt),
			outbox.DeliveredAtIsNil(),
			tenantGoals(s.Organization),
		).SetDeliveredAt(t.now).SetAvailableAt(t.now.Add(5 * time.Second)).Save(ctx)
		if err != nil || updated == 0 || workflowID == "" {
			return err
		}
		return t.client.Run.Update().Where(run.GoalIDEQ(item.GoalID), run.ParentRunIDIsNil()).SetWorkflowID(workflowID).SetOrchestrationReference(workflowID).Exec(ctx)
	})
}

func (s *Store) MarkOutboxFailed(ctx context.Context, item OutboxItem) error {
	return s.tx(ctx, func(t *transaction) error {
		return t.client.Outbox.Update().Where(
			outbox.IDEQ(item.ID),
			outbox.GoalIDEQ(item.GoalID),
			outbox.AvailableAtEQ(item.AvailableAt),
			tenantGoals(s.Organization),
		).AddFailures(1).SetAvailableAt(t.now.Add(5 * time.Second)).Exec(ctx)
	})
}

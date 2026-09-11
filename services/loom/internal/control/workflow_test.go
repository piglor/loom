package control

import (
	"context"
	"strings"
	"testing"

	"github.com/piglor/loom/services/loom/ent/goal"
	"github.com/piglor/loom/services/loom/ent/outbox"
	"github.com/piglor/loom/services/loom/ent/run"
	"github.com/piglor/loom/services/loom/ent/workflowsteprun"
)

func validWorkflowSpec(trigger WorkflowTrigger) WorkflowSpec {
	return WorkflowSpec{
		SchemaVersion: 1,
		Triggers:      []WorkflowTrigger{trigger},
		Steps: []WorkflowStep{
			{Key: "review", Name: "Review change", Type: "agent", Config: map[string]any{"runtime": "demo"}},
			{Key: "done", Name: "Complete goal", Type: "complete", Config: map[string]any{}},
		},
		Edges: []WorkflowEdge{{From: "review", To: "done", Outcome: "success"}},
	}
}

func TestWorkflowValidationRejectsCyclesAndDisconnectedSteps(t *testing.T) {
	spec := validWorkflowSpec(WorkflowTrigger{Type: "manual"})
	spec.Steps = append(spec.Steps, WorkflowStep{Key: "orphan", Name: "Orphan", Type: "condition", Config: map[string]any{}})
	spec.Edges = append(spec.Edges, WorkflowEdge{From: "done", To: "review", Outcome: "success"})
	result := ValidateWorkflowSpec(spec)
	if result.Valid || len(result.Errors) == 0 {
		t.Fatalf("invalid graph accepted: %#v", result)
	}
}

func TestPublishedWorkflowIsImmutableAndManualStartUsesPinnedVersion(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	created, err := store.CreateWorkflow(ctx, CreateWorkflow{Name: "Manual review", Description: "Review the requested change", Spec: validWorkflowSpec(WorkflowTrigger{Type: "manual"})})
	if err != nil {
		t.Fatal(err)
	}
	published, err := store.PublishWorkflow(ctx, created.ID)
	if err != nil || published.Version != 1 {
		t.Fatalf("publish=%#v err=%v", published, err)
	}
	updated := UpdateWorkflow{Name: created.Name, Description: "A changed draft", Spec: validWorkflowSpec(WorkflowTrigger{Type: "manual"})}
	if _, err = store.UpdateWorkflow(ctx, created.ID, updated); err != nil {
		t.Fatal(err)
	}
	goalID, err := store.StartWorkflow(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx, goalID); err != nil {
		t.Fatal(err)
	}
	if err = store.tx(ctx, func(tx *transaction) error {
		row, queryErr := tx.client.Run.Query().Where(run.GoalIDEQ(goalID)).Only(ctx)
		if queryErr != nil {
			return queryErr
		}
		if row.WorkflowVersionID == nil || *row.WorkflowVersionID != published.VersionID || row.CurrentStepKey == nil || *row.CurrentStepKey != "done" || row.State != "succeeded" {
			t.Fatalf("run was not pinned to published version: %#v", row)
		}
		goalRow, queryErr := tx.client.Goal.Query().Where(goal.IDEQ(goalID)).Only(ctx)
		if queryErr == nil && goalRow.State != "COMPLETED" {
			t.Fatalf("workflow did not complete the goal: %s", goalRow.State)
		}
		count, queryErr := tx.client.WorkflowStepRun.Query().Where(workflowsteprun.RunIDEQ(row.ID)).Count(ctx)
		if queryErr == nil && count != 2 {
			t.Fatalf("step runs=%d", count)
		}
		return queryErr
	}); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationEventStartsConfiguredWorkflowInsteadOfWakingPlugin(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	credential := IntegrationCredentialRecord{ID: ID(), PluginID: "github", Label: "App", SecretReference: "organizations/test/plugins/github/credentials/workflow", SecretVersion: 1, State: "active"}
	if err := store.CreateIntegrationCredential(ctx, credential); err != nil {
		t.Fatal(err)
	}
	instanceID, err := store.UpsertIntegrationInstance(ctx, IntegrationInstanceRecord{CredentialID: credential.ID, PluginID: "github", ExternalInstanceID: "42", RoutingIdentity: "installation:42", AccountID: "7", AccountLabel: "piglor", RepositorySelection: "selected", Metadata: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	condition := Condition{Source: "github", Type: "workflow.completed", Resource: "repository/run", Version: strings.Repeat("a", 40)}
	spec := validWorkflowSpec(WorkflowTrigger{Type: "integration_event", IntegrationInstanceID: instanceID, Source: "github", EventType: condition.Type, Resource: "*", Version: "*"})
	definition, err := store.CreateWorkflow(ctx, CreateWorkflow{Name: "CI review", Description: "Review after CI", Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishWorkflow(ctx, definition.ID); err != nil {
		t.Fatal(err)
	}
	// Republishing replaces the active start binding; events must not fan out
	// into every historical immutable version.
	if _, err = store.PublishWorkflow(ctx, definition.ID); err != nil {
		t.Fatal(err)
	}
	result, err := store.ReceiveIntegration(ctx, IntegrationReceipt{Source: "github", Instance: "installation:42", DeliveryID: ID(), Condition: &condition, Details: map[string]any{"trust": "signed"}, RawBody: []byte("verified")})
	if err != nil || result.Disposition != "workflow_started" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if err = store.tx(ctx, func(tx *transaction) error {
		goals, queryErr := tx.client.Goal.Query().Where(goal.OrganizationEQ(store.Organization), goal.TitleEQ("CI review")).All(ctx)
		if queryErr != nil {
			return queryErr
		}
		if len(goals) != 1 {
			t.Fatalf("workflow goals=%d", len(goals))
		}
		items, queryErr := tx.client.Outbox.Query().Where(outbox.GoalIDEQ(goals[0].ID)).All(ctx)
		if queryErr == nil && (len(items) != 1 || items[0].Kind != "agent_step_ready") {
			t.Fatalf("workflow intent=%#v", items)
		}
		return queryErr
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUnboundIntegrationEventOnlyRecordsEvidence(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	condition := Condition{Source: "approval", Type: "approved", Resource: "request-1", Version: "1"}
	result, err := store.ReceiveIntegration(ctx, IntegrationReceipt{Source: condition.Source, Instance: "system:one", DeliveryID: ID(), Condition: &condition, RawBody: []byte("verified")})
	if err != nil || result.Disposition != "ignored" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestEarlyIntegrationEventResumesConfiguredWorkflowWaitWithoutStartingAnotherGoal(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	credential := IntegrationCredentialRecord{ID: ID(), PluginID: "github", Label: "App", SecretReference: "organizations/test/plugins/github/credentials/wait", SecretVersion: 1, State: "active"}
	if err := store.CreateIntegrationCredential(ctx, credential); err != nil {
		t.Fatal(err)
	}
	instanceID, err := store.UpsertIntegrationInstance(ctx, IntegrationInstanceRecord{CredentialID: credential.ID, PluginID: "github", ExternalInstanceID: "42", RoutingIdentity: "installation:42", AccountID: "7", AccountLabel: "piglor", RepositorySelection: "selected", Metadata: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	condition := Condition{Source: "github", Type: "workflow.completed", Resource: "repository/run", Version: strings.Repeat("b", 40)}
	spec := WorkflowSpec{
		SchemaVersion: 1,
		Triggers:      []WorkflowTrigger{{Type: "manual"}},
		Steps: []WorkflowStep{
			{Key: "review", Name: "Review", Type: "agent", Config: map[string]any{"runtime": "demo"}},
			{Key: "ci", Name: "Wait for CI", Type: "wait_event", Config: map[string]any{"integration_instance_id": instanceID, "condition": jsonObject(condition)}},
			{Key: "done", Name: "Complete", Type: "complete", Config: map[string]any{}},
		},
		Edges: []WorkflowEdge{{From: "review", To: "ci", Outcome: "success"}, {From: "ci", To: "done", Outcome: "success"}},
	}
	definition, err := store.CreateWorkflow(ctx, CreateWorkflow{Name: "Wait for CI", Description: "Resume only this workflow", Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishWorkflow(ctx, definition.ID); err != nil {
		t.Fatal(err)
	}
	result, err := store.ReceiveIntegration(ctx, IntegrationReceipt{Source: "github", Instance: "installation:42", DeliveryID: ID(), Condition: &condition, RawBody: []byte("verified")})
	if err != nil || result.Disposition != "ignored" {
		t.Fatalf("early result=%#v err=%v", result, err)
	}
	goalID, err := store.StartWorkflow(ctx, definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx, goalID); err != nil {
		t.Fatal(err)
	}
	if err = store.tx(ctx, func(tx *transaction) error {
		count, queryErr := tx.client.Goal.Query().Where(goal.OrganizationEQ(store.Organization)).Count(ctx)
		if queryErr == nil && count != 1 {
			t.Fatalf("resume created %d goals", count)
		}
		row, queryErr := tx.client.Goal.Get(ctx, goalID)
		if queryErr == nil && row.State != "COMPLETED" {
			t.Fatalf("resumed goal state=%s", row.State)
		}
		return queryErr
	}); err != nil {
		t.Fatal(err)
	}
}

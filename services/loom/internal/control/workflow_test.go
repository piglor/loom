package control

import (
	"context"
	"strings"
	"testing"

	"github.com/piglor/loom/services/loom/ent/goal"
	"github.com/piglor/loom/services/loom/ent/outbox"
	"github.com/piglor/loom/services/loom/ent/run"
	"github.com/piglor/loom/services/loom/ent/waithistory"
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

func TestConditionStepBranchesOnPinnedWorkflowContext(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	spec := WorkflowSpec{
		SchemaVersion: 1,
		Triggers:      []WorkflowTrigger{{Type: "manual"}},
		Steps: []WorkflowStep{
			{Key: "agent", Name: "Prepare", Type: "agent", Config: map[string]any{"runtime": "demo"}},
			{Key: "branch", Name: "Approved?", Type: "condition", Config: map[string]any{"path": "trigger.condition.type", "equals": "workflow.manual"}},
			{Key: "yes", Name: "Approved", Type: "complete", Config: map[string]any{}},
			{Key: "no", Name: "Rejected", Type: "complete", Config: map[string]any{}},
		},
		Edges: []WorkflowEdge{{From: "agent", To: "branch", Outcome: "success"}, {From: "branch", To: "yes", Outcome: "success"}, {From: "branch", To: "no", Outcome: "failure"}},
	}
	definition, err := store.CreateWorkflow(ctx, CreateWorkflow{Name: "Condition branch", Description: "Branch from verified context", Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishWorkflow(ctx, definition.ID); err != nil {
		t.Fatal(err)
	}
	goalID, err := store.StartWorkflow(ctx, definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx, goalID); err != nil {
		t.Fatal(err)
	}
	if err = store.tx(ctx, func(tx *transaction) error {
		g, queryErr := tx.client.Goal.Get(ctx, goalID)
		if queryErr != nil {
			return queryErr
		}
		if g.State != "COMPLETED" {
			t.Fatalf("condition branch did not complete: %s", g.State)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSubflowCreatesPinnedChildRunAndResumesParent(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	childSpec := validWorkflowSpec(WorkflowTrigger{Type: "manual"})
	child, err := store.CreateWorkflow(ctx, CreateWorkflow{Name: "Child workflow", Description: "Reusable child", Spec: childSpec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishWorkflow(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	parentSpec := WorkflowSpec{
		SchemaVersion: 1,
		Triggers:      []WorkflowTrigger{{Type: "manual"}},
		Steps: []WorkflowStep{
			{Key: "start", Name: "Start", Type: "agent", Config: map[string]any{"runtime": "demo"}},
			{Key: "child", Name: "Run child", Type: "subflow", Config: map[string]any{"workflow_definition_id": child.ID}},
			{Key: "done", Name: "Complete", Type: "complete", Config: map[string]any{}},
		},
		Edges: []WorkflowEdge{{From: "start", To: "child", Outcome: "success"}, {From: "child", To: "done", Outcome: "success"}},
	}
	parent, err := store.CreateWorkflow(ctx, CreateWorkflow{Name: "Parent workflow", Description: "Invokes child", Spec: parentSpec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishWorkflow(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	goalID, err := store.StartWorkflow(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx, goalID); err != nil {
		t.Fatal(err)
	}
	items, err := store.OutboxBatch(ctx)
	if err != nil || len(items) == 0 {
		t.Fatalf("child intent=%v err=%v", items, err)
	}
	var childRun string
	for _, item := range items {
		if item.RunID != "" {
			childRun = item.RunID
		}
	}
	if childRun == "" {
		t.Fatal("missing child run intent")
	}
	if err = store.DispatchRun(ctx, goalID, childRun); err != nil {
		t.Fatal(err)
	}
	if err = store.tx(ctx, func(tx *transaction) error {
		g, queryErr := tx.client.Goal.Get(ctx, goalID)
		if queryErr != nil {
			return queryErr
		}
		if g.State != "COMPLETED" {
			t.Fatalf("parent did not resume: %s", g.State)
		}
		children, queryErr := tx.client.Run.Query().Where(run.GoalIDEQ(goalID), run.ParentRunIDNotNil()).All(ctx)
		if queryErr != nil {
			return queryErr
		}
		if len(children) != 1 || children[0].State != "succeeded" {
			t.Fatalf("child runs=%#v", children)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
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

func TestNonGitHubEventSuspendsAndResumesWorkflow(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	credential := IntegrationCredentialRecord{ID: ID(), PluginID: "approval", Label: "Approvals", SecretReference: "organizations/test/plugins/approval/credentials/one", SecretVersion: 1, State: "active"}
	if err := store.CreateIntegrationCredential(ctx, credential); err != nil {
		t.Fatal(err)
	}
	instanceID, err := store.UpsertIntegrationInstance(ctx, IntegrationInstanceRecord{CredentialID: credential.ID, PluginID: "approval", ExternalInstanceID: "one", RoutingIdentity: "approval:one", AccountID: "one", AccountLabel: "Review board", RepositorySelection: "all", Metadata: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	condition := Condition{Source: "approval", Type: "request.approved", Resource: "request-42", Version: "1"}
	spec := WorkflowSpec{
		SchemaVersion: 1,
		Triggers:      []WorkflowTrigger{{Type: "manual"}},
		Steps: []WorkflowStep{
			{Key: "prepare", Name: "Prepare", Type: "agent", Config: map[string]any{"runtime": "demo"}},
			{Key: "approval", Name: "Wait for approval", Type: "wait_event", Config: map[string]any{"integration_instance_id": instanceID, "condition": jsonObject(condition)}},
			{Key: "done", Name: "Complete", Type: "complete", Config: map[string]any{}},
		},
		Edges: []WorkflowEdge{{From: "prepare", To: "approval", Outcome: "success"}, {From: "approval", To: "done", Outcome: "success"}},
	}
	definition, err := store.CreateWorkflow(ctx, CreateWorkflow{Name: "Approval workflow", Description: "Resume from an approval service", Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishWorkflow(ctx, definition.ID); err != nil {
		t.Fatal(err)
	}
	goalID, err := store.StartWorkflow(ctx, definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx, goalID); err != nil {
		t.Fatal(err)
	}
	result, err := store.ReceiveIntegration(ctx, IntegrationReceipt{Source: condition.Source, Instance: "approval:one", DeliveryID: ID(), Condition: &condition, Details: map[string]any{"approved": true}, RawBody: []byte("approval")})
	if err != nil || result.Disposition != "accepted" {
		t.Fatalf("approval result=%#v err=%v", result, err)
	}
	if err = store.tx(ctx, func(tx *transaction) error {
		g, queryErr := tx.client.Goal.Get(ctx, goalID)
		if queryErr != nil {
			return queryErr
		}
		if g.State != "COMPLETED" {
			t.Fatalf("approval workflow state=%s", g.State)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowCanArchiveMultipleWaitGenerations(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	credential := IntegrationCredentialRecord{ID: ID(), PluginID: "approval", Label: "Approvals", SecretReference: "organizations/test/plugins/approval/credentials/repeat", SecretVersion: 1, State: "active"}
	if err := store.CreateIntegrationCredential(ctx, credential); err != nil {
		t.Fatal(err)
	}
	instanceID, err := store.UpsertIntegrationInstance(ctx, IntegrationInstanceRecord{CredentialID: credential.ID, PluginID: "approval", ExternalInstanceID: "repeat", RoutingIdentity: "approval:repeat", AccountID: "repeat", AccountLabel: "Board", RepositorySelection: "all", Metadata: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	wait := func(resource string) WorkflowStep {
		return WorkflowStep{Key: "wait_" + resource, Name: "Wait " + resource, Type: "wait_event", Config: map[string]any{"integration_instance_id": instanceID, "condition": jsonObject(Condition{Source: "approval", Type: "approved", Resource: resource, Version: "1"})}}
	}
	spec := WorkflowSpec{SchemaVersion: 1, Triggers: []WorkflowTrigger{{Type: "manual"}}, Steps: []WorkflowStep{
		{Key: "first", Name: "First", Type: "agent", Config: map[string]any{"runtime": "demo"}}, wait("one"),
		{Key: "second", Name: "Second", Type: "agent", Config: map[string]any{"runtime": "demo"}}, wait("two"),
		{Key: "done", Name: "Done", Type: "complete", Config: map[string]any{}},
	}, Edges: []WorkflowEdge{{From: "first", To: "wait_one", Outcome: "success"}, {From: "wait_one", To: "second", Outcome: "success"}, {From: "second", To: "wait_two", Outcome: "success"}, {From: "wait_two", To: "done", Outcome: "success"}}}
	definition, err := store.CreateWorkflow(ctx, CreateWorkflow{Name: "Repeat waits", Description: "Archive each wait generation", Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishWorkflow(ctx, definition.ID); err != nil {
		t.Fatal(err)
	}
	goalID, err := store.StartWorkflow(ctx, definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx, goalID); err != nil {
		t.Fatal(err)
	}
	for _, resource := range []string{"one", "two"} {
		condition := Condition{Source: "approval", Type: "approved", Resource: resource, Version: "1"}
		if _, err = store.ReceiveIntegration(ctx, IntegrationReceipt{Source: "approval", Instance: "approval:repeat", DeliveryID: ID(), Condition: &condition, RawBody: []byte(resource)}); err != nil {
			t.Fatal(err)
		}
		if resource == "one" {
			if err = store.Dispatch(ctx, goalID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = store.tx(ctx, func(tx *transaction) error {
		g, queryErr := tx.client.Goal.Get(ctx, goalID)
		if queryErr != nil {
			return queryErr
		}
		if g.State != "COMPLETED" {
			t.Fatalf("repeat wait workflow state=%s", g.State)
		}
		count, queryErr := tx.client.WaitHistory.Query().Where(waithistory.GoalIDEQ(goalID)).Count(ctx)
		if queryErr != nil {
			return queryErr
		}
		if count != 1 {
			t.Fatalf("wait history count=%d", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
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

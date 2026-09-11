package orchestration

import (
	"context"
	"errors"
	"log/slog"
	"time"

	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/piglor/loom/services/loom/internal/control"
)

type DispatchInput struct {
	GoalID            string            `json:"goal_id"`
	RunID             string            `json:"run_id"`
	WorkflowVersionID string            `json:"workflow_version_id,omitempty"`
	StepKey           string            `json:"step_key,omitempty"`
	IntentID          string            `json:"intent_id"`
	Topology          *WorkflowTopology `json:"topology,omitempty"`
}

type DispatchOutput struct {
	GoalID string `json:"goal_id"`
}

type hatchetPublisher struct{ task *hatchet.StandaloneTask }

func (p hatchetPublisher) Publish(ctx context.Context, item control.OutboxItem) (string, error) {
	var topology *WorkflowTopology
	if item.WorkflowVersionID != "" {
		compiled, err := CompileTopology(item.WorkflowDefinitionID, item.WorkflowVersionID, item.WorkflowVersionNumber, item.WorkflowSpec)
		if err != nil {
			return "", err
		}
		topology = &compiled
	}
	ref, err := p.task.RunNoWait(ctx, DispatchInput{GoalID: item.GoalID, RunID: item.RunID, WorkflowVersionID: item.WorkflowVersionID, StepKey: item.StepKey, IntentID: item.ID, Topology: topology},
		hatchet.WithRunKey(item.ID),
		hatchet.WithRunMetadata(map[string]string{"loom_goal_id": item.GoalID, "loom_run_id": item.RunID, "loom_workflow_version_id": item.WorkflowVersionID, "loom_step_key": item.StepKey, "loom_intent_id": item.ID, "loom_intent_kind": item.Kind}),
	)
	if err != nil {
		return "", err
	}
	return ref.RunId, nil
}

// Run starts the official Hatchet Go worker and a cheap outbox relay. Waiting
// is persisted by Loom and Hatchet; the relay never invokes an agent runtime.
func Run(ctx context.Context, store *control.Store) error {
	client, err := hatchet.NewClient()
	if err != nil {
		return errors.New("cannot configure Hatchet client")
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Close(closeCtx); err != nil {
			slog.Warn("hatchet_client_close_failed")
		}
	}()

	task := client.NewStandaloneTask("loom-workflow-dispatch-v1", func(taskContext hatchet.Context, input DispatchInput) (DispatchOutput, error) {
		if !control.ValidID(input.GoalID) || !control.ValidID(input.RunID) || !control.ValidID(input.IntentID) {
			return DispatchOutput{}, errors.New("invalid Loom dispatch identity")
		}
		if input.WorkflowVersionID != "" {
			if input.Topology == nil {
				return DispatchOutput{}, errors.New("missing Loom workflow topology")
			}
			if err := ValidateDispatchTopology(*input.Topology, input.WorkflowVersionID, input.StepKey); err != nil {
				return DispatchOutput{}, err
			}
		}
		if err := store.DispatchRun(taskContext, input.GoalID, input.RunID); err != nil {
			return DispatchOutput{}, err
		}
		return DispatchOutput{GoalID: input.GoalID}, nil
	},
		hatchet.WithRetries(5),
		hatchet.WithRetryBackoff(2, 30),
		hatchet.WithScheduleTimeout(30*time.Minute),
		hatchet.WithExecutionTimeout(30*time.Second),
	)
	worker, err := client.NewWorker("loom-control-plane", hatchet.WithWorkflows(task), hatchet.WithSlots(4))
	if err != nil {
		return errors.New("cannot configure Hatchet worker")
	}
	cleanup, err := worker.Start()
	if err != nil {
		return errors.New("cannot start Hatchet worker")
	}
	defer func() {
		if err := cleanup(); err != nil {
			slog.Warn("hatchet_worker_cleanup_failed")
		}
	}()

	publisher := hatchetPublisher{task}
	relay := func() {
		if err := RelayOnce(ctx, store, publisher); err != nil {
			slog.Warn("outbox_relay_retry")
		}
	}
	relay()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			relay()
		}
	}
}

package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/piglor/loom/services/loom/internal/control"
)

type fakeOutbox struct {
	items     []control.OutboxItem
	delivered []string
	failed    []string
}

func (f *fakeOutbox) OutboxBatch(context.Context) ([]control.OutboxItem, error) {
	return f.items, nil
}
func (f *fakeOutbox) MarkOutboxDelivered(_ context.Context, item control.OutboxItem, runID string) error {
	f.delivered = append(f.delivered, item.ID+":"+runID)
	return nil
}
func (f *fakeOutbox) MarkOutboxFailed(_ context.Context, item control.OutboxItem) error {
	f.failed = append(f.failed, item.ID)
	return nil
}

type fakePublisher struct{ failGoal string }

func (f fakePublisher) Publish(_ context.Context, item control.OutboxItem) (string, error) {
	if item.GoalID == f.failGoal {
		return "", errors.New("provider details must not escape")
	}
	return "run-" + item.ID, nil
}

func TestRelayAcknowledgesSuccessAndRetainsFailure(t *testing.T) {
	store := &fakeOutbox{items: []control.OutboxItem{
		{ID: "one", GoalID: "goal-one", Kind: "start", AvailableAt: time.Unix(1, 0)},
		{ID: "two", GoalID: "goal-two", Kind: "wake", AvailableAt: time.Unix(2, 0)},
	}}
	err := RelayOnce(context.Background(), store, fakePublisher{failGoal: "goal-two"})
	if err == nil || len(store.delivered) != 1 || store.delivered[0] != "one:run-one" || len(store.failed) != 1 || store.failed[0] != "two" {
		t.Fatalf("delivered=%v failed=%v err=%v", store.delivered, store.failed, err)
	}
}

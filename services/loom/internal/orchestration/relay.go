package orchestration

import (
	"context"
	"errors"
	"fmt"

	"github.com/piglor/loom/services/loom/internal/control"
)

type Outbox interface {
	OutboxBatch(context.Context) ([]control.OutboxItem, error)
	MarkOutboxDelivered(context.Context, control.OutboxItem, string) error
	MarkOutboxFailed(context.Context, control.OutboxItem) error
}

type Publisher interface {
	Publish(context.Context, control.OutboxItem) (string, error)
}

// RelayOnce transfers durable intents to the scheduler. Provider errors are
// returned only to the operator log; their text must never enter user-visible
// state or API responses.
func RelayOnce(ctx context.Context, store Outbox, publisher Publisher) error {
	items, err := store.OutboxBatch(ctx)
	if err != nil {
		return fmt.Errorf("load outbox: %w", err)
	}
	var failures []error
	for _, item := range items {
		runID, publishErr := publisher.Publish(ctx, item)
		if publishErr == nil {
			publishErr = store.MarkOutboxDelivered(ctx, item, runID)
		}
		if publishErr != nil {
			failures = append(failures, errors.New("outbox transfer failed"))
			if markErr := store.MarkOutboxFailed(ctx, item); markErr != nil {
				failures = append(failures, errors.New("outbox retry persistence failed"))
			}
		}
	}
	return errors.Join(failures...)
}

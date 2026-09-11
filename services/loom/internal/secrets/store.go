// Package secrets defines the control-plane boundary for integration secrets.
// Provider adapters receive values through this port; persistence stores only
// opaque references returned by the implementation.
package secrets

import (
	"context"
	"errors"
)

var (
	ErrNotConfigured = errors.New("secret store is not configured")
	ErrUnavailable   = errors.New("secret store is unavailable")
	ErrSealed        = errors.New("secret store is sealed")
	ErrNotFound      = errors.New("secret not found")
)

type Status string

const (
	StatusReady        Status = "ready"
	StatusUnconfigured Status = "unconfigured"
	StatusSealed       Status = "sealed"
	StatusUnavailable  Status = "unavailable"
)

// Store never exposes provider values through presentation contracts. Callers
// must keep returned values inside server-side integration adapters.
type Store interface {
	Status(context.Context) Status
	Put(context.Context, string, map[string]string) (int, error)
	Get(context.Context, string) (map[string]string, int, error)
	Delete(context.Context, string) error
}

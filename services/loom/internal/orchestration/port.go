package orchestration

import (
	"context"
	"fmt"
	"sort"

	"github.com/piglor/loom/services/loom/internal/control"
)

// Backend is Loom's replaceable scheduler boundary. Public HTTP and persisted
// workflow specifications never expose implementation-specific SDK types.
type Backend interface {
	Activate(context.Context, WorkflowTopology) error
	StartRun(context.Context, RunRequest) (string, error)
	PublishSignal(context.Context, SignalRequest) error
	CancelRun(context.Context, string) error
	InspectRun(context.Context, string) (Telemetry, error)
}

type WorkflowTopology struct {
	DefinitionID string         `json:"definition_id"`
	VersionID    string         `json:"version_id"`
	Version      int            `json:"version"`
	Nodes        []TopologyNode `json:"nodes"`
	Edges        []TopologyEdge `json:"edges"`
}

type TopologyNode struct {
	Key     string   `json:"key"`
	Type    string   `json:"type"`
	Parents []string `json:"parents"`
}

type TopologyEdge struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Outcome string `json:"outcome"`
}

type RunRequest struct {
	GoalID   string `json:"goal_id"`
	RunID    string `json:"run_id"`
	IntentID string `json:"intent_id"`
}

type SignalRequest struct {
	RunID      string `json:"run_id"`
	StepKey    string `json:"step_key"`
	Generation int    `json:"generation"`
	ReceiptID  string `json:"receipt_id"`
}

type Telemetry struct {
	State     string `json:"state"`
	StartedAt string `json:"started_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
	Retries   int    `json:"retries"`
}

// CompileTopology converts the canonical Loom DAG to an adapter-neutral parent
// map. Hatchet and future backends consume this representation; clients do not.
func CompileTopology(definitionID, versionID string, version int, spec control.WorkflowSpec) (WorkflowTopology, error) {
	validation := control.ValidateWorkflowSpec(spec)
	if !validation.Valid {
		return WorkflowTopology{}, &control.Fault{Status: 422, Message: validation.Errors[0]}
	}
	parents := map[string][]string{}
	edges := make([]TopologyEdge, 0, len(spec.Edges))
	for _, edge := range spec.Edges {
		parents[edge.To] = append(parents[edge.To], edge.From)
		edges = append(edges, TopologyEdge{From: edge.From, To: edge.To, Outcome: edge.Outcome})
	}
	nodes := make([]TopologyNode, 0, len(spec.Steps))
	for _, step := range spec.Steps {
		sort.Strings(parents[step.Key])
		nodes = append(nodes, TopologyNode{Key: step.Key, Type: step.Type, Parents: parents[step.Key]})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Key < nodes[j].Key })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From == edges[j].From {
			if edges[i].Outcome == edges[j].Outcome {
				return edges[i].To < edges[j].To
			}
			return edges[i].Outcome < edges[j].Outcome
		}
		return edges[i].From < edges[j].From
	})
	return WorkflowTopology{DefinitionID: definitionID, VersionID: versionID, Version: version, Nodes: nodes, Edges: edges}, nil
}

// ValidateDispatchTopology makes the adapter consume the immutable Loom
// topology on every agent dispatch. Loom remains the relationship authority;
// the scheduler may execute a single ready node, but it must not silently
// accept a missing or mismatched graph.
func ValidateDispatchTopology(topology WorkflowTopology, versionID, stepKey string) error {
	if topology.VersionID != versionID || stepKey == "" {
		return fmt.Errorf("workflow topology identity mismatch")
	}
	for _, node := range topology.Nodes {
		if node.Key != stepKey {
			continue
		}
		if node.Type != "agent" {
			return fmt.Errorf("workflow topology step %q is not dispatchable", stepKey)
		}
		return nil
	}
	return fmt.Errorf("workflow topology step %q is missing", stepKey)
}

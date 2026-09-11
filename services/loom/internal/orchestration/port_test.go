package orchestration

import (
	"testing"

	"github.com/piglor/loom/services/loom/internal/control"
)

func TestCompileTopologyPreservesLoomRelationships(t *testing.T) {
	spec := control.WorkflowSpec{
		SchemaVersion: 1,
		Triggers:      []control.WorkflowTrigger{{Type: "manual"}},
		Steps: []control.WorkflowStep{
			{Key: "start", Name: "Agent", Type: "agent", Config: map[string]any{"runtime": "demo"}},
			{Key: "check", Name: "Check", Type: "agent", Config: map[string]any{"runtime": "demo"}},
			{Key: "done", Name: "Done", Type: "complete", Config: map[string]any{}},
		},
		Edges: []control.WorkflowEdge{{From: "start", To: "check", Outcome: "success"}, {From: "check", To: "done", Outcome: "success"}},
	}
	topology, err := CompileTopology("definition", "version", 3, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(topology.Nodes) != 3 || len(topology.Edges) != 2 || len(topology.Nodes[1].Parents) != 1 || topology.Nodes[1].Parents[0] != "check" {
		t.Fatalf("relationship lost: %#v", topology)
	}
}

func TestValidateDispatchTopologyRequiresPinnedAgentNode(t *testing.T) {
	topology := WorkflowTopology{
		VersionID: "version",
		Nodes:     []TopologyNode{{Key: "agent", Type: "agent"}, {Key: "done", Type: "complete"}},
	}
	if err := ValidateDispatchTopology(topology, "version", "agent"); err != nil {
		t.Fatalf("expected agent node to be dispatchable: %v", err)
	}
	if err := ValidateDispatchTopology(topology, "other", "agent"); err == nil {
		t.Fatal("expected version mismatch to be rejected")
	}
	if err := ValidateDispatchTopology(topology, "version", "done"); err == nil {
		t.Fatal("expected non-agent node to be rejected")
	}
}

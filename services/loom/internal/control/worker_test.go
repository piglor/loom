package control

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/piglor/loom/services/loom/ent/attempt"
	entaudit "github.com/piglor/loom/services/loom/ent/audit"
	"github.com/piglor/loom/services/loom/ent/wait"
)

func TestLegacyStopDigestFixtures(t *testing.T) {
	const claimID = "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"
	const sessionID = "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb"
	for _, tc := range []struct {
		duration float64
		encoded  string
	}{{1, "1.0"}, {86400000, "86400000.0"}, {0.000001, "1e-06"}, {0.0001, "0.0001"}} {
		report := Stop{Claim: Claim{Protocol: 1, ClaimID: strings.ToUpper(claimID)}, SessionID: strings.ToUpper(sessionID), Duration: tc.duration, Success: true}
		if err := report.Validate(); err != nil {
			t.Fatal(err)
		}
		expected := fmt.Sprintf(`{"claim_id": "%s", "duration_ms": %s, "protocol_version": 1, "session_id": "%s", "success": true}`, claimID, tc.encoded, sessionID)
		got, err := stopDigest(report)
		if err != nil || got != hash([]byte(expected)) {
			t.Fatalf("legacy stop hash at %s: %v", tc.encoded, err)
		}
	}
}

func queuedCommand(t *testing.T, s *Store, token string) string {
	t.Helper()
	p, err := s.Poll(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	commands := p["commands"].([]map[string]any)
	if len(commands) != 1 {
		t.Fatalf("expected one command, got %v", commands)
	}
	return commands[0]["id"].(string)
}

func TestLegacyProtocolTwoAndUncertainStop(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	e, err := s.Enroll(ctx, Enrollment{Workspace: "legacy", Protocol: 2})
	if err != nil {
		t.Fatal(err)
	}
	workerID := e["worker_id"].(string)
	token := e["token"].(string)
	c := Condition{"approval", "approved", "request", "1"}
	id, err := s.Create(ctx, CreateGoal{Title: "Legacy", Objective: "Legacy context", Runtime: "remote-demo", WorkerID: &workerID, Condition: c})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Dispatch(ctx, id); err != nil {
		t.Fatal(err)
	}
	commandID := queuedCommand(t, s, token)
	claim := Claim{Protocol: 2, ClaimID: strings.ToUpper(ID())}
	execution, err := s.Claim(ctx, token, commandID, claim)
	if err != nil {
		t.Fatal(err)
	}
	if execution["context"].(map[string]any)["lifecycle"] != "legacy" {
		t.Fatal("invalid legacy lifecycle")
	}
	if _, err = s.Claim(ctx, token, commandID, claim); err != nil {
		t.Fatal("same uppercase claim replay", err)
	}
	provider := "exact"
	sessionID := strings.ToUpper(execution["session_id"].(string))
	if _, err = s.BindSession(ctx, token, commandID, BindSession{Claim: claim, SessionID: sessionID, ProviderID: provider}); err != nil {
		t.Fatal(err)
	}
	outcome := "blocked"
	report := Stop{Claim: claim, SessionID: sessionID, ProviderID: &provider, Duration: 2, Outcome: &outcome}
	if _, err = s.Report(ctx, token, commandID, report); err == nil {
		t.Fatal("legacy failure accepted explicit outcome")
	}
	report.Outcome = nil
	report.Success = true
	if err = s.tx(ctx, func(tx *transaction) error {
		return tx.client.Attempt.Update().Where(attempt.GoalIDEQ(id)).SetState("UNKNOWN").Exec(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Report(ctx, token, commandID, report); err != nil {
		t.Fatal("late stop could not resolve UNKNOWN", err)
	}
	legacyReceipt := fmt.Sprintf(`{"claim_id": "%s", "duration_ms": 2.0, "protocol_version": 2, "provider_session_id": "exact", "session_id": "%s", "success": true}`, strings.ToLower(claim.ClaimID), strings.ToLower(sessionID))
	if err = s.tx(ctx, func(tx *transaction) error {
		return tx.client.Command.UpdateOneID(commandID).SetReportDigest(hash([]byte(legacyReceipt))).Exec(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	if result, err := s.Report(ctx, token, commandID, report); err != nil || result["status"] != "duplicate" {
		t.Fatalf("persisted legacy stop receipt %+v %v", result, err)
	}
	if _, err = s.Receive(ctx, Event{Condition: c, GoalID: id, Generation: 1, DeliveryID: "approved"}); err != nil {
		t.Fatal(err)
	}
	if err = s.Dispatch(ctx, id); err != nil {
		t.Fatal(err)
	}
	commandID = queuedCommand(t, s, token)
	report.Claim = Claim{Protocol: 2, ClaimID: ID()}
	if _, err = s.Claim(ctx, token, commandID, report.Claim); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Report(ctx, token, commandID, report); err != nil {
		t.Fatal(err)
	}
	if err = s.tx(ctx, func(tx *transaction) error {
		g, err := tx.client.Goal.Get(ctx, id)
		if err != nil {
			return err
		}
		if g.State != "COMPLETED" || g.Phase != 2 {
			t.Fatalf("legacy completion %+v", g)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteMultiGenerationWaitAndAudit(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	e, err := s.Enroll(ctx, Enrollment{Workspace: "repeat", Protocol: 2})
	if err != nil {
		t.Fatal(err)
	}
	workerID := e["worker_id"].(string)
	token := e["token"].(string)
	first := Condition{"deployment", "finished", "deploy", "1"}
	last := Condition{"human", "approved", "report", "1"}
	id, err := s.Create(ctx, CreateGoal{Title: "Repeat", Objective: "Wait twice", Runtime: "remote-demo", WorkerID: &workerID, Condition: first, CompletionCondition: &last})
	if err != nil {
		t.Fatal(err)
	}
	for phase := 0; phase < 3; phase++ {
		if err = s.Dispatch(ctx, id); err != nil {
			t.Fatal(err)
		}
		commandID := queuedCommand(t, s, token)
		claim := Claim{Protocol: 2, ClaimID: ID()}
		execution, err := s.Claim(ctx, token, commandID, claim)
		if err != nil {
			t.Fatal(err)
		}
		sessionID := execution["session_id"].(string)
		if phase == 1 {
			r := PrepareWait{Claim: claim, SessionID: sessionID, Generation: 1, Condition: last}
			if _, err = s.Prepare(ctx, token, commandID, r); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Prepare(ctx, token, commandID, r); err != nil {
				t.Fatal("wait retry", err)
			}
			r.Condition.Version = "changed"
			if _, err = s.Prepare(ctx, token, commandID, r); err == nil {
				t.Fatal("wait changed on replay")
			}
		}
		outcome := "yield"
		if phase == 2 {
			outcome = "complete"
		}
		if _, err = s.Report(ctx, token, commandID, Stop{Claim: claim, SessionID: sessionID, Duration: 1, Success: true, Outcome: &outcome}); err != nil {
			t.Fatal(err)
		}
		if phase < 2 {
			c := first
			if phase == 1 {
				c = last
			}
			if _, err = s.Receive(ctx, Event{Condition: c, GoalID: id, Generation: phase + 1, DeliveryID: ID()}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = s.tx(ctx, func(tx *transaction) error {
		g, err := tx.client.Goal.Get(ctx, id)
		if err != nil {
			return err
		}
		if g.State != "COMPLETED" {
			t.Fatal(g.State)
		}
		history, err := tx.client.WaitHistory.Query().Count(ctx)
		if err != nil {
			return err
		}
		if history != 1 {
			t.Fatal("missing prior wait")
		}
		records, err := tx.client.Audit.Query().Where(entaudit.GoalIDEQ(id), entaudit.ActionEQ("wait_prepared")).Count(ctx)
		if err != nil {
			return err
		}
		if records != 1 {
			t.Fatal("missing/idempotency-broken wait audit")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerPollSkipsInactiveGoal(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	e, err := s.Enroll(ctx, Enrollment{Workspace: "queue", Protocol: 2})
	if err != nil {
		t.Fatal(err)
	}
	workerID := e["worker_id"].(string)
	token := e["token"].(string)
	c := Condition{"approval", "approved", "request", "1"}
	create := func() string {
		id, err := s.Create(ctx, CreateGoal{Title: "Queue", Objective: "Queue", Runtime: "remote-demo", WorkerID: &workerID, Condition: c})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Dispatch(ctx, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := create()
	oldCommand := queuedCommand(t, s, token)
	if err = s.tx(ctx, func(tx *transaction) error { return transition(ctx, tx, first, "BLOCKED") }); err != nil {
		t.Fatal(err)
	}
	create()
	if next := queuedCommand(t, s, token); next == oldCommand {
		t.Fatal("inactive command starves queue")
	}
}

func TestBlockedExecutionAudit(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	e, err := s.Enroll(ctx, Enrollment{Workspace: "audit", Protocol: 2})
	if err != nil {
		t.Fatal(err)
	}
	workerID := e["worker_id"].(string)
	token := e["token"].(string)
	c := Condition{"approval", "approved", "request", "1"}
	id, err := s.Create(ctx, CreateGoal{Title: "Audit", Objective: "Premature completion", Runtime: "remote-demo", WorkerID: &workerID, Condition: c, CompletionCondition: &c})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Dispatch(ctx, id); err != nil {
		t.Fatal(err)
	}
	commandID := queuedCommand(t, s, token)
	claim := Claim{Protocol: 2, ClaimID: ID()}
	execution, err := s.Claim(ctx, token, commandID, claim)
	if err != nil {
		t.Fatal(err)
	}
	outcome := "complete"
	if _, err = s.Report(ctx, token, commandID, Stop{Claim: claim, SessionID: execution["session_id"].(string), Success: true, Duration: 1, Outcome: &outcome}); err != nil {
		t.Fatal(err)
	}
	if err = s.tx(ctx, func(tx *transaction) error {
		row, err := tx.client.Audit.Query().Where(entaudit.GoalIDEQ(id), entaudit.ActionEQ("execution_blocked")).Only(ctx)
		if err != nil {
			return err
		}
		if row.Details["reason"] != "completion_policy_rejected" {
			t.Fatal(row.Details)
		}
		w, err := tx.client.Wait.Query().Where(wait.GoalIDEQ(id)).Only(ctx)
		if err != nil {
			return err
		}
		if w.ClosedAt == nil {
			t.Fatal("blocked wait left open")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOutboundWorkerExactSessionLifecycle(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	enrolled, err := s.Enroll(ctx, Enrollment{Workspace: "proof", Protocol: 2})
	if err != nil {
		t.Fatal(err)
	}
	workerID := enrolled["worker_id"].(string)
	token := enrolled["token"].(string)
	c := Condition{"approval", "approved", "change-1", "revision-1"}
	id, err := s.Create(ctx, CreateGoal{Title: "Approval continuation", Objective: "Continue the same Session after approval", Runtime: "remote-demo", WorkerID: &workerID, Condition: c, CompletionCondition: &c})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Dispatch(ctx, id); err != nil {
		t.Fatal(err)
	}
	commandID := queuedCommand(t, s, token)
	claim := Claim{Protocol: 2, ClaimID: ID()}
	other, err := s.Enroll(ctx, Enrollment{Workspace: "other", Protocol: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(ctx, other["token"].(string), commandID, claim); err == nil {
		t.Fatal("wrong worker claimed command")
	}
	if _, err = s.Poll(ctx, "invalid-credential-aaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("invalid worker authentication")
	}
	execution, err := s.Claim(ctx, token, commandID, claim)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := execution["session_id"].(string)
	provider := "test-provider-exact-session"
	if _, err = s.Claim(ctx, token, commandID, Claim{Protocol: 2, ClaimID: ID()}); err == nil {
		t.Fatal("second claim admitted")
	}
	if _, err = s.BindSession(ctx, token, commandID, BindSession{Claim: claim, SessionID: sessionID, ProviderID: provider}); err != nil {
		t.Fatal(err)
	}
	yield := "yield"
	report := Stop{Claim: claim, SessionID: sessionID, Duration: 5, Success: true, ProviderID: &provider, Outcome: &yield}
	if _, err = s.Report(ctx, token, commandID, report); err != nil {
		t.Fatal(err)
	}
	if result, err := s.Report(ctx, token, commandID, report); err != nil || result["status"] != "duplicate" {
		t.Fatalf("stop replay %+v %v", result, err)
	}
	restarted := &Store{DB: s.DB, Organization: s.Organization}
	if err = restarted.Dispatch(ctx, id); err != nil {
		t.Fatal(err)
	}
	p, err := restarted.Poll(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if len(p["commands"].([]map[string]any)) != 0 {
		t.Fatal("model command during external wait")
	}
	if err = restarted.Bind(ctx, Binding{GoalID: id, Generation: 1, Instance: "company", Condition: c}); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.ReceiveIntegration(ctx, IntegrationReceipt{Source: c.Source, Instance: "company", DeliveryID: "approved-1", Condition: &c}); err != nil {
		t.Fatal(err)
	}
	if err = restarted.Dispatch(ctx, id); err != nil {
		t.Fatal(err)
	}
	continuationID := queuedCommand(t, restarted, token)
	next := Claim{Protocol: 2, ClaimID: ID()}
	execution, err = restarted.Claim(ctx, token, continuationID, next)
	if err != nil {
		t.Fatal(err)
	}
	context := execution["context"].(map[string]any)
	if execution["session_id"] != sessionID || execution["worker_id"] != workerID || str(context["provider_session_id"].(*string)) != provider {
		t.Fatalf("lost Session affinity: %+v", execution)
	}
	complete := "complete"
	if _, err = restarted.Report(ctx, token, continuationID, Stop{Claim: next, SessionID: sessionID, Duration: 8, Success: true, ProviderID: &provider, Outcome: &complete}); err != nil {
		t.Fatal(err)
	}
	if err = restarted.tx(ctx, func(tx *transaction) error {
		g, err := tx.client.Goal.Get(ctx, id)
		if err != nil {
			return err
		}
		if g.State != "COMPLETED" {
			t.Fatalf("state %s", g.State)
		}
		n, err := tx.client.Attempt.Query().Where(attempt.GoalIDEQ(id)).Count(ctx)
		if err != nil {
			return err
		}
		if n != 2 {
			t.Fatalf("attempts %d", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSuccessfulCodexStopRequiresProviderIdentity(t *testing.T) {
	s := testStore(t, true)
	s.EnableCodex = true
	ctx := context.Background()
	e, err := s.Enroll(ctx, Enrollment{Workspace: "proof", Protocol: 2, Runtime: "codex-container"})
	if err != nil {
		t.Fatal(err)
	}
	workerID := e["worker_id"].(string)
	token := e["token"].(string)
	c := Condition{"human", "approved", "request", "1"}
	id, err := s.Create(ctx, CreateGoal{Title: "Proof", Objective: "Stop identity", Runtime: "codex-container", WorkerID: &workerID, Condition: c, CompletionCondition: &c})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Dispatch(ctx, id); err != nil {
		t.Fatal(err)
	}
	commandID := queuedCommand(t, s, token)
	claim := Claim{Protocol: 2, ClaimID: ID()}
	execution, err := s.Claim(ctx, token, commandID, claim)
	if err != nil {
		t.Fatal(err)
	}
	yield := "yield"
	report := Stop{Claim: claim, SessionID: execution["session_id"].(string), Success: true, Duration: 1, Outcome: &yield}
	if _, err = s.Report(ctx, token, commandID, report); err == nil {
		t.Fatal("success without provider binding")
	}
	report.Success = false
	if _, err = s.Report(ctx, token, commandID, report); err != nil {
		t.Fatal("pre-inference runtime failure must be reportable", err)
	}
}

package control

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/piglor/loom/services/loom/ent"
	"github.com/piglor/loom/services/loom/ent/attempt"
	"github.com/piglor/loom/services/loom/ent/command"
	"github.com/piglor/loom/services/loom/ent/goal"
	"github.com/piglor/loom/services/loom/ent/run"
	"github.com/piglor/loom/services/loom/ent/session"
	"github.com/piglor/loom/services/loom/ent/wait"
	"github.com/piglor/loom/services/loom/ent/worker"
)

func (s *Store) Enroll(ctx context.Context, r Enrollment) (map[string]any, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if r.Runtime == "codex-container" && !s.EnableCodex {
		return nil, conflict("Contained runtime disabled")
	}
	secret := make([]byte, 48)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	id := ID()
	caps := []string{"finite-demo"}
	if r.Runtime == "codex-container" {
		caps = []string{r.Runtime}
	}
	if r.Protocol == 2 {
		caps = append(caps, "event-driven-v1")
	}
	err := s.tx(ctx, func(t *transaction) error {
		return t.client.Worker.Create().SetID(id).SetOrganization(s.Organization).SetRuntime(r.Runtime).SetCapabilities(caps).SetTokenHash(hash([]byte(token))).SetWorkspaceRef(r.Workspace).SetLabels(jsonObject(r.Labels)).Exec(ctx)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"worker_id": id, "token": token, "workspace_ref": r.Workspace, "protocol_version": r.Protocol}, nil
}

func (s *Store) worker(ctx context.Context, t *transaction, token string) (*ent.Worker, error) {
	if len(token) < 32 || len(token) > 256 {
		return nil, unauthorized()
	}
	w, err := t.client.Worker.Query().Where(worker.OrganizationEQ(s.Organization), worker.TokenHashEQ(hash([]byte(token))), worker.RevokedAtIsNil()).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, unauthorized()
	}
	return w, err
}

func (s *Store) Poll(ctx context.Context, token string) (map[string]any, error) {
	var result map[string]any
	err := s.tx(ctx, func(t *transaction) error {
		w, err := s.worker(ctx, t, token)
		if err != nil {
			return err
		}
		if err = t.client.Worker.UpdateOneID(w.ID).SetLastSeenAt(t.now).Exec(ctx); err != nil {
			return err
		}
		rows, err := t.client.Command.Query().Where(command.WorkerIDEQ(w.ID), command.StateIn("QUEUED", "CLAIMED"), func(q *entsql.Selector) {
			g := entsql.Table(goal.Table)
			q.Where(entsql.In(q.C(command.FieldGoalID), entsql.Select(g.C(goal.FieldID)).From(g).Where(entsql.And(entsql.EQ(g.C(goal.FieldOrganization), s.Organization), entsql.NotIn(g.C(goal.FieldState), "COMPLETED", "FAILED", "CANCELLED", "BLOCKED")))))
		}).Order(ent.Asc(command.FieldCreatedAt)).Limit(1).All(ctx)
		if err != nil {
			return err
		}
		commands := []map[string]any{}
		for _, r := range rows {
			commands = append(commands, map[string]any{"id": r.ID, "state": r.State})
		}
		result = map[string]any{"protocol_version": 1, "worker_id": w.ID, "commands": commands}
		return nil
	})
	return result, err
}

func (s *Store) goalRow(ctx context.Context, t *transaction, id string) (*ent.Goal, error) {
	g, err := t.client.Goal.Query().Where(goal.IDEQ(id), goal.OrganizationEQ(s.Organization)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, notFound()
	}
	return g, err
}
func runRow(ctx context.Context, t *transaction, id string) (*ent.Run, error) {
	return t.client.Run.Query().Where(run.GoalIDEQ(id), run.ParentRunIDIsNil()).Only(ctx)
}
func waitRow(ctx context.Context, t *transaction, id string) (*ent.Wait, error) {
	return t.client.Wait.Query().Where(wait.GoalIDEQ(id)).Only(ctx)
}
func policy(r *ent.Run) Policy {
	var p Policy
	b, _ := json.Marshal(r.Policy)
	_ = json.Unmarshal(b, &p)
	if p.Lifecycle == "" {
		p.Lifecycle = "legacy"
	}
	return p
}

// Dispatch admits at most one queued attempt under the Goal lock. An offline
// bound worker leaves a durable command; it is never moved to another machine.
func (s *Store) Dispatch(ctx context.Context, id string) error {
	return s.tx(ctx, func(t *transaction) error {
		g, err := s.goalRow(ctx, t, id)
		if err != nil {
			return err
		}
		if g.State != "READY" {
			return nil
		}
		r, err := runRow(ctx, t, id)
		if err != nil {
			return err
		}
		p := policy(r)
		se, err := t.client.Session.Query().Where(session.RunIDEQ(r.ID)).Only(ctx)
		if err != nil {
			return err
		}
		if se.Runtime == "demo" {
			return s.dispatchDemo(ctx, t, g, r, se)
		}
		if se.Runtime != "remote-demo" && se.Runtime != "codex-container" {
			return conflict("Native dispatcher requires an outbound runtime")
		}
		w, err := waitRow(ctx, t, id)
		if err != nil {
			return err
		}
		if g.Phase > 0 && w.SatisfiedAt == nil {
			return nil
		}
		if g.Phase >= p.MaxAttempts {
			if err = t.client.Wait.UpdateOneID(w.ID).SetClosedAt(t.now).Exec(ctx); err != nil {
				return err
			}
			if err = transition(ctx, t, id, "BLOCKED"); err != nil {
				return err
			}
			return audit(ctx, t, id, "attempt_budget_exhausted", nil)
		}
		exists, err := t.client.Attempt.Query().Where(attempt.GoalIDEQ(id), attempt.PhaseEQ(g.Phase)).Exist(ctx)
		if err != nil || exists {
			return err
		}
		a, err := t.client.Attempt.Create().SetGoalID(id).SetSessionID(se.ID).SetWorkerID(se.WorkerID).SetPhase(g.Phase).SetState("QUEUED").SetStartedAt(t.now).Save(ctx)
		if err != nil {
			return err
		}
		if err = t.client.Command.Create().SetGoalID(id).SetAttemptID(a.ID).SetWorkerID(se.WorkerID).SetState("QUEUED").SetCreatedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		if err = transition(ctx, t, id, "WAITING"); err != nil {
			return err
		}
		if err = t.client.Goal.UpdateOneID(id).SetWaitingReason("worker").Exec(ctx); err != nil {
			return err
		}
		return audit(ctx, t, id, "command_queued", map[string]any{"worker_id": se.WorkerID})
	})
}

// dispatchDemo is a finite, in-process acceptance runtime. It never invokes a
// model, executes user code, or starts a background process. Keeping it here
// preserves legacy/demo Goals while the production runtimes use outbound Agents.
func (s *Store) dispatchDemo(ctx context.Context, t *transaction, g *ent.Goal, r *ent.Run, se *ent.Session) error {
	p := policy(r)
	w, err := waitRow(ctx, t, g.ID)
	if err != nil {
		return err
	}
	if g.Phase > 0 && w.SatisfiedAt == nil {
		return nil
	}
	if g.Phase >= p.MaxAttempts {
		if err = t.client.Wait.UpdateOneID(w.ID).SetClosedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		if err = transition(ctx, t, g.ID, "BLOCKED"); err != nil {
			return err
		}
		return audit(ctx, t, g.ID, "attempt_budget_exhausted", nil)
	}
	exists, err := t.client.Attempt.Query().Where(attempt.GoalIDEQ(g.ID), attempt.PhaseEQ(g.Phase)).Exist(ctx)
	if err != nil || exists {
		return err
	}
	outcome := "success"
	if p.Lifecycle == "event-driven-v1" {
		outcome = "yield"
		if g.Phase > 0 {
			outcome = "complete"
		}
	}
	a, err := t.client.Attempt.Create().SetGoalID(g.ID).SetSessionID(se.ID).SetWorkerID(se.WorkerID).SetPhase(g.Phase).SetState("STOPPED").SetStartedAt(t.now).SetStoppedAt(t.now).SetDurationMs(0).SetOutcome(outcome).Save(ctx)
	if err != nil {
		return err
	}
	if err = audit(ctx, t, g.ID, "attempt_started", map[string]any{"attempt_id": a.ID}); err != nil {
		return err
	}
	if err = audit(ctx, t, g.ID, "runtime_stopped", map[string]any{"attempt_id": a.ID}); err != nil {
		return err
	}
	if p.Lifecycle == "workflow-v1" {
		return s.advanceWorkflowAfterAgent(ctx, t, g.ID, r, true)
	}
	if g.Phase == 0 {
		if err = t.client.Wait.UpdateOneID(w.ID).SetArmedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		if err = t.client.Goal.UpdateOneID(g.ID).AddPhase(1).SetWaitingReason("external_event").Exec(ctx); err != nil {
			return err
		}
		state := "WAITING"
		if w.SatisfiedAt != nil {
			state = "READY"
		}
		if err = transition(ctx, t, g.ID, state); err != nil {
			return err
		}
		return enqueue(ctx, t, g.ID, "wake")
	}
	if w.ClosedAt == nil {
		if err = t.client.Wait.UpdateOneID(w.ID).SetClosedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		w.ClosedAt = &t.now
	}
	if w.ClosedAt == nil || w.SatisfiedAt == nil || !reflect.DeepEqual(w.Condition, g.CompletionCriteria["event_matches"]) {
		if err = transition(ctx, t, g.ID, "BLOCKED"); err != nil {
			return err
		}
		return audit(ctx, t, g.ID, "execution_blocked", map[string]any{"reason": "completion_policy_rejected"})
	}
	if p.Lifecycle == "legacy" {
		if err = t.client.Goal.UpdateOneID(g.ID).SetPhase(2).Exec(ctx); err != nil {
			return err
		}
	}
	if err = transition(ctx, t, g.ID, "COMPLETED"); err != nil {
		return err
	}
	return enqueue(ctx, t, g.ID, "wake")
}

type admitted struct {
	worker  *ent.Worker
	command *ent.Command
	goal    *ent.Goal
	attempt *ent.Attempt
	session *ent.Session
	policy  Policy
}

func (s *Store) admission(ctx context.Context, t *transaction, token, id string, r *Claim) (*admitted, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if !ValidID(id) {
		return nil, invalid("Invalid command")
	}
	w, err := s.worker(ctx, t, token)
	if err != nil {
		return nil, err
	}
	c, err := t.client.Command.Query().Where(command.IDEQ(id), command.WorkerIDEQ(w.ID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	g, err := s.goalRow(ctx, t, c.GoalID)
	if err != nil {
		return nil, err
	}
	c, err = t.client.Command.Query().Where(command.IDEQ(id)).ForUpdate().Only(ctx)
	if err != nil {
		return nil, err
	}
	a, err := t.client.Attempt.Get(ctx, c.AttemptID)
	if err != nil {
		return nil, err
	}
	se, err := t.client.Session.Get(ctx, a.SessionID)
	if err != nil {
		return nil, err
	}
	run, err := runRow(ctx, t, g.ID)
	if err != nil {
		return nil, err
	}
	p := policy(run)
	if (r.Protocol == 2 && !contains(w.Capabilities, "event-driven-v1")) || (p.Lifecycle == "event-driven-v1" && r.Protocol != 2) {
		return nil, conflict("Execution protocol mismatch")
	}
	return &admitted{w, c, g, a, se, p}, nil
}

func (s *Store) Claim(ctx context.Context, token, id string, r Claim) (map[string]any, error) {
	var response map[string]any
	err := s.tx(ctx, func(t *transaction) error {
		a, err := s.admission(ctx, t, token, id, &r)
		if err != nil {
			return err
		}
		if a.worker.Runtime == "codex-container" && !s.EnableCodex {
			return conflict("Contained admission disabled")
		}
		if terminal(a.goal.State) {
			return conflict("Goal is not executable")
		}
		if a.command.State == "CLAIMED" {
			if str(a.command.ClaimID) != r.ClaimID {
				return conflict("Execution already claimed; reconcile before retry")
			}
		} else if a.command.State != "QUEUED" {
			return conflict("Command not executable")
		} else {
			if err = t.client.Command.UpdateOneID(id).SetState("CLAIMED").SetClaimID(r.ClaimID).SetClaimedAt(t.now).Exec(ctx); err != nil {
				return err
			}
			if err = t.client.Attempt.UpdateOneID(a.attempt.ID).SetState("RUNNING").SetStartedAt(t.now).Exec(ctx); err != nil {
				return err
			}
			if a.attempt.Phase > 0 {
				if err = t.client.Wait.Update().Where(wait.GoalIDEQ(a.goal.ID)).SetClosedAt(t.now).Exec(ctx); err != nil {
					return err
				}
			}
			if err = transition(ctx, t, a.goal.ID, "RUNNING"); err != nil {
				return err
			}
			if err = t.client.Goal.UpdateOneID(a.goal.ID).ClearWaitingReason().Exec(ctx); err != nil {
				return err
			}
			if err = audit(ctx, t, a.goal.ID, "attempt_started", map[string]any{"attempt_id": a.attempt.ID}); err != nil {
				return err
			}
		}
		response = map[string]any{"protocol_version": r.Protocol, "command_id": id, "claim_id": r.ClaimID, "session_id": a.session.ID, "worker_id": a.worker.ID, "runtime": a.worker.Runtime, "workspace_ref": str(a.worker.WorkspaceRef), "phase": a.attempt.Phase}
		if r.Protocol == 2 {
			w, err := waitRow(ctx, t, a.goal.ID)
			if err != nil {
				return err
			}
			var event any
			if w.EventID != nil {
				e, err := t.client.Event.Get(ctx, *w.EventID)
				if err != nil {
					return err
				}
				event = e.Body
			}
			response["context"] = map[string]any{"goal_id": a.goal.ID, "objective": a.goal.Objective, "lifecycle": a.policy.Lifecycle, "max_attempts": a.policy.MaxAttempts, "completion_condition": a.goal.CompletionCriteria["event_matches"], "wait": map[string]any{"generation": w.Generation, "condition": w.Condition, "satisfied": w.SatisfiedAt != nil}, "provider_session_id": a.session.ProviderSessionID, "external_event": event}
		}
		return nil
	})
	return response, err
}

func (s *Store) BindSession(ctx context.Context, token, id string, r BindSession) (map[string]any, error) {
	if !ValidID(r.SessionID) || r.ProviderID == "" || !validProvider(&r.ProviderID) {
		return nil, invalid("Invalid session binding")
	}
	r.SessionID = strings.ToLower(r.SessionID)
	var response map[string]any
	err := s.tx(ctx, func(t *transaction) error {
		a, err := s.admission(ctx, t, token, id, &r.Claim)
		if err != nil {
			return err
		}
		if a.command.State != "CLAIMED" || a.goal.State != "RUNNING" || a.attempt.State != "RUNNING" || str(a.command.ClaimID) != r.ClaimID || a.session.ID != r.SessionID {
			return conflict("Session binding requires admitted execution")
		}
		if a.session.ProviderSessionID != nil {
			if *a.session.ProviderSessionID != r.ProviderID {
				return conflict("Provider session cannot change")
			}
		} else {
			if a.attempt.Phase != 0 {
				return conflict("Continuation cannot replace provider context")
			}
			exists, err := t.client.Session.Query().Where(session.WorkerIDEQ(a.worker.ID), session.RuntimeEQ(a.session.Runtime), session.ProviderSessionIDEQ(r.ProviderID)).Exist(ctx)
			if err != nil {
				return err
			}
			if exists {
				return conflict("Provider session already bound")
			}
			if err = t.client.Session.UpdateOneID(a.session.ID).SetProviderSessionID(r.ProviderID).Exec(ctx); err != nil {
				return err
			}
			if err = audit(ctx, t, a.goal.ID, "provider_session_bound", map[string]any{"session_id": a.session.ID}); err != nil {
				return err
			}
		}
		response = map[string]any{"protocol_version": r.Protocol, "session_id": a.session.ID, "worker_id": a.worker.ID, "runtime": a.session.Runtime, "provider_session_id": r.ProviderID}
		return nil
	})
	return response, err
}

func (s *Store) Prepare(ctx context.Context, token, id string, r PrepareWait) (map[string]any, error) {
	if err := r.Condition.Validate(); err != nil {
		return nil, err
	}
	if !ValidID(r.SessionID) || r.Generation < 1 {
		return nil, invalid("Invalid wait generation")
	}
	r.SessionID = strings.ToLower(r.SessionID)
	var response map[string]any
	err := s.tx(ctx, func(t *transaction) error {
		a, err := s.admission(ctx, t, token, id, &r.Claim)
		if err != nil {
			return err
		}
		if a.command.State != "CLAIMED" || str(a.command.ClaimID) != r.ClaimID || a.session.ID != r.SessionID || a.goal.State != "RUNNING" || a.attempt.State != "RUNNING" || a.policy.Lifecycle != "event-driven-v1" {
			return conflict("Wait preparation requires admitted execution")
		}
		w, err := waitRow(ctx, t, a.goal.ID)
		if err != nil {
			return err
		}
		if str(w.PreparedByAttempt) == a.attempt.ID {
			if w.Generation != r.Generation+1 || !reflect.DeepEqual(w.Condition, jsonObject(r.Condition)) {
				return conflict("Attempt prepared a different wait")
			}
		} else {
			if w.Generation != r.Generation || w.ClosedAt == nil || a.attempt.Phase == 0 {
				return conflict("Wait not ready for replacement")
			}
			if err = t.client.WaitHistory.Create().SetID(w.ID).SetGoalID(w.GoalID).SetGeneration(w.Generation).SetCondition(w.Condition).SetNillableArmedAt(w.ArmedAt).SetNillableSatisfiedAt(w.SatisfiedAt).SetNillableEventID(w.EventID).SetNillableClosedAt(w.ClosedAt).SetNillablePreparedByAttempt(w.PreparedByAttempt).Exec(ctx); err != nil {
				return err
			}
			if err = t.client.Wait.DeleteOneID(w.ID).Exec(ctx); err != nil {
				return err
			}
			w, err = t.client.Wait.Create().SetGoalID(a.goal.ID).SetGeneration(r.Generation + 1).SetCondition(jsonObject(r.Condition)).SetPreparedByAttempt(a.attempt.ID).Save(ctx)
			if err != nil {
				return err
			}
			if err = audit(ctx, t, a.goal.ID, "wait_prepared", map[string]any{"wait_id": w.ID, "generation": w.Generation, "attempt_id": a.attempt.ID}); err != nil {
				return err
			}
		}
		response = map[string]any{"protocol_version": 2, "wait_id": w.ID, "generation": w.Generation}
		return nil
	})
	return response, err
}

func stopDigest(r Stop) (string, error) {
	m := jsonObject(r)
	duration := strconv.FormatFloat(r.Duration, 'f', -1, 64)
	if r.Duration > 0 && r.Duration < 0.0001 {
		duration = strconv.FormatFloat(r.Duration, 'g', -1, 64)
	}
	if !strings.ContainsAny(duration, ".e") {
		duration += ".0"
	}
	m["duration_ms"] = json.Number(duration)
	b, err := canonicalJSON(m, true)
	return hash(b), err
}
func (s *Store) Report(ctx context.Context, token, id string, r Stop) (map[string]any, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	digest, err := stopDigest(r)
	if err != nil {
		return nil, err
	}
	status := "accepted"
	err = s.tx(ctx, func(t *transaction) error {
		a, err := s.admission(ctx, t, token, id, &r.Claim)
		if err != nil {
			return err
		}
		if str(a.command.ClaimID) != r.ClaimID || a.session.ID != r.SessionID {
			return conflict("Claim or Session mismatch")
		}
		if a.command.ReportDigest != nil {
			if *a.command.ReportDigest != digest {
				return conflict("Conflicting stop report")
			}
			status = "duplicate"
			return nil
		}
		if a.command.State != "CLAIMED" || (a.attempt.State != "RUNNING" && a.attempt.State != "UNKNOWN") {
			return conflict("Execution was not running")
		}
		if !reflect.DeepEqual(a.session.ProviderSessionID, r.ProviderID) {
			return conflict("Provider session mismatch")
		}
		if r.Success && a.worker.Runtime == "codex-container" && r.ProviderID == nil {
			return conflict("Successful Codex execution requires provider binding")
		}
		repeat := a.policy.Lifecycle == "event-driven-v1"
		workflowRun := a.policy.Lifecycle == "workflow-v1"
		if !repeat && !workflowRun && r.Outcome != nil {
			return conflict("Legacy outcome cannot change")
		}
		outcome := "runtime_failure"
		if r.Success {
			if repeat {
				outcome = str(r.Outcome)
				if outcome != "yield" && outcome != "complete" && outcome != "blocked" {
					return conflict("Explicit execution outcome required")
				}
			} else {
				outcome = "success"
			}
		}
		if err = t.client.Attempt.UpdateOneID(a.attempt.ID).SetState("STOPPED").SetStoppedAt(t.now).SetDurationMs(r.Duration).SetOutcome(outcome).Exec(ctx); err != nil {
			return err
		}
		if err = audit(ctx, t, a.goal.ID, "runtime_stopped", map[string]any{"attempt_id": a.attempt.ID}); err != nil {
			return err
		}
		if workflowRun {
			workflow, runErr := runRow(ctx, t, a.goal.ID)
			if runErr != nil {
				return runErr
			}
			err = s.advanceWorkflowAfterAgent(ctx, t, a.goal.ID, workflow, r.Success)
		} else if !r.Success {
			err = transition(ctx, t, a.goal.ID, "FAILED")
		} else {
			w, e := waitRow(ctx, t, a.goal.ID)
			if e != nil {
				return e
			}
			if (!repeat && a.attempt.Phase == 0) || (repeat && outcome == "yield" && (a.attempt.Phase == 0 || str(w.PreparedByAttempt) == a.attempt.ID)) {
				if err = t.client.Wait.UpdateOneID(w.ID).SetArmedAt(t.now).Exec(ctx); err != nil {
					return err
				}
				if err = t.client.Goal.UpdateOneID(a.goal.ID).AddPhase(1).SetWaitingReason("external_event").Exec(ctx); err != nil {
					return err
				}
				state := "WAITING"
				if w.SatisfiedAt != nil {
					state = "READY"
				}
				err = transition(ctx, t, a.goal.ID, state)
			} else if ((!repeat && a.attempt.Phase > 0) || (repeat && outcome == "complete")) && w.ClosedAt != nil && w.SatisfiedAt != nil && reflect.DeepEqual(w.Condition, a.goal.CompletionCriteria["event_matches"]) {
				if !repeat {
					if err = t.client.Goal.UpdateOneID(a.goal.ID).SetPhase(2).Exec(ctx); err != nil {
						return err
					}
				}
				err = transition(ctx, t, a.goal.ID, "COMPLETED")
			} else {
				if w.ClosedAt == nil {
					if err = t.client.Wait.UpdateOneID(w.ID).SetClosedAt(t.now).Exec(ctx); err != nil {
						return err
					}
				}
				err = transition(ctx, t, a.goal.ID, "BLOCKED")
				if err != nil {
					return err
				}
				reason := "agent_blocked"
				if outcome == "yield" {
					reason = "missing_next_wait"
				} else if outcome == "complete" {
					reason = "completion_policy_rejected"
				}
				err = audit(ctx, t, a.goal.ID, "execution_blocked", map[string]any{"reason": reason})
			}
		}
		if err != nil {
			return err
		}
		if err = t.client.Command.UpdateOneID(id).SetState("STOPPED").SetReportDigest(digest).SetStoppedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		if workflowRun {
			return nil
		}
		return enqueue(ctx, t, a.goal.ID, "wake")
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": status}, nil
}

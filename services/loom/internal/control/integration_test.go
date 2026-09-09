package control

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/piglor/loom/services/loom/ent/attempt"
	"github.com/piglor/loom/services/loom/ent/goal"
	"github.com/piglor/loom/services/loom/ent/outbox"
)

func assertGoalState(t *testing.T, s *Store, id, state string) {
	t.Helper()
	ctx := context.Background()
	if err := s.tx(ctx, func(tx *transaction) error {
		g, err := tx.client.Goal.Get(ctx, id)
		if err != nil {
			return err
		}
		if g.State != state {
			t.Fatalf("goal state %s, want %s", g.State, state)
		}
		n, err := tx.client.Attempt.Query().Where(attempt.GoalIDEQ(id)).Count(ctx)
		if err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("routing launched %d model attempts", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBindingAdmissionAndNumericIdentifiers(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	c := Condition{"approval", "approved", "request", "2"}
	id := newWaitingGoal(t, s, c)
	b := Binding{GoalID: id, Generation: 1, Instance: "service", Condition: c}
	wrong := b
	wrong.Generation = 2
	if err := s.Bind(ctx, wrong); err == nil {
		t.Fatal("bound wrong generation")
	}
	for _, number := range []any{int64(9007199254740992), uint64(9007199254740993), json.Number("9007199254740993"), 1.5} {
		bad := b
		bad.Attributes = map[string]any{"nested": []any{map[string]any{"identifier": number}}}
		if err := s.Bind(ctx, bad); err == nil {
			t.Fatalf("accepted unsafe identifier %v", number)
		}
	}
	b.Attributes = map[string]any{"identifier": "9007199254740993"}
	if err := s.Bind(ctx, b); err != nil {
		t.Fatal(err)
	}
	changed := b
	changed.Attributes = map[string]any{"identifier": "9007199254740992"}
	if err := s.Bind(ctx, changed); err == nil {
		t.Fatal("changed immutable identifier")
	}
	otherID := newWaitingGoal(t, s, c)
	other := b
	other.GoalID = otherID
	if err := s.Bind(ctx, other); err == nil {
		t.Fatal("external resource acquired two owners")
	}
	stale := c
	stale.Version = "1"
	r, err := s.ReceiveIntegration(ctx, IntegrationReceipt{Source: c.Source, Instance: b.Instance, DeliveryID: "stale", Condition: &stale})
	if err != nil || r.Disposition != "ignored" {
		t.Fatalf("stale version %+v %v", r, err)
	}
	assertGoalState(t, s, id, "WAITING")
}

func TestBindingSerializesWithCancellation(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	c := Condition{"approval", "approved", "request", "2"}
	id := newWaitingGoal(t, s, c)
	locked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	defer unlock()
	cancelled := make(chan error, 1)
	go func() {
		cancelled <- s.tx(ctx, func(tx *transaction) error {
			if _, err := tx.client.Goal.Query().Where(goal.IDEQ(id)).ForUpdate().Only(ctx); err != nil {
				return err
			}
			close(locked)
			<-release
			return transition(ctx, tx, id, "CANCELLED")
		})
	}()
	select {
	case <-locked:
	case err := <-cancelled:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not lock")
	}
	bound := make(chan error, 1)
	go func() { bound <- s.Bind(ctx, Binding{GoalID: id, Generation: 1, Instance: "service", Condition: c}) }()
	select {
	case err := <-bound:
		t.Fatalf("binding bypassed in-flight domain transition: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	if err := <-cancelled; err != nil {
		t.Fatal(err)
	}
	if err := <-bound; err == nil {
		t.Fatal("bound a cancelled Goal")
	}
}

func TestLegacyIntegrationReceiptRevalidation(t *testing.T) {
	for _, source := range []string{"github", "gitlab"} {
		t.Run(source, func(t *testing.T) {
			s := testStore(t, true)
			ctx := context.Background()
			c := Condition{source, "approved", "request", "2"}
			id := newWaitingGoal(t, s, c)
			b := Binding{GoalID: id, Generation: 1, Instance: "service", Condition: c}
			if err := s.Bind(ctx, b); err != nil {
				t.Fatal(err)
			}
			raw := []byte("original signed external payload")
			// Historical inbox identity: raw digest, no Go fingerprint; imported
			// ignored receipts must not gain execution authority from migration.
			if err := s.tx(ctx, func(tx *transaction) error {
				q := tx.client.IntegrationDelivery.Create().SetOrganization(s.Organization).SetSource(source).SetInstance(b.Instance).SetDeliveryID("legacy").SetDigest(hash(raw)).SetDetails(map[string]any{"legacy": "evidence"}).SetRawBody(raw).SetDisposition("ignored").SetReceivedAt(tx.now)
				if source == "gitlab" {
					q.SetCondition(jsonObject(c))
				}
				return q.Exec(ctx)
			}); err != nil {
				t.Fatal(err)
			}
			assertGoalState(t, s, id, "WAITING")
			receipt := IntegrationReceipt{Source: source, Instance: b.Instance, DeliveryID: "legacy", Condition: &c, RawBody: raw}
			r, err := s.ReceiveIntegration(ctx, receipt)
			if err != nil || r.Disposition != "accepted" {
				t.Fatalf("legacy revalidation %+v %v", r, err)
			}
			r, err = s.ReceiveIntegration(ctx, receipt)
			if err != nil || r.Disposition != "duplicate" {
				t.Fatalf("legacy duplicate %+v %v", r, err)
			}
			changed := c
			changed.Version = "3"
			receipt.Condition = &changed
			if _, err = s.ReceiveIntegration(ctx, receipt); err == nil {
				t.Fatal("changed normalized event retained same identity")
			}
			assertGoalState(t, s, id, "READY")
		})
	}
}

func TestPendingReceiptAuthorizationCanUpgradeButNeverDowngrade(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	c := Condition{"github", "workflow.completed", "11/12/4/81/2", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	id := newWaitingGoal(t, s, c)
	raw := []byte("exact signed webhook bytes")
	pending := IntegrationReceipt{Source: c.Source, Instance: "repository:11", DeliveryID: "delivery-1", Details: map[string]any{"trust": "signed"}, RawBody: raw}
	result, err := s.ReceiveIntegration(ctx, pending)
	if err != nil || result.Disposition != "ignored" {
		t.Fatalf("pending=%+v err=%v", result, err)
	}
	if err = s.Bind(ctx, Binding{GoalID: id, Generation: 1, Instance: pending.Instance, Condition: c}); err != nil {
		t.Fatal(err)
	}
	authorized := pending
	authorized.Condition = &c
	result, err = s.ReceiveIntegration(ctx, authorized)
	if err != nil || result.Disposition != "accepted" {
		t.Fatalf("authorized=%+v err=%v", result, err)
	}
	// Mutable currentness may later fail, but identical signed bytes remain a
	// duplicate instead of changing or conflicting with retained authority.
	result, err = s.ReceiveIntegration(ctx, pending)
	if err != nil || result.Disposition != "duplicate" {
		t.Fatalf("downgrade=%+v err=%v", result, err)
	}
	assertGoalState(t, s, id, "READY")
}

func TestIntegrationApprovalRouting(t *testing.T) {
	for _, source := range []string{"approval-service", "dataset", "gitlab", "github"} {
		t.Run(source, func(t *testing.T) {
			s := testStore(t, true)
			ctx := context.Background()
			c := Condition{source, "approved", "request-123", "2"}
			id := newWaitingGoal(t, s, c)
			binding := Binding{GoalID: id, Generation: 1, Instance: "company-a", Condition: c, Attributes: map[string]any{"authorized_group": "release-managers"}}
			if err := s.Bind(ctx, binding); err != nil {
				t.Fatal(err)
			}
			if err := s.Bind(ctx, binding); err != nil {
				t.Fatal("idempotent binding", err)
			}
			changed := binding
			changed.Instance = "company-b"
			if err := s.Bind(ctx, changed); err == nil {
				t.Fatal("binding was mutable")
			}
			receipt := IntegrationReceipt{Source: source, Instance: "company-a", DeliveryID: "approval-1", Condition: &c, RawBody: []byte("verified event")}
			wrongInstance := receipt
			wrongInstance.Instance = "company-b"
			result, err := s.ReceiveIntegration(ctx, wrongInstance)
			if err != nil || result.Disposition != "ignored" {
				t.Fatalf("wrong instance %+v %v", result, err)
			}
			unnormalized := receipt
			unnormalized.DeliveryID = "untrusted-content"
			unnormalized.Condition = nil
			result, err = s.ReceiveIntegration(ctx, unnormalized)
			if err != nil || result.Disposition != "ignored" {
				t.Fatalf("unnormalized %+v %v", result, err)
			}
			other := &Store{DB: s.DB, Organization: "other-org"}
			result, err = other.ReceiveIntegration(ctx, receipt)
			if err != nil || result.Disposition != "ignored" {
				t.Fatalf("other tenant %+v %v", result, err)
			}
			assertGoalState(t, s, id, "WAITING")
			result, err = s.ReceiveIntegration(ctx, receipt)
			if err != nil || result.Disposition != "accepted" {
				t.Fatalf("matched %+v %v", result, err)
			}
			duplicate, err := s.ReceiveIntegration(ctx, receipt)
			if err != nil || duplicate.Disposition != "duplicate" || duplicate.ID != result.ID {
				t.Fatalf("duplicate %+v %v", duplicate, err)
			}
			changedReceipt := receipt
			changedReceipt.RawBody = []byte("different event")
			if _, err = s.ReceiveIntegration(ctx, changedReceipt); err == nil {
				t.Fatal("accepted altered receipt")
			}
			assertGoalState(t, s, id, "READY")
		})
	}
}

func TestEarlyApprovalSurvivesRestartAndBinding(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	c := Condition{"approval", "approved", "deployment-1", "revision-3"}
	id := newWaitingGoal(t, s, c)
	receipt := IntegrationReceipt{Source: c.Source, Instance: "external-service", DeliveryID: "early", Condition: &c, RawBody: []byte("already verified")}
	r, err := s.ReceiveIntegration(ctx, receipt)
	if err != nil || r.Disposition != "ignored" {
		t.Fatalf("early %+v %v", r, err)
	}
	assertGoalState(t, s, id, "WAITING")
	restarted := &Store{DB: s.DB, Organization: s.Organization}
	if err = restarted.Bind(ctx, Binding{GoalID: id, Generation: 1, Instance: receipt.Instance, Condition: c}); err != nil {
		t.Fatal(err)
	}
	assertGoalState(t, restarted, id, "READY")
}

func TestConcurrentBindingAndApproval(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	c := Condition{"approval", "approved", "deployment-1", "2"}
	id := newWaitingGoal(t, s, c)
	b := Binding{GoalID: id, Generation: 1, Instance: "service", Condition: c}
	receipt := IntegrationReceipt{Source: c.Source, Instance: b.Instance, DeliveryID: "concurrent", Condition: &c}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs <- s.Bind(ctx, b) }()
	go func() { defer wg.Done(); _, err := s.ReceiveIntegration(ctx, receipt); errs <- err }()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertGoalState(t, s, id, "READY")
	if err := s.tx(ctx, func(tx *transaction) error {
		n, err := tx.client.Outbox.Query().Where(outbox.GoalIDEQ(id), outbox.KindEQ("wake")).Count(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("wake count %d", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

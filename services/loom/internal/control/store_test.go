package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/piglor/loom/services/loom/ent/attempt"
	"github.com/piglor/loom/services/loom/ent/integrationbinding"
	"github.com/piglor/loom/services/loom/ent/integrationdelivery"
	"github.com/piglor/loom/services/loom/ent/outbox"
	"github.com/piglor/loom/services/loom/ent/wait"
)

var testDSN string

// Every run uses a disposable PostgreSQL container, or creates an isolated
// schema in an explicitly supplied test database. Never use production.env.
func TestMain(m *testing.M) {
	testDSN = os.Getenv("LOOM_GO_TEST_DATABASE_URL")
	container := ""
	if testDSN == "" {
		output, err := exec.Command("docker", "run", "--detach", "--rm", "-e", "POSTGRES_PASSWORD=loom-test-only", "-e", "POSTGRES_DB=loom_test", "-p", "127.0.0.1::5432", "postgres:15.6").Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Go persistence tests require Docker or LOOM_GO_TEST_DATABASE_URL: %v\n", err)
			os.Exit(1)
		}
		container = strings.TrimSpace(string(output))
		port, err := exec.Command("docker", "port", container, "5432/tcp").Output()
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot resolve disposable PostgreSQL port:", err)
			exec.Command("docker", "stop", container).Run()
			os.Exit(1)
		}
		testDSN = "postgres://postgres:loom-test-only@" + strings.TrimSpace(string(port)) + "/loom_test?sslmode=disable"
	}
	code := m.Run()
	if container != "" {
		if err := exec.Command("docker", "stop", container).Run(); err != nil {
			fmt.Fprintln(os.Stderr, "test container cleanup failed", err)
			code = 1
		}
	}
	os.Exit(code)
}

func testStore(t *testing.T, migrate bool) *Store {
	t.Helper()
	ctx := context.Background()
	config, err := pgx.ParseConfig(testDSN)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	admin := stdlib.OpenDB(*config)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err = admin.PingContext(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			admin.Close()
			t.Fatal("test PostgreSQL unavailable")
		}
		time.Sleep(100 * time.Millisecond)
	}
	schema := "loom_test_" + strings.ReplaceAll(ID(), "-", "")
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	s := &Store{DB: db, Organization: "test-org"}
	if migrate {
		if err = s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestFreshGenericSchema(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name LIKE 'github%'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("fresh core created %d GitHub tables", count)
	}
}

func newWaitingGoal(t *testing.T, s *Store, c Condition) string {
	t.Helper()
	ctx := context.Background()
	id, err := s.Create(ctx, CreateGoal{Title: "External approval", Objective: "Continue after authorized approval", Condition: c})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.tx(ctx, func(tx *transaction) error {
		if err := tx.client.Wait.Update().Where(wait.GoalIDEQ(id)).SetArmedAt(tx.now).Exec(ctx); err != nil {
			return err
		}
		return transition(ctx, tx, id, "WAITING")
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestGenericExternalWaits(t *testing.T) {
	for _, source := range []string{"approval-service", "deployment", "dataset", "gitlab", "github"} {
		t.Run(source, func(t *testing.T) {
			s := testStore(t, true)
			ctx := context.Background()
			condition := Condition{Source: source, Type: "approved", Resource: "request-123", Version: "revision-2"}
			id := newWaitingGoal(t, s, condition)
			e := Event{Condition: condition, GoalID: id, DeliveryID: "receipt-1", Generation: 1}
			for _, tc := range []struct {
				name   string
				change func(*Event)
				want   string
			}{
				{"wrong resource", func(e *Event) { e.Resource = "request-other" }, "mismatch"},
				{"stale version", func(e *Event) { e.Version = "revision-1" }, "mismatch"},
				{"future generation", func(e *Event) { e.Generation = 2 }, "mismatch"},
				{"wrong source", func(e *Event) { e.Source = "unrelated" }, "mismatch"},
				{"unknown goal", func(e *Event) { e.GoalID = ID() }, "unknown_goal"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					bad := e
					bad.DeliveryID = ID()
					tc.change(&bad)
					r, err := s.Receive(ctx, bad)
					if err != nil || r.Disposition != tc.want {
						t.Fatalf("result=%+v err=%v", r, err)
					}
				})
			}
			// A fresh Store represents control-plane restart: no process state owns the wait.
			restarted := &Store{DB: s.DB, Organization: s.Organization}
			if err := restarted.tx(ctx, func(tx *transaction) error {
				n, err := tx.client.Attempt.Query().Where(attempt.GoalIDEQ(id)).Count(ctx)
				if err != nil {
					return err
				}
				if n != 0 {
					t.Fatalf("waiting created %d executions", n)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			foreign := &Store{DB: s.DB, Organization: "other-org"}
			r, err := foreign.Receive(ctx, e)
			if err != nil || r.Disposition != "unknown_goal" {
				t.Fatalf("cross-tenant %+v %v", r, err)
			}
			r, err = restarted.Receive(ctx, e)
			if err != nil || r.Disposition != "accepted" {
				t.Fatalf("approval %+v %v", r, err)
			}
			duplicate, err := restarted.Receive(ctx, e)
			if err != nil || duplicate.Disposition != "duplicate" || duplicate.ID != r.ID {
				t.Fatalf("duplicate %+v %v", duplicate, err)
			}
			altered := e
			altered.Version = "revision-3"
			if _, err = restarted.Receive(ctx, altered); err == nil {
				t.Fatal("accepted reused delivery with different content")
			}
			if err = restarted.tx(ctx, func(tx *transaction) error {
				g, err := tx.client.Goal.Get(ctx, id)
				if err != nil {
					return err
				}
				if g.State != "READY" {
					t.Fatalf("approval should make ready, not complete: %s", g.State)
				}
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
		})
	}
}

func TestConcurrentDuplicateApproval(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	c := Condition{"human", "approval", "change-1", "2"}
	id := newWaitingGoal(t, s, c)
	e := Event{Condition: c, GoalID: id, DeliveryID: "same", Generation: 1}
	var wg sync.WaitGroup
	results := make(chan EventResult, 16)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, err := s.Receive(ctx, e); results <- r; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	accepted := 0
	for r := range results {
		if r.Disposition == "accepted" {
			accepted++
		} else if r.Disposition != "duplicate" {
			t.Fatalf("unexpected %+v", r)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d concurrent approvals", accepted)
	}
}

func TestGeneratedMutationRollsBackWithTransaction(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	id := newWaitingGoal(t, s, Condition{"human", "approval", "request", "1"})
	failure := errors.New("abort after generated UpdateOne")
	err := s.tx(ctx, func(tx *transaction) error {
		if err := transition(ctx, tx, id, "READY"); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := s.tx(ctx, func(tx *transaction) error {
		g, err := tx.client.Goal.Get(ctx, id)
		if err != nil {
			return err
		}
		if g.State != "WAITING" {
			t.Fatal("nested generated mutation committed early")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyBindingMigrationPreservesData(t *testing.T) {
	s := testStore(t, false)
	ctx := context.Background()
	for _, path := range []string{"migrations/001-schema.sql", "migrations/002-workers.sql", "legacy/003-github.sql", "legacy/004-github-inbox.sql", "legacy/005-reconcile.sql", "migrations/006-provider-binding.sql"} {
		body, err := migrations.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.DB.ExecContext(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	id := ID()
	receipt := ID()
	oldCondition := `{"source":"github","type":"workflow.completed","resource":"456/7/89/1","version":"abc"}`
	fixture := `INSERT INTO goals(id,organization,title,objective,state,completion_criteria) VALUES($1,'test-org','legacy','legacy','WAITING',jsonb_build_object('event_matches',$2::jsonb));`
	if _, err := s.DB.ExecContext(ctx, fixture, id, oldCondition); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO waits(id,goal_id,generation,condition,armed_at) VALUES($1,$2,1,$3::jsonb,clock_timestamp())`, ID(), id, oldCondition); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO github_bindings VALUES($1,'test-org',123,456,7,'abc',89,1,22,NULL)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO github_deliveries(organization,delivery_id,digest,disposition,event_type,raw_body,payload) VALUES('test-org',$1,'digest','ignored','workflow_run',$2,'{"installation":{"id":123}}')`, receipt, []byte("original signed bytes")); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.tx(ctx, func(tx *transaction) error {
		b, err := tx.client.IntegrationBinding.Query().Where(integrationbinding.GoalIDEQ(id)).Only(ctx)
		if err != nil {
			return err
		}
		if b.Source != "github" || b.Instance != "installation:123" || b.Resource != "456/7/22/89/1" || b.Version != "abc" || b.Generation != 1 || b.Attributes["workflow_id"] != float64(22) {
			t.Fatalf("incorrect imported binding %+v", b)
		}
		w, err := tx.client.Wait.Query().Where(wait.GoalIDEQ(id)).Only(ctx)
		if err != nil {
			return err
		}
		if w.Condition["resource"] != "456/7/22/89/1" {
			t.Fatalf("legacy wait was stranded: %+v", w.Condition)
		}
		g, err := tx.client.Goal.Get(ctx, id)
		if err != nil {
			return err
		}
		criterion, _ := g.CompletionCriteria["event_matches"].(map[string]any)
		if criterion["resource"] != "456/7/22/89/1" {
			t.Fatalf("legacy completion condition was stranded: %+v", g.CompletionCriteria)
		}
		d, err := tx.client.IntegrationDelivery.Query().Where(integrationdelivery.DeliveryIDEQ(receipt)).Only(ctx)
		if err != nil {
			return err
		}
		if string(d.RawBody) != "original signed bytes" || d.Condition != nil || d.Digest != "digest" || d.Instance != "installation:123" {
			t.Fatalf("receipt corrupted %+v", d)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM github_bindings").Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("legacy originals lost: %d %v", retained, err)
	}
}

func TestCanonicalEventIdentity(t *testing.T) {
	b, err := canonicalJSON(map[string]any{"z": "你好😀", "a": "<a>,\"\\"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"a":"<a>,\"\\","z":"\u4f60\u597d\ud83d\ude00"}` {
		t.Fatalf("canonical %s", b)
	}
	b, err = canonicalJSON([]string{"org", "source", "id"}, true)
	if err != nil || string(b) != `["org", "source", "id"]` {
		t.Fatalf("lock identity %s %v", b, err)
	}
	b, err = canonicalJSON(map[string]string{"value": "\x7f"}, false)
	if err != nil || string(b) != `{"value":"\u007f"}` {
		t.Fatalf("DEL identity %s %v", b, err)
	}
}

func TestLegacyCanonicalReceiptReplay(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	c := Condition{"human", "approval", "request\x7f", "2"}
	id := newWaitingGoal(t, s, c)
	e := Event{Condition: c, GoalID: id, DeliveryID: "legacy", Generation: 1}
	// Literal legacy JSON convention, independent of the new serializer.
	body := fmt.Sprintf(`{"delivery_id":"legacy","generation":1,"goal_id":"%s","resource":"request\u007f","source":"human","type":"approval","version":"2"}`, id)
	if err := s.tx(ctx, func(tx *transaction) error {
		return tx.client.Event.Create().SetOrganization(s.Organization).SetSource(c.Source).SetDeliveryID(e.DeliveryID).SetDigest(hash([]byte(body))).SetBody(jsonObject(e)).SetDisposition("accepted").SetReceivedAt(tx.now).Exec(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	e.GoalID = strings.ToUpper(id)
	r, err := s.Receive(ctx, e)
	if err != nil || r.Disposition != "duplicate" {
		t.Fatalf("legacy redelivery: %+v %v", r, err)
	}
}

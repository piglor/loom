import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  BrowserRouter,
  Link,
  NavLink,
  Route,
  Routes,
  useParams,
} from "react-router-dom";
import {
  APIError,
  createClient,
  type Goal,
  type GoalSummary,
} from "@piglor/loom-client";
import "./style.css";

type Client = ReturnType<typeof createClient>;
const labels: Record<string, string> = {
  READY: "Ready",
  RUNNING: "Working",
  WAITING: "Suspended",
  BLOCKED: "Needs you",
  COMPLETED: "Completed",
  FAILED: "Failed",
  CANCELLED: "Cancelled",
};
function Status({ state }: { state: string }) {
  return (
    <span className={`status state-${state.toLowerCase()}`}>
      {labels[state] ?? state}
    </span>
  );
}
function duration(value: string | number) {
  const seconds = Math.max(0, Number(value));
  if (!Number.isFinite(seconds)) return "Unavailable";
  if (seconds < 1) return `${Math.round(seconds * 1000)} ms`;
  if (seconds < 60) return `${Math.floor(seconds)}s`;
  if (seconds < 3600)
    return `${Math.floor(seconds / 60)}m ${Math.floor(seconds % 60)}s`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
}
function date(value: string) {
  return new Date(value).toLocaleString();
}
function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "Unable to load data.";
}

function Login({ onLogin }: { onLogin: (token: string) => void }) {
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    document.title = "Sign in · Loom";
  }, []);
  return (
    <main className="login">
      <div className="brand">
        <span className="mark">L</span> loom <small>by Piglor</small>
      </div>
      <h1>
        Pay for thinking.
        <br />
        <span>Not waiting.</span>
      </h1>
      <p>Your Goals keep moving—even when your agents don’t.</p>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError("");
          try {
            await createClient(token).goals();
            onLogin(token);
            setToken("");
          } catch (e) {
            setError(errorMessage(e));
          } finally {
            setBusy(false);
          }
        }}
      >
        <h2>Open your control plane</h2>
        <label htmlFor="token">Operator API token</label>
        <input
          id="token"
          type="password"
          autoComplete="off"
          required
          value={token}
          onChange={(e) => setToken(e.target.value)}
        />
        <p className="muted">
          Single-organization operator access. Your token stays in memory;
          refreshing signs you out. Never use a Hatchet or worker token here.
        </p>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <button disabled={busy}>
          {busy ? "Connecting…" : "Connect to Loom →"}
        </button>
      </form>
      <p className="footnote">
        Finite-runtime preview · No unattended coding agents enabled
      </p>
    </main>
  );
}

function GoalList({
  client,
  attention,
  onUnauthorized,
}: {
  client: Client;
  attention?: boolean;
  onUnauthorized: () => void;
}) {
  const [goals, setGoals] = useState<GoalSummary[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [revision, setRevision] = useState(0);
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState("ALL");
  useEffect(() => {
    const abort = new AbortController();
    setLoading(true);
    setError("");
    client
      .goals(abort.signal)
      .then(setGoals)
      .catch((e) => {
        if (!abort.signal.aborted) {
          if (e instanceof APIError && e.status === 401) onUnauthorized();
          else setError(errorMessage(e));
        }
      })
      .finally(() => {
        if (!abort.signal.aborted) setLoading(false);
      });
    return () => abort.abort();
  }, [client, revision, onUnauthorized]);
  useEffect(() => {
    document.title = `${attention ? "Needs you" : "Goals"} · Loom`;
  }, [attention]);
  const shown = goals.filter(
    (g) =>
      (!attention || ["BLOCKED", "FAILED"].includes(g.state)) &&
      (filter === "ALL" || g.state === filter) &&
      `${g.title} ${g.id}`.toLowerCase().includes(search.toLowerCase()),
  );
  return (
    <>
      <header className="page-heading">
        <div>
          <p className="eyebrow">CONTROL PLANE</p>
          <h1>{attention ? "Needs you" : "Your Goals"}</h1>
          <p className="muted">
            {attention
              ? "Blocked and failed Goals worth your attention."
              : "Long-running outcomes. Reasoning only when it matters."}
          </p>
        </div>
        <button
          className="secondary"
          onClick={() => setRevision((r) => r + 1)}
          disabled={loading}
        >
          ↻ Refresh
        </button>
      </header>
      <div className="stats">
        {[
          ["Working", goals.filter((g) => g.state === "RUNNING").length],
          ["Suspended", goals.filter((g) => g.state === "WAITING").length],
          [
            "Needs you",
            goals.filter((g) => ["BLOCKED", "FAILED"].includes(g.state)).length,
          ],
          ["Completed", goals.filter((g) => g.state === "COMPLETED").length],
        ].map(([label, value]) => (
          <div className="stat" key={label}>
            <span>{label}</span>
            <strong>{error || loading ? "—" : value}</strong>
          </div>
        ))}
      </div>
      <section className="panel">
        <div className="toolbar">
          <label className="search">
            Find a Goal
            <input
              aria-label="Find a Goal"
              placeholder="Search title or ID…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </label>
          <label>
            State
            <select value={filter} onChange={(e) => setFilter(e.target.value)}>
              <option value="ALL">All states</option>
              {Object.entries(labels).map(([state, label]) => (
                <option key={state} value={state}>
                  {label}
                </option>
              ))}
            </select>
          </label>
        </div>
        {error ? (
          <div role="alert" className="empty error">
            {error}
          </div>
        ) : loading ? (
          <div role="status" className="empty">
            Loading Goals…
          </div>
        ) : shown.length === 0 ? (
          <div className="empty">
            <h2>
              {goals.length
                ? attention && !search && filter === "ALL"
                  ? "Nothing here needs attention"
                  : "No matching Goals"
                : "No Goals yet"}
            </h2>
            <p>
              {goals.length
                ? "Try another search or state filter."
                : "Create a Goal through the Loom CLI or API to see it here."}
            </p>
          </div>
        ) : (
          <div className="goal-list">
            {shown.map((goal) => (
              <Link className="goal-row" to={`/goals/${goal.id}`} key={goal.id}>
                <div>
                  <h2>{goal.title}</h2>
                  <span className="muted mono">{goal.id.slice(0, 8)}</span>{" "}
                  <span className="muted">· {date(goal.updated_at)}</span>
                </div>
                <Status state={goal.state} />
                <span aria-hidden="true">↗</span>
              </Link>
            ))}
          </div>
        )}
        <p className="panel-footer">
          Latest {goals.length} Goals · API currently returns at most 100 ·
          Refresh is manual
        </p>
      </section>
      <aside className="note">
        <strong>Waiting is a valid state.</strong> A suspended Goal does not
        need a running agent. Open a Goal to see its wait condition, execution
        history and worker binding.
      </aside>
    </>
  );
}

function GoalDetail({
  client,
  onUnauthorized,
}: {
  client: Client;
  onUnauthorized: () => void;
}) {
  const { id = "" } = useParams();
  const [goal, setGoal] = useState<Goal | null>(null);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const abort = new AbortController();
    setGoal(null);
    setError("");
    client
      .goal(id, abort.signal)
      .then(setGoal)
      .catch((e) => {
        if (!abort.signal.aborted) {
          if (e instanceof APIError && e.status === 401) onUnauthorized();
          else setError(errorMessage(e));
        }
      });
    return () => abort.abort();
  }, [client, id, revision, onUnauthorized]);
  useEffect(() => {
    document.title = `${goal?.title ?? "Goal"} · Loom`;
  }, [goal]);
  if (error)
    return (
      <section className="panel empty" role="alert">
        <h1>Unable to open Goal</h1>
        <p>{error}</p>
        <button onClick={() => setRevision((r) => r + 1)}>Retry</button>{" "}
        <Link to="/goals">Back to Goals</Link>
      </section>
    );
  if (!goal) return <p role="status">Loading Goal…</p>;
  const condition = goal.wait.condition;
  const running = goal.attempts.some(
    (a) => a.state === "RUNNING" || a.state === "UNKNOWN",
  );
  return (
    <>
      <Link className="back" to="/goals">
        ← All Goals
      </Link>
      <header className="page-heading">
        <div>
          <p className="eyebrow">GOAL · {goal.id.slice(0, 8)}</p>
          <h1>{goal.title}</h1>
          <Status state={goal.state} />
        </div>
        <button className="secondary" onClick={() => setRevision((r) => r + 1)}>
          ↻ Refresh
        </button>
      </header>
      <p className="objective">{goal.objective}</p>
      <div className="stats">
        {[
          ["Lifetime", duration(goal.metrics.lifetime_seconds)],
          ["Execution recorded", duration(goal.metrics.execution_seconds)],
          ["Suspended", duration(goal.metrics.suspended_seconds)],
          ["Wake-ups", goal.metrics.wake_ups],
        ].map(([label, value]) => (
          <div className="stat" key={label}>
            <span>{label}</span>
            <strong>{value}</strong>
          </div>
        ))}
      </div>
      <p className="footnote">
        {goal.session.runtime.includes("demo")
          ? "Finite demo runtime—not model inference. "
          : ""}
        Execution includes reported finished attempts. Tokens and provider cost
        are unavailable; no savings estimate is claimed.
      </p>
      <div className="detail-grid">
        <section className="panel">
          <div className="section-heading">
            <h2>
              {goal.state === "WAITING"
                ? "What wakes this Goal?"
                : "Wait contract"}
            </h2>
            <span className="muted">Generation {goal.wait.generation}</span>
          </div>
          <dl>
            <dt>Source</dt>
            <dd>{condition.source}</dd>
            <dt>Event</dt>
            <dd>{condition.type}</dd>
            <dt>Resource</dt>
            <dd className="mono">{condition.resource}</dd>
            <dt>Expected version</dt>
            <dd className="mono">{condition.version}</dd>
            <dt>Reason</dt>
            <dd>{goal.waiting_reason ?? "External dependency"}</dd>
            <dt>Matched event</dt>
            <dd className="mono">{goal.wait.event_id ?? "None yet"}</dd>
          </dl>
          {goal.state === "WAITING" && (
            <p className="wait-callout">
              {running
                ? "Execution state requires reconciliation. Inspect attempts below."
                : "No running or uncertain execution attempt is recorded."}
            </p>
          )}
        </section>
        <section className="panel">
          <div className="section-heading">
            <h2>Session & worker</h2>
          </div>
          <dl>
            <dt>Runtime</dt>
            <dd>{goal.session.runtime}</dd>
            <dt>Loom session</dt>
            <dd className="mono">{goal.session.id}</dd>
            <dt>Bound worker</dt>
            <dd className="mono">{goal.session.worker_id}</dd>
            <dt>Provider session</dt>
            <dd className="mono">
              {goal.session.provider_session_id ?? "Not available"}
            </dd>
            <dt>Run</dt>
            <dd className="mono">{goal.run.id}</dd>
          </dl>
          <p className="muted">
            Session affinity is explicit. This view does not infer worker
            availability.
          </p>
        </section>
      </div>
      <section className="panel">
        <div className="section-heading">
          <h2>Audit timeline</h2>
          <span className="muted">{goal.audit.length} transitions</span>
        </div>
        <ol className="timeline">
          {goal.audit.map((entry) => (
            <li key={entry.sequence}>
              <time>{date(entry.recorded_at)}</time>
              <div>
                <strong>{entry.action.replaceAll("_", " ")}</strong>
                <pre>{JSON.stringify(entry.details, null, 2)}</pre>
              </div>
            </li>
          ))}
        </ol>
      </section>
      <section className="panel">
        <div className="section-heading">
          <h2>Execution attempts</h2>
        </div>
        {goal.attempts.length === 0 ? (
          <p>No execution attempts yet.</p>
        ) : (
          goal.attempts.map((attempt, index) => (
            <div className="attempt" key={attempt.id}>
              <strong>Attempt {index + 1}</strong>
              <span>{attempt.state}</span>
              <span>{attempt.outcome ?? "No outcome reported"}</span>
              <span>
                {attempt.duration_ms === null
                  ? "Duration unavailable"
                  : duration(attempt.duration_ms / 1000)}
              </span>
            </div>
          ))
        )}
        <details>
          <summary>Completion criteria & policy</summary>
          <pre>
            {JSON.stringify(
              { criteria: goal.completion_criteria, policy: goal.run.policy },
              null,
              2,
            )}
          </pre>
        </details>
      </section>
    </>
  );
}

function App() {
  const [client, setClient] = useState<Client | null>(null);
  // Stable callback prevents request effects from restarting on every render.
  const [logout] = useState(() => () => setClient(null));
  if (!client)
    return <Login onLogin={(token) => setClient(createClient(token))} />;
  return (
    <div className="shell">
      <a className="skip" href="#content">
        Skip to content
      </a>
      <aside className="sidebar">
        <Link className="brand" to="/goals">
          <span className="mark">L</span> loom
        </Link>
        <p className="eyebrow">PIGLOR / CONTROL PLANE</p>
        <nav aria-label="Main navigation">
          <NavLink to="/goals">◎ Goals</NavLink>
          <NavLink to="/needs-you">◈ Needs you</NavLink>
        </nav>
        <div className="sidebar-bottom">
          <p>
            Pay for thinking,
            <br />
            <strong>not waiting.</strong>
          </p>
          <button className="secondary" onClick={logout}>
            Sign out
          </button>
          <small>Operator preview · Read-only console</small>
        </div>
      </aside>
      <main id="content" className="content" tabIndex={-1}>
        <Routes>
          <Route
            path="/"
            element={<GoalList client={client} onUnauthorized={logout} />}
          />
          <Route
            path="/goals"
            element={<GoalList client={client} onUnauthorized={logout} />}
          />
          <Route
            path="/needs-you"
            element={
              <GoalList
                key="attention"
                client={client}
                attention
                onUnauthorized={logout}
              />
            }
          />
          <Route
            path="/goals/:id"
            element={<GoalDetail client={client} onUnauthorized={logout} />}
          />
          <Route
            path="*"
            element={
              <>
                <h1>Page not found</h1>
                <Link to="/goals">Go to Goals</Link>
              </>
            }
          />
        </Routes>
      </main>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </StrictMode>,
);

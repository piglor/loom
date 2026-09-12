import { StrictMode, useEffect, useState, type FormEvent } from "react";
import { createRoot } from "react-dom/client";
import {
  BrowserRouter,
  Link,
  NavLink,
  Route,
  Routes,
  useLocation,
  useParams,
} from "react-router-dom";
import {
  APIError,
  createClient,
  type AuthUser,
  type Goal,
  type GoalSummary,
  type Plugin,
} from "@piglor/loom-client";
import { PluginSetup } from "./PluginSetup";
import { WorkflowEditor, WorkflowList } from "./Workflows";
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

function pluginStateLabel(plugin: Plugin) {
  switch (plugin.state) {
    case "connected":
      return `${plugin.connection_count} connected`;
    case "ready_to_connect":
      return "Ready to connect";
    case "needs_attention":
      return "Needs attention";
    default:
      return "Server setup needed";
  }
}

function Login({ onLogin }: { onLogin: (user: AuthUser) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [mode, setMode] = useState<"signin" | "register">("signin");
  const [socialProviders, setSocialProviders] = useState<
    { id: string; name: string }[]
  >([]);
  const [emailRegistrationEnabled, setEmailRegistrationEnabled] =
    useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    document.title = "Sign in · Loom";
    const client = createClient();
    const abort = new AbortController();
    client
      .authConfig(abort.signal)
      .then((config) => {
        setSocialProviders(config.social_providers ?? []);
        setEmailRegistrationEnabled(
          config.email_registration_enabled !== false,
        );
        setEmail((current) => current || config.bootstrap_email || "");
      })
      .catch(() => undefined);
    client
      .authSession(abort.signal)
      .then(({ user }) => onLogin(user))
      .catch((e) => {
        if (
          !abort.signal.aborted &&
          !(e instanceof APIError && e.status === 401)
        ) {
          setError(errorMessage(e));
        }
      });
    const callbackError = new URLSearchParams(window.location.search).get(
      "auth_error",
    );
    if (callbackError) setError("Social sign-in was not completed. Try again.");
    return () => abort.abort();
  }, [onLogin]);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const client = createClient();
      const response =
        mode === "register"
          ? await client.authRegister({ email, password })
          : await client.authLogin({ email, password });
      onLogin(response.user);
      setPassword("");
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <main className="login-shell">
      <section className="login-story">
        <div className="brand">
          <span className="mark">L</span> loom <small>by Piglor</small>
        </div>
        <div>
          <p className="eyebrow">THE AGENT CONTROL PLANE</p>
          <h1>
            Keep work moving.
            <br />
            <span>Without babysitting it.</span>
          </h1>
          <p>
            Connect your tools, hand Loom an outcome, and let verified events
            bring your agents back when there is real work to do.
          </p>
        </div>
        <div className="login-proof">
          <span>Work</span>
          <i aria-hidden="true">→</i>
          <span>Wait safely</span>
          <i aria-hidden="true">→</i>
          <span>Resume</span>
        </div>
      </section>
      <section className="login-card" aria-labelledby="login-title">
        <p className="eyebrow">WELCOME BACK</p>
        <h2 id="login-title">
          {mode === "signin"
            ? "Sign in to your workspace"
            : "Create your account"}
        </h2>
        <p className="login-intro">
          {mode === "signin"
            ? "Use your Loom account to pick up where work left off."
            : "Create an account with your email, or use a configured sign-in provider."}
        </p>
        {socialProviders.length > 0 && (
          <>
            {socialProviders.map((provider) => (
              <button
                className="social-login"
                type="button"
                key={provider.id}
                onClick={() =>
                  window.location.assign(
                    `/v1/auth/${encodeURIComponent(provider.id)}/start`,
                  )
                }
              >
                Continue with {provider.name}
              </button>
            ))}
            <div className="login-divider">or use email</div>
          </>
        )}
        <form onSubmit={submit}>
          <label htmlFor="email">Email address</label>
          <input
            id="email"
            type="email"
            autoComplete="email"
            required
            value={email}
            onChange={(event) => setEmail(event.target.value)}
          />
          <label htmlFor="password">Password</label>
          <input
            id="password"
            type="password"
            autoComplete={
              mode === "signin" ? "current-password" : "new-password"
            }
            minLength={12}
            required
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
          {mode === "register" && (
            <p className="muted">Use at least 12 characters.</p>
          )}
          {error && (
            <p role="alert" className="error">
              {error}
            </p>
          )}
          <button disabled={busy} type="submit">
            {busy
              ? mode === "signin"
                ? "Signing in…"
                : "Creating account…"
              : mode === "signin"
                ? "Sign in"
                : "Create account"}
          </button>
        </form>
        {emailRegistrationEnabled && (
          <p className="auth-switch">
            {mode === "signin" ? "New to Loom?" : "Already have an account?"}{" "}
            <button
              type="button"
              className="text-button"
              onClick={() => {
                setMode(mode === "signin" ? "register" : "signin");
                setError("");
              }}
            >
              {mode === "signin" ? "Create an account" : "Sign in"}
            </button>
          </p>
        )}
        <p className="token-note">
          The initial administrator defaults to admin@example.com and can be
          changed by your Loom operator.
        </p>
      </section>
    </main>
  );
}

function RouteEffects() {
  const { pathname } = useLocation();
  useEffect(() => {
    window.scrollTo({ top: 0, left: 0 });
  }, [pathname]);
  return null;
}

function GitHubGlyph() {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" className="plugin-glyph">
      <path
        fill="currentColor"
        d="M12 2a10 10 0 0 0-3.16 19.49c.5.09.68-.22.68-.48v-1.87c-2.78.6-3.37-1.18-3.37-1.18-.45-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.61.07-.61 1 .07 1.53 1.03 1.53 1.03.9 1.53 2.35 1.09 2.92.83.09-.65.35-1.09.64-1.34-2.22-.25-4.55-1.11-4.55-4.94 0-1.09.39-1.98 1.03-2.68-.1-.25-.45-1.27.1-2.64 0 0 .84-.27 2.75 1.02A9.6 9.6 0 0 1 12 6.82a9.6 9.6 0 0 1 2.5.34c1.91-1.3 2.75-1.02 2.75-1.02.55 1.37.2 2.39.1 2.64.64.7 1.03 1.59 1.03 2.68 0 3.84-2.34 4.68-4.57 4.93.36.31.68.92.68 1.85v2.77c0 .27.18.58.69.48A10 10 0 0 0 12 2Z"
      />
    </svg>
  );
}

function PluginGlyph({ plugin }: { plugin: Plugin }) {
  return plugin.id === "github" ? (
    <GitHubGlyph />
  ) : (
    <span aria-hidden="true">{plugin.name.slice(0, 1).toUpperCase()}</span>
  );
}

function Home({
  client,
  onUnauthorized,
  canAdmin,
}: {
  client: Client;
  onUnauthorized: () => void;
  canAdmin: boolean;
}) {
  const { plugins, loading } = usePlugins(client, onUnauthorized);
  const github = plugins.find((plugin) => plugin.id === "github");
  useEffect(() => {
    document.title = "Home · Loom";
  }, []);
  return (
    <>
      <header className="welcome">
        <div>
          <p className="eyebrow">WELCOME TO LOOM</p>
          <h1>Let’s get Loom working for you</h1>
          <p>
            Connect the tools your team already uses. Loom can then pause work,
            wait for trusted events, and continue when there is something to do.
          </p>
        </div>
        <div className="welcome-orbit" aria-hidden="true">
          <span className="orbit-core">L</span>
          <span className="orbit-node orbit-one">Git</span>
          <span className="orbit-node orbit-two">CI</span>
          <span className="orbit-node orbit-three">AI</span>
        </div>
      </header>
      <section className="setup-section" aria-labelledby="setup-title">
        <div className="section-heading generous">
          <div>
            <p className="eyebrow">START HERE</p>
            <h2 id="setup-title">
              Connect the first service Loom can listen to
            </h2>
          </div>
          <span className="progress-pill">Recommended path</span>
        </div>
        <div className="setup-grid">
          <article className="setup-card featured">
            <div className="plugin-logo">
              <GitHubGlyph />
            </div>
            <h3>
              {github?.state === "connected"
                ? "GitHub is connected"
                : "Connect your first plugin"}
            </h3>
            <p>
              {github?.state === "connected"
                ? `${github.connection_count} GitHub connection${github.connection_count === 1 ? "" : "s"} ready`
                : "Start with GitHub as a verified event source for your workflows."}
            </p>
            <Link className="button-link" to="/plugins/github">
              {loading
                ? "Checking setup…"
                : github?.state === "connected"
                  ? "Manage GitHub"
                  : canAdmin
                    ? "Connect GitHub"
                    : "View GitHub"}{" "}
              <span aria-hidden="true">→</span>
            </Link>
          </article>
        </div>
      </section>
      <section className="explainer-strip">
        <div>
          <span>1</span>
          <p>
            <strong>An agent works</strong>
            <br />
            Only while reasoning or acting
          </p>
        </div>
        <i aria-hidden="true">→</i>
        <div>
          <span>2</span>
          <p>
            <strong>Loom waits</strong>
            <br />
            No model kept running
          </p>
        </div>
        <i aria-hidden="true">→</i>
        <div>
          <span>3</span>
          <p>
            <strong>A workflow receives evidence</strong>
            <br />
            After a verified event
          </p>
        </div>
      </section>
    </>
  );
}

function usePlugins(client: Client, onUnauthorized: () => void) {
  const [plugins, setPlugins] = useState<Plugin[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  useEffect(() => {
    const abort = new AbortController();
    setLoading(true);
    client
      .plugins(abort.signal)
      .then(setPlugins)
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
  }, [client, onUnauthorized]);
  return { plugins, loading, error };
}

function PluginStore({
  client,
  onUnauthorized,
}: {
  client: Client;
  onUnauthorized: () => void;
}) {
  const { plugins, loading, error } = usePlugins(client, onUnauthorized);
  useEffect(() => {
    document.title = "Plugin store · Loom";
  }, []);
  return (
    <>
      <header className="page-heading store-heading">
        <div>
          <p className="eyebrow">EXTEND LOOM</p>
          <h1>Plugin store</h1>
          <p className="page-intro">
            Add trusted event sources. Workflows decide whether an event starts
            or continues work.
          </p>
        </div>
      </header>
      <div className="category-tabs" aria-label="Plugin categories">
        <span className="active">All plugins</span>
      </div>
      {error ? (
        <section className="panel empty error" role="alert">
          {error}
        </section>
      ) : loading ? (
        <section className="panel empty" role="status">
          Loading plugins…
        </section>
      ) : (
        <section className="plugin-grid" aria-label="Available plugins">
          {plugins.map((plugin) => (
            <Link
              className="plugin-card"
              to={`/plugins/${plugin.id}`}
              key={plugin.id}
            >
              <div className="plugin-card-top">
                <div className="plugin-logo">
                  <PluginGlyph plugin={plugin} />
                </div>
                <span className={`plugin-state ${plugin.state}`}>
                  {pluginStateLabel(plugin)}
                </span>
              </div>
              <h2>{plugin.name}</h2>
              <p>{plugin.description}</p>
              <span className="text-link">
                View setup <span aria-hidden="true">→</span>
              </span>
            </Link>
          ))}
        </section>
      )}
      <aside className="trust-note">
        <span aria-hidden="true">◇</span>
        <div>
          <strong>Plugins deliver evidence, not blanket authority.</strong>
          <p>
            Loom verifies the tenant, installation, resource, version, and
            authorization before a waiting session can resume.
          </p>
        </div>
      </aside>
    </>
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
          ["Continuations", goal.metrics.wake_ups],
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
      {goal.run.workflow_version_id && (
        <section className="panel workflow-progress">
          {(() => {
            const workflowSteps = goal.workflow_steps ?? [];
            const activeSteps = workflowSteps.filter(
              (step) => step.state !== "pending",
            );
            const completedSteps = activeSteps.filter(
              (step) => step.state === "succeeded",
            );
            return (
              <>
                <div className="section-heading">
                  <div>
                    <p className="step-kicker">LOOM WORKFLOW</p>
                    <h2>Current workflow relationship</h2>
                  </div>
                  <span className="muted">
                    {activeSteps.length === 0
                      ? "Ready to start"
                      : `${completedSteps.length}/${activeSteps.length} active steps complete`}
                  </span>
                </div>
                <div
                  className="relationship-map"
                  aria-label="Workflow progress"
                >
                  {(goal.workflow_runs ?? []).map((run) => {
                    const steps = (goal.workflow_steps ?? []).filter(
                      (step) => step.run_id === run.id,
                    );
                    const names = new Map(
                      (run.spec?.steps ?? []).map((step) => [
                        step.key,
                        step.name,
                      ]),
                    );
                    const edges = run.spec?.edges ?? [];
                    return (
                      <div className="workflow-run-progress" key={run.id}>
                        <div className="workflow-run-label">
                          <strong>
                            {run.parent_run_id ? "Child run" : "Root run"}
                          </strong>
                          <span className="muted">
                            {run.current_step_key?.replaceAll("_", " ") ??
                              "starting"}{" "}
                            · {run.state}
                          </span>
                        </div>
                        {steps.map((step, index) => (
                          <div className="relationship-item" key={step.id}>
                            <div
                              className={`relationship-node node-${step.step_type} progress-${step.state}`}
                            >
                              <span>{index + 1}</span>
                              <div>
                                <small>{step.state}</small>
                                <strong>
                                  {step.step_key.replaceAll("_", " ")}
                                </strong>
                              </div>
                            </div>
                            {edges.filter((edge) => edge.from === step.step_key)
                              .length > 0 && (
                              <div className="relationship-links">
                                {edges
                                  .filter((edge) => edge.from === step.step_key)
                                  .map((edge) => (
                                    <span
                                      key={`${run.id}-${edge.from}-${edge.outcome}`}
                                    >
                                      {edge.outcome} →{" "}
                                      {names.get(edge.to) ?? edge.to}
                                    </span>
                                  ))}
                              </div>
                            )}
                          </div>
                        ))}
                      </div>
                    );
                  })}
                </div>
                <p className="muted">
                  Loom owns this relationship map. Scheduler references remain
                  an implementation detail.
                </p>
              </>
            );
          })()}
        </section>
      )}
      <div className="detail-grid">
        <section className="panel">
          <div className="section-heading">
            <h2>
              {goal.state === "WAITING"
                ? "What evidence continues this workflow?"
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
  const [currentUser, setCurrentUser] = useState<AuthUser | null>(null);
  // Stable callback prevents request effects from restarting on every render.
  const [logout] = useState(() => () => {
    void createClient()
      .authLogout()
      .catch(() => undefined);
    setClient(null);
    setCurrentUser(null);
  });
  if (!client)
    return (
      <Login
        onLogin={(user) => {
          setCurrentUser(user);
          setClient(createClient());
        }}
      />
    );
  const canAdmin = currentUser?.role === "admin";
  return (
    <div className="shell">
      <RouteEffects />
      <a className="skip" href="#content">
        Skip to content
      </a>
      <aside className="sidebar">
        <Link className="brand" to="/">
          <span className="mark">L</span> loom
        </Link>
        <p className="eyebrow">WORKSPACE</p>
        <nav aria-label="Main navigation">
          <NavLink to="/" end>
            <span aria-hidden="true">⌂</span> Home
          </NavLink>
          <NavLink to="/goals">
            <span aria-hidden="true">◎</span> Goals
          </NavLink>
          <NavLink to="/workflows">
            <span aria-hidden="true">⌘</span> Workflows
          </NavLink>
          <NavLink to="/needs-you">
            <span aria-hidden="true">◈</span> Needs you
          </NavLink>
          <NavLink to="/plugins">
            <span aria-hidden="true">◆</span> Plugins
          </NavLink>
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
          <small>
            Operator console · build{" "}
            {(import.meta.env.VITE_BUILD_SHA ?? "development").slice(0, 12)}
          </small>
        </div>
      </aside>
      <main id="content" className="content" tabIndex={-1}>
        <Routes>
          <Route
            path="/"
            element={
              <Home
                client={client}
                onUnauthorized={logout}
                canAdmin={canAdmin}
              />
            }
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
            path="/workflows"
            element={
              <WorkflowList
                client={client}
                onUnauthorized={logout}
                canAdmin={canAdmin}
              />
            }
          />
          <Route
            path="/workflows/:id"
            element={
              <WorkflowEditor
                client={client}
                onUnauthorized={logout}
                canAdmin={canAdmin}
              />
            }
          />
          <Route
            path="/plugins"
            element={<PluginStore client={client} onUnauthorized={logout} />}
          />
          <Route
            path="/plugins/:id"
            element={
              <PluginSetup
                client={client}
                onUnauthorized={logout}
                canAdmin={canAdmin}
              />
            }
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

import { useEffect, useState, type FormEvent } from "react";
import { Link, useParams } from "react-router-dom";
import {
  APIError,
  createClient,
  type IntegrationInstance,
  type ManualGitHubSetup,
  type Plugin,
} from "@piglor/loom-client";

type Client = ReturnType<typeof createClient>;

function message(error: unknown) {
  return error instanceof Error ? error.message : "Unable to complete setup.";
}

function GitHubMark() {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" className="plugin-glyph">
      <path
        fill="currentColor"
        d="M12 2a10 10 0 0 0-3.16 19.49c.5.09.68-.22.68-.48v-1.87c-2.78.6-3.37-1.18-3.37-1.18-.45-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.61.07-.61 1 .07 1.53 1.03 1.53 1.03.9 1.53 2.35 1.09 2.92.83.09-.65.35-1.09.64-1.34-2.22-.25-4.55-1.11-4.55-4.94 0-1.09.39-1.98 1.03-2.68-.1-.25-.45-1.27.1-2.64 0 0 .84-.27 2.75 1.02A9.6 9.6 0 0 1 12 6.82a9.6 9.6 0 0 1 2.5.34c1.91-1.3 2.75-1.02 2.75-1.02.55 1.37.2 2.39.1 2.64.64.7 1.03 1.59 1.03 2.68 0 3.84-2.34 4.68-4.57 4.93.36.31.68.92.68 1.85v2.77c0 .27.18.58.69.48A10 10 0 0 0 12 2Z"
      />
    </svg>
  );
}

function loadPlugin(
  client: Client,
  id: string,
  signal: AbortSignal,
): Promise<Plugin | undefined> {
  return client
    .plugins(signal)
    .then((plugins) => plugins.find((plugin) => plugin.id === id));
}

export function PluginSetup({
  client,
  onUnauthorized,
}: {
  client: Client;
  onUnauthorized: () => void;
}) {
  const { id = "" } = useParams();
  const [plugin, setPlugin] = useState<Plugin>();
  const [connections, setConnections] = useState<IntegrationInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [accountType, setAccountType] = useState<"organization" | "personal">(
    "organization",
  );
  const [account, setAccount] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const [revision, setRevision] = useState(0);

  useEffect(() => {
    const abort = new AbortController();
    setLoading(true);
    Promise.all([
      loadPlugin(client, id, abort.signal),
      client.integrationInstances(id, abort.signal),
    ])
      .then(([nextPlugin, nextConnections]) => {
        setPlugin(nextPlugin);
        setConnections(nextConnections);
        setError("");
      })
      .catch((reason) => {
        if (!abort.signal.aborted) {
          if (reason instanceof APIError && reason.status === 401)
            onUnauthorized();
          else setError(message(reason));
        }
      })
      .finally(() => {
        if (!abort.signal.aborted) setLoading(false);
      });
    return () => abort.abort();
  }, [client, id, onUnauthorized, revision]);

  useEffect(() => {
    const complete = (event: MessageEvent) => {
      if (
        event.origin === window.location.origin &&
        event.data?.type === "loom-plugin-setup"
      ) {
        setBusy(false);
        if (event.data.success) setRevision((value) => value + 1);
        else
          setError("GitHub setup needs attention. Try the connection again.");
      }
    };
    window.addEventListener("message", complete);
    return () => window.removeEventListener("message", complete);
  }, []);

  useEffect(() => {
    document.title = `${plugin?.name ?? "Plugin"} · Loom`;
  }, [plugin?.name]);

  async function connectGuided() {
    setBusy(true);
    setError("");
    try {
      const setup = await client.startGitHubSetup({
        account_type: accountType,
        account: accountType === "organization" ? account.trim() : "",
      });
      const target = `loom-github-${setup.setup_id}`;
      const popup = window.open("", target, "popup,width=760,height=760");
      if (!popup) throw new Error("Allow pop-ups to continue with GitHub.");
      const form = document.createElement("form");
      form.method = "post";
      form.action = setup.action_url;
      form.target = target;
      for (const [name, value] of Object.entries({
        manifest: setup.manifest,
        state: setup.state,
      })) {
        const field = document.createElement("input");
        field.type = "hidden";
        field.name = name;
        field.value = value;
        form.append(field);
      }
      document.body.append(form);
      form.submit();
      form.remove();
    } catch (reason) {
      setBusy(false);
      setError(message(reason));
    }
  }

  async function connectManual(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    const form = event.currentTarget;
    const values = Object.fromEntries(new FormData(form));
    try {
      await client.connectExistingGitHubApp(
        values as unknown as ManualGitHubSetup,
      );
      form.reset();
      setAdvanced(false);
      setRevision((value) => value + 1);
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }

  async function disable(connection: IntegrationInstance) {
    if (
      !window.confirm(`Disable the connection to ${connection.account_label}?`)
    )
      return;
    setBusy(true);
    setError("");
    try {
      await client.disableIntegration(connection.id);
      setRevision((value) => value + 1);
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }

  if (loading) return <p role="status">Loading plugin…</p>;
  if (!plugin)
    return (
      <section className="panel empty">
        <h1>Plugin not found</h1>
        <Link to="/plugins">Back to plugin store</Link>
      </section>
    );

  const active = connections.filter((item) => item.state === "active");
  const storageReady = plugin.secret_backend === "ready";
  return (
    <>
      <Link className="back" to="/plugins">
        ← Plugin store
      </Link>
      <header className="plugin-hero">
        <div className="plugin-logo large">
          <GitHubMark />
        </div>
        <div>
          <p className="eyebrow">{plugin.category}</p>
          <h1>Connect {plugin.name} to Loom</h1>
          <p>{plugin.setup_summary}</p>
        </div>
      </header>

      {active.length > 0 && (
        <section className="connection-section" aria-labelledby="connections">
          <div className="section-heading generous">
            <div>
              <p className="eyebrow">CONNECTED</p>
              <h2 id="connections">GitHub accounts</h2>
            </div>
            <span className="progress-pill">{active.length} active</span>
          </div>
          <div className="connection-list">
            {active.map((connection) => (
              <article className="connection-card" key={connection.id}>
                <span className="check yes">✓</span>
                <div>
                  <strong>{connection.account_label}</strong>
                  <p>
                    {connection.repository_selection === "all"
                      ? "All repositories"
                      : "Selected repositories"}
                    {connection.last_verified_at
                      ? ` · Verified ${new Date(connection.last_verified_at).toLocaleString()}`
                      : ""}
                  </p>
                </div>
                <button
                  className="secondary danger-button"
                  disabled={busy}
                  onClick={() => disable(connection)}
                >
                  Disable
                </button>
              </article>
            ))}
          </div>
        </section>
      )}

      <div className="plugin-detail-grid">
        <section className="panel setup-panel">
          <div className="section-heading">
            <h2>
              {active.length ? "Add another connection" : "Connect GitHub"}
            </h2>
            <span className="time-pill">{plugin.estimated_time}</span>
          </div>
          {!storageReady ? (
            <div className="configuration-block">
              <strong>OpenBao needs attention first</strong>
              <p>
                Loom cannot accept plugin credentials until its secret store is
                ready. Existing Goals are unaffected.
              </p>
              {plugin.checks
                .filter((check) => check.status !== "ready")
                .map((check) => (
                  <code key={check.id}>{check.detail}</code>
                ))}
            </div>
          ) : (
            <>
              <div className="guided-form">
                <p className="eyebrow">RECOMMENDED</p>
                <h3>Create and connect a GitHub App</h3>
                <p>
                  Loom fills in the webhook and read-only permissions. GitHub
                  returns generated credentials directly to your Loom server.
                </p>
                <fieldset>
                  <legend>Where should the app live?</legend>
                  <label className="choice-row">
                    <input
                      type="radio"
                      checked={accountType === "organization"}
                      onChange={() => setAccountType("organization")}
                    />
                    GitHub organization
                  </label>
                  <label className="choice-row">
                    <input
                      type="radio"
                      checked={accountType === "personal"}
                      onChange={() => setAccountType("personal")}
                    />
                    Personal account
                  </label>
                </fieldset>
                {accountType === "organization" && (
                  <label>
                    Organization name
                    <input
                      value={account}
                      onChange={(event) => setAccount(event.target.value)}
                      placeholder="your-company"
                      autoComplete="off"
                    />
                  </label>
                )}
                <button
                  className="plugin-action"
                  disabled={
                    busy || (accountType === "organization" && !account.trim())
                  }
                  onClick={connectGuided}
                >
                  {busy ? "Waiting for GitHub…" : "Connect with GitHub →"}
                </button>
                <small>
                  Credentials are never stored in this browser or PostgreSQL.
                </small>
              </div>
              <button
                className="secondary advanced-toggle"
                aria-expanded={advanced}
                onClick={() => setAdvanced((value) => !value)}
              >
                {advanced
                  ? "Hide advanced setup"
                  : "Use an existing GitHub App"}
              </button>
              {advanced && (
                <form className="manual-credentials" onSubmit={connectManual}>
                  <h3>Existing GitHub App</h3>
                  <p>
                    All secret fields are write-only and cannot be revealed
                    later.
                  </p>
                  <label>
                    Connection label
                    <input name="label" defaultValue="GitHub App" required />
                  </label>
                  <div className="field-pair">
                    <label>
                      App ID
                      <input name="app_id" inputMode="numeric" required />
                    </label>
                    <label>
                      Installation ID
                      <input
                        name="installation_id"
                        inputMode="numeric"
                        required
                      />
                    </label>
                  </div>
                  <div className="field-pair">
                    <label>
                      Client ID
                      <input name="client_id" autoComplete="off" required />
                    </label>
                    <label>
                      App slug
                      <input name="app_slug" autoComplete="off" required />
                    </label>
                  </div>
                  <label>
                    Client secret
                    <input
                      name="client_secret"
                      type="password"
                      autoComplete="new-password"
                      required
                    />
                  </label>
                  <label>
                    Webhook secret
                    <input
                      name="webhook_secret"
                      type="password"
                      minLength={32}
                      autoComplete="new-password"
                      required
                    />
                  </label>
                  <label>
                    Private key (PEM)
                    <textarea name="private_key" rows={8} required />
                  </label>
                  <button disabled={busy}>
                    {busy ? "Verifying…" : "Verify and save connection"}
                  </button>
                </form>
              )}
            </>
          )}
          {error && (
            <p className="error" role="alert">
              {error}
            </p>
          )}
        </section>

        <aside className="panel readiness-panel">
          <p className="eyebrow">READINESS</p>
          <h2>
            {active.length
              ? "Connected"
              : storageReady
                ? "Ready to connect"
                : "Setup required"}
          </h2>
          {plugin.checks.map((check) => (
            <div className="check-row" key={check.id}>
              <span
                className={check.status === "ready" ? "check yes" : "check"}
              >
                {check.status === "ready" ? "✓" : "!"}
              </span>
              <div>
                <strong>{check.label}</strong>
                <small>{check.detail}</small>
              </div>
            </div>
          ))}
          <div className="endpoint">
            <label htmlFor="webhook-url">Webhook URL</label>
            <input
              id="webhook-url"
              readOnly
              value={`${window.location.origin}/v1/github/webhook`}
            />
          </div>
          <p className="fine-print">{plugin.notice}</p>
        </aside>
      </div>
    </>
  );
}

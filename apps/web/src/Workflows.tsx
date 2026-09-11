import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  APIError,
  createClient,
  type Condition,
  type IntegrationInstance,
  type WorkflowDefinition,
  type WorkflowSpec,
  type WorkflowStep,
  type WorkflowStepType,
} from "@piglor/loom-client";

type Client = ReturnType<typeof createClient>;

function message(error: unknown) {
  return error instanceof Error ? error.message : "Unable to save workflow.";
}

export function WorkflowList({
  client,
  onUnauthorized,
}: {
  client: Client;
  onUnauthorized: () => void;
}) {
  const [workflows, setWorkflows] = useState<WorkflowDefinition[]>([]);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();
  useEffect(() => {
    document.title = "Workflows · Loom";
    const abort = new AbortController();
    client
      .workflows(abort.signal)
      .then(setWorkflows)
      .catch((reason) => {
        if (reason instanceof APIError && reason.status === 401)
          onUnauthorized();
        else if (!abort.signal.aborted) setError(message(reason));
      });
    return () => abort.abort();
  }, [client, onUnauthorized]);
  return (
    <>
      <header className="page-heading workflow-heading">
        <div>
          <p className="eyebrow">WORKFLOW LIBRARY</p>
          <h1>Decide how work moves</h1>
          <p className="page-intro">
            Plugins deliver verified events. Your workflow decides what starts,
            what waits, and exactly when an agent is needed.
          </p>
        </div>
      </header>
      <section className="panel workflow-create">
        <div>
          <p className="eyebrow">NEW WORKFLOW</p>
          <h2>Start with a simple guided workflow</h2>
          <p className="muted">
            Loom creates an editable Agent → Complete relationship. Add event
            triggers and wait steps after opening it.
          </p>
        </div>
        <form
          onSubmit={async (event) => {
            event.preventDefault();
            setBusy(true);
            setError("");
            try {
              const workflow = await client.createWorkflow({
                name,
                description,
              });
              navigate(`/workflows/${workflow.id}`);
            } catch (reason) {
              setError(message(reason));
            } finally {
              setBusy(false);
            }
          }}
        >
          <label>
            Workflow name
            <input
              required
              maxLength={120}
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Review a completed pull request"
            />
          </label>
          <label>
            Goal created by this workflow
            <input
              maxLength={1000}
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              placeholder="Review the change and report any actionable problems"
            />
          </label>
          <button disabled={busy}>
            {busy ? "Creating…" : "Create workflow"}
          </button>
        </form>
      </section>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <section className="workflow-library" aria-label="Saved workflows">
        {workflows.length === 0 ? (
          <div className="panel empty">
            <h2>No workflows yet</h2>
            <p>
              Create one above. Connecting a plugin alone never starts an agent.
            </p>
          </div>
        ) : (
          workflows.map((workflow) => (
            <Link
              className="workflow-card"
              to={`/workflows/${workflow.id}`}
              key={workflow.id}
            >
              <div>
                <span className={`workflow-state ${workflow.state}`}>
                  {workflow.state}
                </span>
                <h2>{workflow.name}</h2>
                <p>{workflow.description || "No goal description yet"}</p>
              </div>
              <dl>
                <dt>Steps</dt>
                <dd>{workflow.draft_spec.steps.length}</dd>
                <dt>Published</dt>
                <dd>
                  {workflow.latest_version
                    ? `v${workflow.latest_version}`
                    : "Not yet"}
                </dd>
              </dl>
            </Link>
          ))
        )}
      </section>
    </>
  );
}

function nextKey(type: WorkflowStepType, steps: WorkflowStep[]) {
  let number = 1;
  while (steps.some((step) => step.key === `${type}_${number}`)) number++;
  return `${type}_${number}`;
}

function linearSpec(spec: WorkflowSpec, steps: WorkflowStep[]): WorkflowSpec {
  return {
    ...spec,
    steps,
    edges: steps.slice(0, -1).map((step, index) => ({
      from: step.key,
      to: steps[index + 1].key,
      outcome: "success" as const,
    })),
  };
}

function waitCondition(step: WorkflowStep): Partial<Condition> {
  return (step.config.condition ?? {}) as Partial<Condition>;
}

function configureWait(
  spec: WorkflowSpec,
  index: number,
  change: {
    integration_instance_id?: string;
    condition?: Partial<Condition>;
  },
) {
  const steps = [...spec.steps];
  const step = steps[index];
  steps[index] = {
    ...step,
    config: {
      ...step.config,
      ...(change.integration_instance_id !== undefined
        ? { integration_instance_id: change.integration_instance_id }
        : {}),
      condition: { ...waitCondition(step), ...change.condition },
    },
  };
  return linearSpec(spec, steps);
}

function configureAgent(
  spec: WorkflowSpec,
  change: { runtime?: string; worker_id?: string },
) {
  return {
    ...spec,
    steps: spec.steps.map((step) =>
      step.type === "agent"
        ? { ...step, config: { ...step.config, ...change } }
        : step,
    ),
  };
}

function RelationshipMap({ steps }: { steps: WorkflowStep[] }) {
  return (
    <div className="relationship-map" aria-label="Workflow relationships">
      {steps.map((step, index) => (
        <div className="relationship-item" key={step.key}>
          <div className={`relationship-node node-${step.type}`}>
            <span>{index + 1}</span>
            <div>
              <small>{step.type.replaceAll("_", " ")}</small>
              <strong>{step.name}</strong>
            </div>
          </div>
          {index < steps.length - 1 && <i aria-hidden="true">→</i>}
        </div>
      ))}
    </div>
  );
}

export function WorkflowEditor({
  client,
  onUnauthorized,
}: {
  client: Client;
  onUnauthorized: () => void;
}) {
  const { id = "" } = useParams();
  const [workflow, setWorkflow] = useState<WorkflowDefinition | null>(null);
  const [connections, setConnections] = useState<IntegrationInstance[]>([]);
  const [validation, setValidation] = useState<string[]>([]);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();
  useEffect(() => {
    const abort = new AbortController();
    Promise.all([
      client.workflow(id, abort.signal),
      client.integrationInstances("", abort.signal),
    ])
      .then(([definition, instances]) => {
        setWorkflow(definition);
        setConnections(
          instances.filter((instance) => instance.state === "active"),
        );
      })
      .catch((reason) => {
        if (reason instanceof APIError && reason.status === 401)
          onUnauthorized();
        else if (!abort.signal.aborted) setError(message(reason));
      });
    return () => abort.abort();
  }, [client, id, onUnauthorized]);
  useEffect(() => {
    document.title = `${workflow?.name ?? "Workflow"} · Loom`;
  }, [workflow]);
  const trigger = workflow?.draft_spec.triggers[0];
  const selectedConnection = useMemo(
    () =>
      connections.find(
        (connection) => connection.id === trigger?.integration_instance_id,
      ),
    [connections, trigger?.integration_instance_id],
  );
  if (!workflow) return <p role="status">Loading workflow…</p>;
  const updateSpec = (spec: WorkflowSpec) =>
    setWorkflow({ ...workflow, draft_spec: spec });
  const save = async () => {
    const saved = await client.updateWorkflow(workflow.id, {
      name: workflow.name,
      description: workflow.description,
      spec: workflow.draft_spec,
    });
    setWorkflow(saved);
    return saved;
  };
  const execute = async (action: "save" | "validate" | "publish" | "run") => {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const saved = await save();
      if (action === "validate") {
        const result = await client.validateWorkflow(
          saved.id,
          saved.draft_spec,
        );
        setValidation(result.errors);
        setNotice(
          result.valid
            ? "Workflow is valid and ready to publish."
            : "Fix the items below.",
        );
      } else if (action === "publish") {
        const result = await client.publishWorkflow(saved.id);
        setWorkflow({
          ...saved,
          state: "published",
          latest_version: result.version,
        });
        setValidation([]);
        setNotice(
          `Published version ${result.version}. Existing runs stay on their original version.`,
        );
      } else if (action === "run") {
        const result = await client.startWorkflow(saved.id);
        navigate(`/goals/${result.goal_id}`);
      } else {
        setNotice("Draft saved.");
      }
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Link className="back" to="/workflows">
        ← Workflow library
      </Link>
      <header className="page-heading workflow-heading">
        <div>
          <p className="eyebrow">WORKFLOW · {workflow.id.slice(0, 8)}</p>
          <h1>{workflow.name}</h1>
          <p className="page-intro">
            Loom owns this specification. Hatchet executes a compiled version
            underneath.
          </p>
        </div>
        <span className={`workflow-state ${workflow.state}`}>
          {workflow.state}
          {workflow.latest_version ? ` · v${workflow.latest_version}` : ""}
        </span>
      </header>
      <div className="workflow-editor-grid">
        <div>
          <section className="panel builder-section">
            <p className="step-kicker">1 · BASICS</p>
            <label>
              Workflow name
              <input
                value={workflow.name}
                maxLength={120}
                onChange={(event) =>
                  setWorkflow({ ...workflow, name: event.target.value })
                }
              />
            </label>
            <label>
              Goal description
              <textarea
                value={workflow.description}
                maxLength={1000}
                onChange={(event) =>
                  setWorkflow({ ...workflow, description: event.target.value })
                }
              />
            </label>
          </section>
          <section className="panel builder-section">
            <p className="step-kicker">2 · TRIGGER</p>
            <h2>What may start this workflow?</h2>
            <p className="muted">
              The event starts the workflow. It never starts an agent directly.
            </p>
            <div className="choice-cards">
              <button
                type="button"
                className={
                  trigger?.type === "manual"
                    ? "choice-card selected"
                    : "choice-card"
                }
                onClick={() =>
                  updateSpec({
                    ...workflow.draft_spec,
                    triggers: [{ type: "manual" }],
                  })
                }
              >
                Manual start<small>Run from Loom when you choose.</small>
              </button>
              <button
                type="button"
                className={
                  trigger?.type === "integration_event"
                    ? "choice-card selected"
                    : "choice-card"
                }
                disabled={connections.length === 0}
                onClick={() => {
                  const connection = connections[0];
                  if (connection)
                    updateSpec({
                      ...workflow.draft_spec,
                      triggers: [
                        {
                          type: "integration_event",
                          integration_instance_id: connection.id,
                          source: connection.plugin_id,
                          event_type: "workflow.completed",
                          resource: "*",
                          version: "*",
                        },
                      ],
                    });
                }}
              >
                Plugin event
                <small>
                  {connections.length
                    ? "Use a verified connection."
                    : "Connect a plugin first."}
                </small>
              </button>
            </div>
            {trigger?.type === "integration_event" && (
              <div className="trigger-fields">
                <label>
                  Connection
                  <select
                    value={trigger.integration_instance_id}
                    onChange={(event) => {
                      const connection = connections.find(
                        (item) => item.id === event.target.value,
                      );
                      if (connection)
                        updateSpec({
                          ...workflow.draft_spec,
                          triggers: [
                            {
                              ...trigger,
                              integration_instance_id: connection.id,
                              source: connection.plugin_id,
                            },
                          ],
                        });
                    }}
                  >
                    {connections.map((connection) => (
                      <option value={connection.id} key={connection.id}>
                        {connection.account_label} · {connection.plugin_id}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  Verified event
                  <input
                    value={trigger.event_type ?? ""}
                    onChange={(event) =>
                      updateSpec({
                        ...workflow.draft_spec,
                        triggers: [
                          { ...trigger, event_type: event.target.value },
                        ],
                      })
                    }
                    placeholder="workflow.completed"
                  />
                </label>
                <label>
                  Resource selector
                  <input
                    value={trigger.resource ?? "*"}
                    onChange={(event) =>
                      updateSpec({
                        ...workflow.draft_spec,
                        triggers: [
                          { ...trigger, resource: event.target.value },
                        ],
                      })
                    }
                  />
                </label>
                <label>
                  Version selector
                  <input
                    value={trigger.version ?? "*"}
                    onChange={(event) =>
                      updateSpec({
                        ...workflow.draft_spec,
                        triggers: [{ ...trigger, version: event.target.value }],
                      })
                    }
                  />
                </label>
                <p className="muted">
                  Events are accepted only from{" "}
                  {selectedConnection?.account_label ??
                    "the selected connection"}
                  .
                </p>
              </div>
            )}
          </section>
          <section className="panel builder-section">
            <p className="step-kicker">3 · STEPS</p>
            <div className="section-heading">
              <div>
                <h2>What happens next?</h2>
                <p className="muted">
                  Each arrow is an explicit success relationship.
                </p>
              </div>
            </div>
            <ol className="step-editor">
              {workflow.draft_spec.steps.map((step, index) => (
                <li key={step.key}>
                  <span className="step-handle">{index + 1}</span>
                  <div>
                    <label>
                      Step name
                      <input
                        value={step.name}
                        onChange={(event) => {
                          const steps = [...workflow.draft_spec.steps];
                          steps[index] = { ...step, name: event.target.value };
                          updateSpec(linearSpec(workflow.draft_spec, steps));
                        }}
                      />
                    </label>
                    <label>
                      Type
                      <select
                        value={step.type}
                        onChange={(event) => {
                          const type = event.target.value as WorkflowStepType;
                          const config =
                            type === "agent"
                              ? { runtime: "demo" }
                              : type === "wait_event"
                                ? {
                                    ...(connections[0]
                                      ? {
                                          integration_instance_id:
                                            connections[0].id,
                                        }
                                      : {}),
                                    condition: {
                                      source:
                                        connections[0]?.plugin_id ?? "external",
                                      type: "workflow.completed",
                                      resource: "resource-id",
                                      version: "1",
                                    },
                                  }
                                : {};
                          const steps = [...workflow.draft_spec.steps];
                          steps[index] = { ...step, type, config };
                          updateSpec(linearSpec(workflow.draft_spec, steps));
                        }}
                      >
                        <option value="agent">Agent work</option>
                        <option value="wait_event">Wait for event</option>
                        <option value="complete">Complete Goal</option>
                      </select>
                    </label>
                    {step.type === "agent" && (
                      <div className="trigger-fields">
                        <label>
                          Runtime
                          <select
                            value={(step.config.runtime as string) ?? "demo"}
                            onChange={(event) =>
                              updateSpec(
                                configureAgent(workflow.draft_spec, {
                                  runtime: event.target.value,
                                  worker_id:
                                    event.target.value === "demo"
                                      ? ""
                                      : (step.config.worker_id as string),
                                }),
                              )
                            }
                          >
                            <option value="demo">Demo (no model)</option>
                            <option value="remote-demo">Remote Agent</option>
                            <option value="codex-container">
                              Contained Codex Agent
                            </option>
                          </select>
                        </label>
                        {step.config.runtime !== "demo" && (
                          <label>
                            Worker ID
                            <input
                              value={(step.config.worker_id as string) ?? ""}
                              onChange={(event) =>
                                updateSpec(
                                  configureAgent(workflow.draft_spec, {
                                    worker_id: event.target.value,
                                  }),
                                )
                              }
                              placeholder="Enrolled worker UUID"
                            />
                          </label>
                        )}
                        <p className="muted">
                          All agent steps in this run reuse this session and
                          worker binding.
                        </p>
                      </div>
                    )}
                    {step.type === "wait_event" && (
                      <div className="trigger-fields">
                        <label>
                          Evidence connection
                          <select
                            value={
                              (step.config.integration_instance_id as string) ??
                              ""
                            }
                            onChange={(event) => {
                              const connection = connections.find(
                                (item) => item.id === event.target.value,
                              );
                              updateSpec(
                                configureWait(workflow.draft_spec, index, {
                                  integration_instance_id: event.target.value,
                                  condition: connection
                                    ? { source: connection.plugin_id }
                                    : undefined,
                                }),
                              );
                            }}
                          >
                            <option value="">Operator API (no plugin)</option>
                            {connections.map((connection) => (
                              <option value={connection.id} key={connection.id}>
                                {connection.account_label} ·{" "}
                                {connection.plugin_id}
                              </option>
                            ))}
                          </select>
                        </label>
                        <label>
                          Event type
                          <input
                            value={waitCondition(step).type ?? ""}
                            onChange={(event) =>
                              updateSpec(
                                configureWait(workflow.draft_spec, index, {
                                  condition: { type: event.target.value },
                                }),
                              )
                            }
                          />
                        </label>
                        <label>
                          Exact resource
                          <input
                            value={waitCondition(step).resource ?? ""}
                            onChange={(event) =>
                              updateSpec(
                                configureWait(workflow.draft_spec, index, {
                                  condition: { resource: event.target.value },
                                }),
                              )
                            }
                          />
                        </label>
                        <label>
                          Exact version
                          <input
                            value={waitCondition(step).version ?? ""}
                            onChange={(event) =>
                              updateSpec(
                                configureWait(workflow.draft_spec, index, {
                                  condition: { version: event.target.value },
                                }),
                              )
                            }
                          />
                        </label>
                        <p className="muted">
                          Only this exact verified event may continue the
                          workflow. The plugin cannot start an agent itself.
                        </p>
                      </div>
                    )}
                  </div>
                  <button
                    type="button"
                    className="secondary compact"
                    disabled={workflow.draft_spec.steps.length <= 2}
                    onClick={() =>
                      updateSpec(
                        linearSpec(
                          workflow.draft_spec,
                          workflow.draft_spec.steps.filter(
                            (_, stepIndex) => stepIndex !== index,
                          ),
                        ),
                      )
                    }
                  >
                    Remove
                  </button>
                </li>
              ))}
            </ol>
            <div className="add-step-row">
              {(["agent", "wait_event", "complete"] as WorkflowStepType[]).map(
                (type) => (
                  <button
                    type="button"
                    className="secondary"
                    key={type}
                    onClick={() => {
                      const key = nextKey(type, workflow.draft_spec.steps);
                      const config =
                        type === "agent"
                          ? { runtime: "demo" }
                          : type === "wait_event"
                            ? {
                                ...(connections[0]
                                  ? {
                                      integration_instance_id:
                                        connections[0].id,
                                    }
                                  : {}),
                                condition: {
                                  source:
                                    connections[0]?.plugin_id ?? "external",
                                  type: "workflow.completed",
                                  resource: "resource-id",
                                  version: "1",
                                },
                              }
                            : {};
                      updateSpec(
                        linearSpec(workflow.draft_spec, [
                          ...workflow.draft_spec.steps,
                          {
                            key,
                            name: type.replaceAll("_", " "),
                            type,
                            config,
                          },
                        ]),
                      );
                    }}
                  >
                    + {type.replaceAll("_", " ")}
                  </button>
                ),
              )}
            </div>
          </section>
          {(error || notice || validation.length > 0) && (
            <section className="panel builder-feedback" aria-live="polite">
              {error && <p className="error">{error}</p>}
              {notice && <p>{notice}</p>}
              {validation.length > 0 && (
                <ul>
                  {validation.map((item) => (
                    <li key={item}>{item}</li>
                  ))}
                </ul>
              )}
            </section>
          )}
          <div className="builder-actions">
            <button
              className="secondary"
              disabled={busy}
              onClick={() => void execute("save")}
            >
              Save draft
            </button>
            <button
              className="secondary"
              disabled={busy}
              onClick={() => void execute("validate")}
            >
              Check workflow
            </button>
            <button disabled={busy} onClick={() => void execute("publish")}>
              Publish new version
            </button>
            {workflow.latest_version > 0 && trigger?.type === "manual" && (
              <button disabled={busy} onClick={() => void execute("run")}>
                Start workflow
              </button>
            )}
          </div>
        </div>
        <aside className="panel relationship-panel">
          <p className="step-kicker">RELATIONSHIP MAP</p>
          <h2>How Loom sees this workflow</h2>
          <p className="muted">
            This map comes from Loom’s portable specification—not Hatchet’s
            interface.
          </p>
          <RelationshipMap steps={workflow.draft_spec.steps} />
        </aside>
      </div>
    </>
  );
}

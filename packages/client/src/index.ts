// Shared client contracts: no React, DOM, storage, or provider dependencies.
export type GoalState =
  | "READY"
  | "RUNNING"
  | "WAITING"
  | "BLOCKED"
  | "COMPLETED"
  | "FAILED"
  | "CANCELLED";
export interface GoalSummary {
  id: string;
  title: string;
  state: GoalState;
  created_at: string;
  updated_at: string;
}
export interface Plugin {
  id: string;
  name: string;
  category: string;
  description: string;
  state:
    | "ready_to_connect"
    | "needs_configuration"
    | "connected"
    | "needs_attention";
  setup_title: string;
  setup_summary: string;
  estimated_time: string;
  steps: { title: string; description: string }[];
  checks: {
    id: string;
    label: string;
    status: "ready" | "missing";
    detail: string;
    required: boolean;
  }[];
  action?: { label: string; url: string };
  endpoints?: { label: string; path: string }[];
  notice: string;
  connection_count: number;
  secret_backend:
    | "ready"
    | "unconfigured"
    | "needs_credentials"
    | "initializing"
    | "authentication_failed"
    | "sealed"
    | "unavailable";
}
export interface AuthUser {
  id: string;
  organization: string;
  email: string;
  display_name: string;
  role: "admin" | "member";
}
export interface AuthConfig {
  email_registration_enabled: boolean;
  bootstrap_email: string;
  social_providers: { id: string; name: string }[];
}
export interface IntegrationInstance {
  id: string;
  credential_id: string;
  plugin_id: string;
  external_instance_id: string;
  account_id: string;
  account_label: string;
  repository_selection: "all" | "selected";
  metadata: { permissions?: Record<string, string> };
  state: "active" | "needs_attention" | "disabled";
  last_verified_at: string | null;
}
export type WorkflowStepType =
  "agent" | "wait_event" | "condition" | "subflow" | "complete";
export interface WorkflowStepConfig {
  runtime?: "demo" | "remote-demo" | "codex-container";
  worker_id?: string;
  integration_instance_id?: string;
  condition?: Condition;
  path?: string;
  equals?: unknown;
  not_equals?: unknown;
  exists?: boolean;
  workflow_definition_id?: string;
  workflow_version_id?: string;
}
export interface WorkflowTrigger {
  type: "manual" | "integration_event";
  integration_instance_id?: string;
  source?: string;
  event_type?: string;
  resource?: string;
  version?: string;
}
export interface WorkflowStep {
  key: string;
  name: string;
  type: WorkflowStepType;
  config: WorkflowStepConfig;
}
export interface WorkflowEdge {
  from: string;
  to: string;
  outcome: "success" | "failure";
}
export interface WorkflowSpec {
  schema_version: 1;
  triggers: WorkflowTrigger[];
  steps: WorkflowStep[];
  edges: WorkflowEdge[];
}
export interface WorkflowDefinition {
  id: string;
  name: string;
  description: string;
  state: "draft" | "published" | "archived";
  draft_spec: WorkflowSpec;
  latest_version: number;
  created_at: string;
  updated_at: string;
}
export interface WorkflowValidation {
  valid: boolean;
  errors: string[];
}
export interface PublishedWorkflow {
  definition_id: string;
  version_id: string;
  version: number;
  digest: string;
  spec: WorkflowSpec;
}
export interface GitHubSetupStart {
  setup_id: string;
  state: string;
  action_url: string;
  manifest: string;
  expires_at: string;
}
export interface ManualGitHubSetup {
  label: string;
  app_id: string;
  client_id: string;
  client_secret: string;
  private_key: string;
  webhook_secret: string;
  app_slug: string;
  installation_id: string;
}
export interface Condition {
  source: string;
  type: string;
  resource: string;
  version: string;
}
export interface Goal extends GoalSummary {
  objective: string;
  organization: string;
  completion_criteria: unknown;
  waiting_reason: string | null;
  session: {
    id: string;
    worker_id: string;
    runtime: string;
    provider_session_id: string | null;
  };
  run: {
    id: string;
    policy: unknown;
    workflow_definition_id?: string | null;
    workflow_version_id?: string | null;
    current_step_key?: string | null;
    state?: string;
  };
  workflow_runs?: {
    id: string;
    parent_run_id: string | null;
    invoking_step_key: string | null;
    state: string;
    current_step_key: string | null;
    spec?: WorkflowSpec;
  }[];
  workflow_steps?: {
    id: string;
    run_id: string;
    step_key: string;
    step_type: WorkflowStepType;
    position: number;
    state: string;
  }[];
  wait: {
    id: string;
    generation: number;
    condition: Condition;
    armed_at: string | null;
    satisfied_at: string | null;
    event_id: string | null;
  };
  attempts: {
    id: string;
    worker_id: string;
    state: string;
    outcome: string | null;
    started_at: string;
    stopped_at: string | null;
    duration_ms: number | null;
  }[];
  audit: {
    sequence: number;
    action: string;
    recorded_at: string;
    details: unknown;
  }[];
  metrics: {
    lifetime_seconds: number | string;
    suspended_seconds: number | string;
    execution_seconds: number | string;
    wake_ups: number;
    attempts: number;
    tokens: number | null;
    provider_cost: number | null;
  };
}
export class APIError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
export function createClient(
  token = "",
  baseURL = "",
  transport: typeof fetch = fetch,
) {
  function csrfToken() {
    if (typeof document === "undefined") return "";
    return (
      document.cookie
        .split(";")
        .map((part) => part.trim())
        .find((part) => part.startsWith("loom_csrf="))
        ?.slice("loom_csrf=".length) ?? ""
    );
  }
  async function request<T>(
    path: string,
    signal?: AbortSignal,
    options: { method?: "GET" | "POST" | "PATCH"; body?: unknown } = {},
  ): Promise<T> {
    const method = options.method ?? "GET";
    const csrf = csrfToken();
    const response = await transport(`${baseURL}${path}`, {
      method,
      headers: {
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        ...(method === "GET" || !csrf ? {} : { "X-Loom-CSRF": csrf }),
        ...(options.body === undefined
          ? {}
          : { "Content-Type": "application/json" }),
      },
      body:
        options.body === undefined ? undefined : JSON.stringify(options.body),
      signal,
      cache: "no-store",
      credentials: "same-origin",
      redirect: "error",
    });
    if (!response.ok) {
      // Do not reflect server payloads (which may contain sensitive diagnostics).
      throw new APIError(
        response.status,
        response.status === 401
          ? path === "/v1/auth/login"
            ? "Invalid email or password."
            : "Your Loom session expired. Sign in again."
          : response.status === 404
            ? "This Goal was not found."
            : response.status === 422
              ? "Check the setup values and try again."
              : "The control plane is unavailable. Please retry.",
      );
    }
    if (response.status === 204) return undefined as T;
    return response.json() as Promise<T>;
  }
  return {
    authConfig: (signal?: AbortSignal) =>
      request<AuthConfig>("/v1/auth/config", signal),
    authSession: (signal?: AbortSignal) =>
      request<{ user: AuthUser }>("/v1/auth/session", signal),
    authLogin: (
      input: { email: string; password: string },
      signal?: AbortSignal,
    ) =>
      request<{ user: AuthUser }>("/v1/auth/login", signal, {
        method: "POST",
        body: input,
      }),
    authRegister: (
      input: { email: string; password: string },
      signal?: AbortSignal,
    ) =>
      request<{ user: AuthUser }>("/v1/auth/register", signal, {
        method: "POST",
        body: input,
      }),
    authLogout: (signal?: AbortSignal) =>
      request<void>("/v1/auth/logout", signal, {
        method: "POST",
        body: {},
      }),
    goals: (signal?: AbortSignal) =>
      request<GoalSummary[]>("/v1/goals", signal),
    goal: (id: string, signal?: AbortSignal) =>
      request<Goal>(`/v1/goals/${encodeURIComponent(id)}`, signal),
    plugins: (signal?: AbortSignal) => request<Plugin[]>("/v1/plugins", signal),
    integrationInstances: (plugin = "", signal?: AbortSignal) =>
      request<IntegrationInstance[]>(
        `/v1/integration-instances${plugin ? `?plugin=${encodeURIComponent(plugin)}` : ""}`,
        signal,
      ),
    workflows: (signal?: AbortSignal) =>
      request<WorkflowDefinition[]>("/v1/workflows", signal),
    workflow: (id: string, signal?: AbortSignal) =>
      request<WorkflowDefinition>(
        `/v1/workflows/${encodeURIComponent(id)}`,
        signal,
      ),
    workflowVersion: (id: string, signal?: AbortSignal) =>
      request<PublishedWorkflow>(
        `/v1/workflow-versions/${encodeURIComponent(id)}`,
        signal,
      ),
    createWorkflow: (
      input: { name: string; description: string; spec?: WorkflowSpec },
      signal?: AbortSignal,
    ) =>
      request<WorkflowDefinition>("/v1/workflows", signal, {
        method: "POST",
        body: input,
      }),
    updateWorkflow: (
      id: string,
      input: { name: string; description: string; spec: WorkflowSpec },
      signal?: AbortSignal,
    ) =>
      request<WorkflowDefinition>(
        `/v1/workflows/${encodeURIComponent(id)}`,
        signal,
        { method: "PATCH", body: input },
      ),
    validateWorkflow: (id: string, spec: WorkflowSpec, signal?: AbortSignal) =>
      request<WorkflowValidation>(
        `/v1/workflows/${encodeURIComponent(id)}/validate`,
        signal,
        { method: "POST", body: spec },
      ),
    publishWorkflow: (id: string, signal?: AbortSignal) =>
      request<PublishedWorkflow>(
        `/v1/workflows/${encodeURIComponent(id)}/publish`,
        signal,
        { method: "POST" },
      ),
    startWorkflow: (id: string, signal?: AbortSignal) =>
      request<{ goal_id: string }>(
        `/v1/workflows/${encodeURIComponent(id)}/runs`,
        signal,
        { method: "POST" },
      ),
    startGitHubSetup: (
      input: { account_type: "organization" | "personal"; account: string },
      signal?: AbortSignal,
    ) =>
      request<GitHubSetupStart>("/v1/plugins/github/setup-sessions", signal, {
        method: "POST",
        body: input,
      }),
    connectExistingGitHubApp: (
      input: ManualGitHubSetup,
      signal?: AbortSignal,
    ) =>
      request<IntegrationInstance>("/v1/plugins/github/manual", signal, {
        method: "POST",
        body: input,
      }),
    disableIntegration: (id: string, signal?: AbortSignal) =>
      request<void>(
        `/v1/integration-instances/${encodeURIComponent(id)}/disable`,
        signal,
        { method: "POST" },
      ),
  };
}

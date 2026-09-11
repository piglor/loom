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
    "ready" | "unconfigured" | "needs_credentials" | "sealed" | "unavailable";
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
  run: { id: string; policy: unknown };
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
  token: string,
  baseURL = "",
  transport: typeof fetch = fetch,
) {
  async function request<T>(
    path: string,
    signal?: AbortSignal,
    options: { method?: "GET" | "POST"; body?: unknown } = {},
  ): Promise<T> {
    const response = await transport(`${baseURL}${path}`, {
      method: options.method ?? "GET",
      headers: {
        Authorization: `Bearer ${token}`,
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
          ? "Your operator token was rejected. Sign in again."
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

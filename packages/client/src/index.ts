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
  async function request<T>(path: string, signal?: AbortSignal): Promise<T> {
    const response = await transport(`${baseURL}${path}`, {
      headers: { Authorization: `Bearer ${token}` },
      signal,
      cache: "no-store",
      credentials: "omit",
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
            : "The control plane is unavailable. Please retry.",
      );
    }
    return response.json() as Promise<T>;
  }
  return {
    goals: (signal?: AbortSignal) =>
      request<GoalSummary[]>("/v1/goals", signal),
    goal: (id: string, signal?: AbortSignal) =>
      request<Goal>(`/v1/goals/${encodeURIComponent(id)}`, signal),
  };
}

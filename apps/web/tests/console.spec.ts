import { expect, test as base, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

const test = base.extend<{ checkPageErrors: void }>({
  checkPageErrors: [
    async ({ page }, use) => {
      const errors: Error[] = [];
      page.on("pageerror", (error) => errors.push(error));
      await use();
      expect(errors, "No uncaught browser JavaScript errors").toEqual([]);
    },
    { auto: true },
  ],
});

const id = "a2222222-2222-4222-8222-222222222222";
const token = "test-operator-token-never-persist-this";
const summary = {
  id,
  title: "Investigate deployment health",
  state: "WAITING",
  created_at: "2026-09-09T01:00:00Z",
  updated_at: "2026-09-09T02:00:00Z",
};
const goal = {
  ...summary,
  objective: "Resume only after deployment completes.",
  organization: "test",
  completion_criteria: { approved: true },
  waiting_reason: "external_event",
  session: {
    id: "logical-session",
    worker_id: "private-worker",
    runtime: "remote-demo",
    provider_session_id: null,
  },
  run: { id: "run-1", policy: {} },
  wait: {
    id: "wait-1",
    generation: 2,
    condition: {
      source: "deployment",
      type: "rollout.completed",
      resource: "deploy-123",
      version: "B",
    },
    event_id: null,
  },
  metrics: {
    lifetime_seconds: "7200",
    execution_seconds: 60,
    suspended_seconds: 7140,
    wake_ups: 1,
    attempts: 1,
    tokens: null,
    provider_cost: null,
  },
  attempts: [
    {
      id: "attempt-1",
      state: "STOPPED",
      outcome: "yielded",
      duration_ms: 60000,
    },
  ],
  audit: [
    {
      sequence: 1,
      action: "agent_yielded",
      recorded_at: "2026-09-09T02:00:00Z",
      details: { source: "deployment" },
    },
  ],
};
async function mockAPI(page: Page) {
  await page.route("**/v1/goals**", async (route) => {
    expect(route.request().headers().authorization).toBe(`Bearer ${token}`);
    await route.fulfill({
      json: route.request().url().endsWith("/v1/goals") ? [summary] : goal,
    });
  });
}
async function login(page: Page, path = "/") {
  await page.goto(path);
  await page.getByLabel("Operator API token").fill(token);
  await page.getByRole("button", { name: "Connect to Loom" }).click();
}
test("unknown browser route retains HTTP 404 and offers recovery", async ({
  page,
}) => {
  await mockAPI(page);
  const response = await page.goto("/not-a-loom-page");
  expect(response?.status()).toBe(404);
  await page.getByLabel("Operator API token").fill(token);
  await page.getByRole("button", { name: "Connect to Loom" }).click();
  await expect(
    page.getByRole("heading", { name: "Page not found" }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Go to Goals", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Your Goals" })).toBeVisible();
});
test("keyboard sign-in and skip navigation focus main content", async ({
  page,
}) => {
  await mockAPI(page);
  await page.goto("/");
  await page.keyboard.press("Tab");
  await expect(page.getByLabel("Operator API token")).toBeFocused();
  await page.keyboard.type(token);
  await page.keyboard.press("Tab");
  await expect(
    page.getByRole("button", { name: "Connect to Loom" }),
  ).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "Your Goals" })).toBeVisible();
  await page.keyboard.press("Tab");
  await expect(
    page.getByRole("link", { name: "Skip to content" }),
  ).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("#content")).toBeFocused();
});
test("back and forward restore Goals and attention navigation", async ({
  page,
}) => {
  await mockAPI(page);
  await login(page, "/goals");
  await page.getByRole("link", { name: "◈ Needs you" }).click();
  await expect(
    page.getByRole("heading", { name: "Needs you", exact: true }),
  ).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("heading", { name: "Your Goals" })).toBeVisible();
  await page.goForward();
  await expect(
    page.getByRole("heading", { name: "Needs you", exact: true }),
  ).toBeVisible();
});
test("state filter and search reset restore matching Goals", async ({
  page,
}) => {
  await mockAPI(page);
  await login(page);
  await page.getByLabel("Find a Goal").fill("unmatched");
  await expect(
    page.getByRole("heading", { name: "No matching Goals" }),
  ).toBeVisible();
  await page.getByLabel("Find a Goal").fill("");
  await page.getByRole("combobox").selectOption("COMPLETED");
  await expect(
    page.getByRole("heading", { name: "No matching Goals" }),
  ).toBeVisible();
  await page.getByRole("combobox").selectOption("ALL");
  await expect(
    page.getByRole("link", { name: /Investigate deployment/ }),
  ).toBeVisible();
});
test("completion policy expands without horizontal overflow", async ({
  page,
}) => {
  await mockAPI(page);
  await login(page, `/goals/${id}`);
  await page.getByText("Completion criteria & policy", { exact: true }).click();
  await expect(page.locator("details[open] pre")).toContainText(
    '"approved": true',
  );
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
});
test("network failure hides stale Goals and refresh recovers", async ({
  page,
}) => {
  await mockAPI(page);
  await login(page);
  await expect(
    page.getByRole("link", { name: /Investigate deployment/ }),
  ).toBeVisible();
  await page.route("**/v1/goals", (route) => route.abort());
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(
    page.getByRole("link", { name: /Investigate deployment/ }),
  ).toHaveCount(0);
  await page.unroute("**/v1/goals");
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(
    page.getByRole("link", { name: /Investigate deployment/ }),
  ).toBeVisible();
});
test("expired authority signs out on refresh", async ({ page }) => {
  await mockAPI(page);
  await login(page);
  await expect(
    page.getByRole("link", { name: /Investigate deployment/ }),
  ).toBeVisible();
  await page.route("**/v1/goals", (route) => route.fulfill({ status: 401 }));
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByLabel("Operator API token")).toHaveValue("");
  await expect(
    page.getByRole("link", { name: /Investigate deployment/ }),
  ).toHaveCount(0);
});
test("unknown Goal supports retry and navigation back", async ({ page }) => {
  await mockAPI(page);
  const missing = "00000000-0000-4000-8000-000000000000";
  await page.route(`**/v1/goals/${missing}`, (route) =>
    route.fulfill({ status: 404 }),
  );
  await login(page, `/goals/${missing}`);
  await expect(
    page.getByRole("heading", { name: "Unable to open Goal" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Unable to open Goal" }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Back to Goals" }).click();
  await expect(page.getByRole("heading", { name: "Your Goals" })).toBeVisible();
});
test("malformed Goal URL produces a recoverable error without a crash", async ({
  page,
}) => {
  const errors: Error[] = [];
  page.on("pageerror", (error) => errors.push(error));
  await mockAPI(page);
  await page.route("**/v1/goals/not-a-uuid", (route) =>
    route.fulfill({ status: 422 }),
  );
  await login(page, "/goals/not-a-uuid");
  await expect(
    page.getByRole("heading", { name: "Unable to open Goal" }),
  ).toBeVisible();
  expect(errors).toEqual([]);
});
test("sign out and browser back cannot restore authority", async ({ page }) => {
  await mockAPI(page);
  await login(page, "/goals");
  await page.getByRole("link", { name: /Investigate deployment/ }).click();
  await expect(
    page.getByRole("heading", { name: "Audit timeline" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.goBack();
  await expect(page.getByLabel("Operator API token")).toHaveValue("");
  await expect(page.getByRole("heading", { name: "Your Goals" })).toHaveCount(
    0,
  );
});
test("navigate real-shaped generic Goal and inspect wait evidence", async ({
  page,
}) => {
  await mockAPI(page);
  await login(page);
  await expect(page.getByRole("heading", { name: "Your Goals" })).toBeVisible();
  await page.getByRole("link", { name: /Investigate deployment/ }).click();
  await expect(
    page.getByRole("heading", { name: "What wakes this Goal?" }),
  ).toBeVisible();
  await expect(
    page.getByText("rollout.completed", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("private-worker", { exact: true })).toBeVisible();
  await expect(
    page.getByText(/Finite demo runtime—not model inference/),
  ).toBeVisible();
  await expect(
    page.getByText("No running or uncertain execution attempt is recorded."),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
});
test("deep links survive sign-in and logout clears authority", async ({
  page,
}) => {
  await mockAPI(page);
  await login(page, `/goals/${id}`);
  await expect(
    page.getByRole("heading", { name: "Audit timeline" }),
  ).toBeVisible();
  expect(
    await page.evaluate(() => ({
      local: localStorage.length,
      session: sessionStorage.length,
      cookies: document.cookie,
    })),
  ).toEqual({ local: 0, session: 0, cookies: "" });
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByLabel("Operator API token")).toHaveValue("");
  await expect(
    page.getByRole("heading", { name: "Audit timeline" }),
  ).toHaveCount(0);
});
test("reject invalid credentials without displaying backend diagnostics", async ({
  page,
}) => {
  await page.route("**/v1/goals", (route) =>
    route.fulfill({ status: 401, json: { detail: "secret internal error" } }),
  );
  await login(page);
  await expect(page.getByRole("alert")).toContainText("rejected");
  await expect(page.getByText("secret internal error")).toHaveCount(0);
});
test("filter Goals and display an empty attention queue", async ({ page }) => {
  await mockAPI(page);
  await login(page);
  await expect(page.getByRole("heading", { name: "Your Goals" })).toBeVisible();
  await page.getByLabel("Find a Goal").fill("unmatched");
  await expect(
    page.getByRole("link", { name: /Investigate deployment/ }),
  ).toHaveCount(0);
  await page.getByRole("link", { name: "◈ Needs you" }).click();
  await expect(
    page.getByRole("heading", { name: "Nothing here needs attention" }),
  ).toBeVisible();
});
test("external HTML is rendered as text", async ({ page }) => {
  await page.route("**/v1/goals", (route) =>
    route.fulfill({
      json: [
        { ...summary, title: '<img src=x onerror="window.injected=true">' },
      ],
    }),
  );
  await login(page);
  await expect(
    page.getByRole("heading", {
      name: '<img src=x onerror="window.injected=true">',
    }),
  ).toBeVisible();
  expect(
    await page.evaluate(() => Reflect.get(window, "injected")),
  ).toBeUndefined();
  await expect(page.locator("img")).toHaveCount(0);
});
test("failed refresh does not present stale data as current", async ({
  page,
}) => {
  await mockAPI(page);
  await login(page);
  await expect(page.getByRole("heading", { name: "Your Goals" })).toBeVisible();
  await page.route("**/v1/goals", (route) => route.fulfill({ status: 503 }));
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("alert")).toContainText("unavailable");
  await expect(
    page.getByRole("link", { name: /Investigate deployment/ }),
  ).toHaveCount(0);
});
test("refreshing browser removes in-memory credentials", async ({ page }) => {
  await mockAPI(page);
  await login(page);
  await expect(page.getByRole("heading", { name: "Your Goals" })).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("Operator API token")).toBeVisible();
});

test("accessible login, overview and detail with responsive screenshots", async ({
  page,
}, info) => {
  // Three full accessibility scans need a different budget from one interaction.
  test.setTimeout(90_000);
  await mockAPI(page);
  await page.goto("/");
  expect(
    (
      await new AxeBuilder({ page })
        .withTags(["wcag2a", "wcag2aa", "wcag21aa"])
        .analyze()
    ).violations,
  ).toEqual([]);
  await login(page);
  await expect(page.getByRole("heading", { name: "Your Goals" })).toBeVisible();
  expect(
    (
      await new AxeBuilder({ page })
        .withTags(["wcag2a", "wcag2aa", "wcag21aa"])
        .analyze()
    ).violations,
  ).toEqual([]);
  await page.screenshot({ path: info.outputPath("goals.png"), fullPage: true });
  await page.getByRole("link", { name: /Investigate deployment/ }).click();
  await expect(
    page.getByRole("heading", { name: "Audit timeline" }),
  ).toBeVisible();
  expect(
    (
      await new AxeBuilder({ page })
        .withTags(["wcag2a", "wcag2aa", "wcag21aa"])
        .analyze()
    ).violations,
  ).toEqual([]);
  await page.screenshot({
    path: info.outputPath("goal-detail.png"),
    fullPage: true,
  });
});

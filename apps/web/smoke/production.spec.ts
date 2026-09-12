import { expect, test } from "@playwright/test";

test("deployed console loads its browser assets and login", async ({
  page,
}) => {
  const errors: Error[] = [];
  page.on("pageerror", (error) => errors.push(error));
  const response = await page.goto("/");
  expect(response?.status()).toBe(200);
  expect(response?.headers()["content-security-policy"]).toContain(
    "frame-ancestors 'none'",
  );
  await expect(page.getByLabel("Email address")).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeEnabled();
  expect(errors).toEqual([]);
});

test("health is public but readiness and Goal data require authentication", async ({
  request,
}) => {
  expect((await request.get("/healthz")).status()).toBe(200);
  expect((await request.get("/readyz")).status()).toBe(401);
  expect((await request.get("/v1/goals")).status()).toBe(401);
});

test("unknown page retains 404 while offering console sign-in", async ({
  page,
}) => {
  const response = await page.goto("/production-smoke-missing-page");
  expect(response?.status()).toBe(404);
  await expect(page.getByLabel("Email address")).toBeVisible();
});

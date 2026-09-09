import { defineConfig, devices } from "@playwright/test";
export default defineConfig({
  testDir: "./tests",
  fullyParallel: true,
  // Bound cross-engine scans on small self-hosted runners.
  workers: 1,
  retries: 0,
  use: { baseURL: "http://127.0.0.1:4173", trace: "retain-on-failure" },
  webServer: {
    command: "../../.loom/bin/loom-server",
    url: "http://127.0.0.1:4173",
    reuseExistingServer: false,
    env: {
      LOOM_API_TOKEN: "browser-fixture-token-not-a-real-credential",
      LOOM_DATABASE_URL: "postgresql://unused@127.0.0.1:1/unused",
      LOOM_UPSTREAM_URL: "http://127.0.0.1:1",
      LOOM_LISTEN_ADDR: "127.0.0.1:4173",
      LOOM_WEB_DIR: "dist",
    },
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    { name: "firefox", use: { ...devices["Desktop Firefox"] } },
    { name: "webkit", use: { ...devices["Desktop Safari"] } },
    { name: "mobile", use: { ...devices["iPhone 13"] } },
  ],
});

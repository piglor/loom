import { defineConfig, devices } from "@playwright/test";

// Read-only public deployment smoke checks. No operator token, traces or
// failure injection against production; the browser CI suite owns those cases.
export default defineConfig({
  testDir: "./smoke",
  workers: 1,
  retries: 0,
  use: { baseURL: "https://loom.piglor.com", trace: "off" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});

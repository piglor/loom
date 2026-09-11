package github

import (
	"net/url"
	"strings"

	"github.com/piglor/loom/services/loom/internal/integrations/catalog"
)

func Descriptor(installURL, webhookSecret, apiToken string) catalog.Plugin {
	plugin := catalog.Plugin{
		ID:            "github",
		Name:          "GitHub",
		Category:      "Source control",
		Description:   "Connect repositories as a verified event source for Loom workflows.",
		State:         catalog.NeedsConfiguration,
		SetupTitle:    "Set up GitHub",
		SetupSummary:  "Let published workflows receive verified pull request and Actions events at the right version.",
		EstimatedTime: "About 2 minutes",
		Steps: []catalog.Step{
			{Title: "Install the Loom GitHub App", Description: "GitHub opens in a new tab. Choose the organization you want to connect."},
			{Title: "Choose repository access", Description: "Select only the repositories Loom should observe. Read-only access is enough."},
			{Title: "Return to Loom", Description: "Create Goals that wait on verified GitHub events."},
		},
		Endpoints: []catalog.Endpoint{{Label: "Webhook URL", Path: "/v1/github/webhook"}},
		Notice:    "Installation enables event observation only. Privileged agent execution remains separately authorized.",
	}
	parsed, parseErr := url.Parse(installURL)
	installReady := installURL != "" && parseErr == nil && parsed.Scheme == "https" && strings.EqualFold(parsed.Hostname(), "github.com") && parsed.Port() == "" && parsed.User == nil && strings.HasPrefix(parsed.EscapedPath(), "/apps/")
	installDetail := "LOOM_GITHUB_APP_INSTALL_URL needed"
	if installURL != "" && !installReady {
		installDetail = "Install URL must be absolute HTTPS without embedded credentials"
	}
	if installReady {
		installDetail = "Configured"
	}
	webhookReady := len(webhookSecret) >= 32
	plugin.Checks = []catalog.Check{
		{ID: "installation_url", Label: "GitHub App installation", Status: checkStatus(installReady), Detail: installDetail, Required: true},
		{ID: "signed_webhooks", Label: "Signed webhooks", Status: checkStatus(webhookReady), Detail: readyDetail(webhookReady, "LOOM_GITHUB_WEBHOOK_SECRET needed"), Required: true},
		{ID: "private_repositories", Label: "Private repositories", Status: checkStatus(apiToken != ""), Detail: readyDetail(apiToken != "", "Optional; public repositories remain supported"), Required: false},
	}
	if installReady && webhookReady {
		plugin.State = catalog.ReadyToConnect
		plugin.Action = &catalog.Action{Label: "Install GitHub app", URL: parsed.String()}
	}
	return plugin
}

func checkStatus(ready bool) catalog.CheckStatus {
	if ready {
		return catalog.CheckReady
	}
	return catalog.CheckMissing
}

func readyDetail(ready bool, missing string) string {
	if ready {
		return "Configured"
	}
	return missing
}

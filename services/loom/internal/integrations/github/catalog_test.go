package github

import (
	"strings"
	"testing"
)

func TestPluginDescriptor(t *testing.T) {
	configured := Descriptor("https://github.com/apps/loom/installations/new", strings.Repeat("s", 32), "api-token")
	if configured.State != "ready_to_connect" || configured.Action == nil || len(configured.Checks) != 3 {
		t.Fatalf("unexpected configured descriptor: %#v", configured)
	}
	publicOnly := Descriptor("https://github.com/apps/loom/installations/new", strings.Repeat("s", 32), "")
	if publicOnly.State != "ready_to_connect" || publicOnly.Checks[2].Status != "missing" || publicOnly.Checks[2].Required {
		t.Fatalf("unexpected public-only descriptor: %#v", publicOnly)
	}
	for _, unsafe := range []string{"javascript:alert(1)", "http://github.com/apps/loom", "https://user@example.com/install", "https://example.com/apps/loom", "https://github.com.evil.test/apps/loom", "https://github.com/not-an-app"} {
		plugin := Descriptor(unsafe, strings.Repeat("s", 32), "token")
		if plugin.State != "needs_configuration" || plugin.Action != nil || plugin.Checks[0].Status != "missing" {
			t.Errorf("unsafe install URL was not isolated: %q %#v", unsafe, plugin)
		}
	}
}

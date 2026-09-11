package control

import (
	"context"
	"testing"
)

func TestIntegrationInstanceUpsertReturnsPersistedID(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	credentials := []IntegrationCredentialRecord{
		{ID: "a1111111-1111-4111-8111-111111111111", PluginID: "github", Label: "GitHub App", SecretReference: "organizations/test/plugins/github/credentials/one", SecretVersion: 1, State: "active"},
		{ID: "a2222222-2222-4222-8222-222222222222", PluginID: "github", Label: "GitHub App", SecretReference: "organizations/test/plugins/github/credentials/two", SecretVersion: 1, State: "active"},
	}
	for _, credential := range credentials {
		if err := store.CreateIntegrationCredential(ctx, credential); err != nil {
			t.Fatal(err)
		}
	}
	firstID, err := store.UpsertIntegrationInstance(ctx, IntegrationInstanceRecord{CredentialID: credentials[0].ID, PluginID: "github", ExternalInstanceID: "42", AccountID: "7", AccountLabel: "first", RepositorySelection: "selected", Metadata: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := store.UpsertIntegrationInstance(ctx, IntegrationInstanceRecord{CredentialID: credentials[1].ID, PluginID: "github", ExternalInstanceID: "42", AccountID: "7", AccountLabel: "updated", RepositorySelection: "all", Metadata: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if secondID != firstID {
		t.Fatalf("upsert returned transient ID %q; want persisted ID %q", secondID, firstID)
	}
	instances, err := store.ListIntegrationInstances(ctx, "github")
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].CredentialID != credentials[1].ID || instances[0].AccountLabel != "updated" {
		t.Fatalf("unexpected persisted instance: %#v", instances)
	}
}

package control

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/piglor/loom/services/loom/ent"
	"github.com/piglor/loom/services/loom/ent/integrationcredential"
	"github.com/piglor/loom/services/loom/ent/integrationinstance"
	"github.com/piglor/loom/services/loom/ent/integrationsetupsession"
)

type IntegrationCredentialRecord struct {
	ID              string
	PluginID        string
	Label           string
	SecretReference string
	SecretVersion   int
	State           string
}

type IntegrationInstanceRecord struct {
	ID                  string         `json:"id"`
	CredentialID        string         `json:"credential_id"`
	PluginID            string         `json:"plugin_id"`
	ExternalInstanceID  string         `json:"external_instance_id"`
	RoutingIdentity     string         `json:"-"`
	AccountID           string         `json:"account_id"`
	AccountLabel        string         `json:"account_label"`
	RepositorySelection string         `json:"repository_selection"`
	Metadata            map[string]any `json:"metadata"`
	State               string         `json:"state"`
	LastVerifiedAt      *time.Time     `json:"last_verified_at"`
}

type IntegrationSetupRecord struct {
	ID                    string
	PluginID              string
	Mode                  string
	Stage                 string
	CredentialID          *string
	PendingInstallationID *string
	ExpiresAt             time.Time
}

func validPluginID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func (s *Store) CreateIntegrationSetup(ctx context.Context, pluginID, mode, stateHash string, expiresAt time.Time) (string, error) {
	if !validPluginID(pluginID) || (mode != "manifest" && mode != "manual") || len(stateHash) != 64 || !expiresAt.After(time.Now()) {
		return "", invalid("Invalid integration setup")
	}
	id := ID()
	err := s.tx(ctx, func(t *transaction) error {
		return t.client.IntegrationSetupSession.Create().SetID(id).SetOrganization(s.Organization).SetPluginID(pluginID).SetMode(mode).SetStateHash(stateHash).SetStage("created").SetExpiresAt(expiresAt).SetCreatedAt(t.now).Exec(ctx)
	})
	return id, err
}

func (s *Store) IntegrationSetupByState(ctx context.Context, stateHash string) (IntegrationSetupRecord, error) {
	var result IntegrationSetupRecord
	err := s.tx(ctx, func(t *transaction) error {
		row, err := t.client.IntegrationSetupSession.Query().Where(integrationsetupsession.OrganizationEQ(s.Organization), integrationsetupsession.StateHashEQ(stateHash), integrationsetupsession.ConsumedAtIsNil(), integrationsetupsession.ExpiresAtGT(t.now)).Only(ctx)
		if ent.IsNotFound(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		result = IntegrationSetupRecord{ID: row.ID, PluginID: row.PluginID, Mode: row.Mode, Stage: row.Stage, CredentialID: row.CredentialID, PendingInstallationID: row.PendingInstallationID, ExpiresAt: row.ExpiresAt}
		return nil
	})
	return result, err
}

func (s *Store) SetIntegrationSetupStage(ctx context.Context, setupID, stage string, credentialID, installationID *string) error {
	allowed := stage == "app_created" || stage == "installation_pending" || stage == "complete" || stage == "failed"
	if !ValidID(setupID) || !allowed {
		return invalid("Invalid integration setup")
	}
	return s.tx(ctx, func(t *transaction) error {
		update := t.client.IntegrationSetupSession.Update().Where(integrationsetupsession.IDEQ(strings.ToLower(setupID)), integrationsetupsession.OrganizationEQ(s.Organization), integrationsetupsession.ConsumedAtIsNil()).SetStage(stage)
		if credentialID != nil {
			update.SetCredentialID(*credentialID)
		}
		if installationID != nil {
			update.SetPendingInstallationID(*installationID)
		}
		if stage == "complete" || stage == "failed" {
			update.SetConsumedAt(t.now)
		}
		count, err := update.Save(ctx)
		if err != nil {
			return err
		}
		if count != 1 {
			return notFound()
		}
		return nil
	})
}

func (s *Store) CreateIntegrationCredential(ctx context.Context, record IntegrationCredentialRecord) error {
	if !ValidID(record.ID) || !validPluginID(record.PluginID) || strings.TrimSpace(record.Label) == "" || len(record.Label) > 100 || record.SecretReference == "" || record.SecretVersion < 1 {
		return invalid("Invalid integration credential metadata")
	}
	return s.tx(ctx, func(t *transaction) error {
		return t.client.IntegrationCredential.Create().SetID(strings.ToLower(record.ID)).SetOrganization(s.Organization).SetPluginID(record.PluginID).SetLabel(strings.TrimSpace(record.Label)).SetSecretReference(record.SecretReference).SetSecretVersion(record.SecretVersion).SetState(record.State).SetCreatedAt(t.now).SetUpdatedAt(t.now).Exec(ctx)
	})
}

func (s *Store) IntegrationCredential(ctx context.Context, id string) (IntegrationCredentialRecord, error) {
	var result IntegrationCredentialRecord
	if !ValidID(id) {
		return result, invalid("Invalid integration credential ID")
	}
	err := s.tx(ctx, func(t *transaction) error {
		row, err := t.client.IntegrationCredential.Query().Where(integrationcredential.IDEQ(strings.ToLower(id)), integrationcredential.OrganizationEQ(s.Organization)).Only(ctx)
		if ent.IsNotFound(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		result = IntegrationCredentialRecord{ID: row.ID, PluginID: row.PluginID, Label: row.Label, SecretReference: row.SecretReference, SecretVersion: row.SecretVersion, State: row.State}
		return nil
	})
	return result, err
}

func (s *Store) SetIntegrationCredentialState(ctx context.Context, id, state string, version *int) error {
	if !ValidID(id) || (state != "setup_incomplete" && state != "active" && state != "needs_attention" && state != "disabled") {
		return invalid("Invalid integration credential update")
	}
	return s.tx(ctx, func(t *transaction) error {
		update := t.client.IntegrationCredential.Update().Where(integrationcredential.IDEQ(strings.ToLower(id)), integrationcredential.OrganizationEQ(s.Organization)).SetState(state).SetUpdatedAt(t.now)
		if version != nil {
			update.SetSecretVersion(*version)
		}
		count, err := update.Save(ctx)
		if err != nil {
			return err
		}
		if count != 1 {
			return notFound()
		}
		return nil
	})
}

func (s *Store) UpsertIntegrationInstance(ctx context.Context, record IntegrationInstanceRecord) (string, error) {
	if !ValidID(record.CredentialID) || !validPluginID(record.PluginID) || record.ExternalInstanceID == "" || record.AccountID == "" || record.AccountLabel == "" {
		return "", invalid("Invalid integration instance")
	}
	if record.RepositorySelection != "all" && record.RepositorySelection != "selected" {
		return "", invalid("Invalid repository selection")
	}
	if record.RoutingIdentity == "" {
		record.RoutingIdentity = record.PluginID + ":" + record.ExternalInstanceID
	}
	if !textBetween(record.RoutingIdentity, 256) {
		return "", invalid("Invalid integration routing identity")
	}
	if record.ID == "" {
		record.ID = ID()
	}
	var persistedID string
	err := s.tx(ctx, func(t *transaction) error {
		credentialExists, err := t.client.IntegrationCredential.Query().Where(integrationcredential.IDEQ(record.CredentialID), integrationcredential.OrganizationEQ(s.Organization), integrationcredential.PluginIDEQ(record.PluginID)).Exist(ctx)
		if err != nil {
			return err
		}
		if !credentialExists {
			return notFound()
		}
		if err := t.client.IntegrationInstance.Create().SetID(record.ID).SetOrganization(s.Organization).SetCredentialID(record.CredentialID).SetPluginID(record.PluginID).SetExternalInstanceID(record.ExternalInstanceID).SetRoutingIdentity(record.RoutingIdentity).SetAccountID(record.AccountID).SetAccountLabel(record.AccountLabel).SetRepositorySelection(record.RepositorySelection).SetMetadata(record.Metadata).SetState("active").SetLastVerifiedAt(t.now).SetCreatedAt(t.now).SetUpdatedAt(t.now).
			OnConflictColumns("organization", "plugin_id", "external_instance_id").Update(func(u *ent.IntegrationInstanceUpsert) {
			u.SetCredentialID(record.CredentialID).SetRoutingIdentity(record.RoutingIdentity).SetAccountID(record.AccountID).SetAccountLabel(record.AccountLabel).SetRepositorySelection(record.RepositorySelection).SetMetadata(record.Metadata).SetState("active").SetLastVerifiedAt(t.now).SetUpdatedAt(t.now)
		}).Exec(ctx); err != nil {
			return err
		}
		persisted, err := t.client.IntegrationInstance.Query().Where(integrationinstance.OrganizationEQ(s.Organization), integrationinstance.PluginIDEQ(record.PluginID), integrationinstance.ExternalInstanceIDEQ(record.ExternalInstanceID)).Only(ctx)
		if err != nil {
			return err
		}
		persistedID = persisted.ID
		return nil
	})
	return persistedID, err
}

func (s *Store) ListIntegrationInstances(ctx context.Context, pluginID string) ([]IntegrationInstanceRecord, error) {
	if pluginID != "" && !validPluginID(pluginID) {
		return nil, invalid("Invalid plugin ID")
	}
	result := []IntegrationInstanceRecord{}
	err := s.tx(ctx, func(t *transaction) error {
		query := t.client.IntegrationInstance.Query().Where(integrationinstance.OrganizationEQ(s.Organization))
		if pluginID != "" {
			query.Where(integrationinstance.PluginIDEQ(pluginID))
		}
		rows, err := query.Order(ent.Asc(integrationinstance.FieldAccountLabel)).All(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			result = append(result, IntegrationInstanceRecord{ID: row.ID, CredentialID: row.CredentialID, PluginID: row.PluginID, ExternalInstanceID: row.ExternalInstanceID, RoutingIdentity: row.RoutingIdentity, AccountID: row.AccountID, AccountLabel: row.AccountLabel, RepositorySelection: row.RepositorySelection, Metadata: row.Metadata, State: row.State, LastVerifiedAt: row.LastVerifiedAt})
		}
		return nil
	})
	return result, err
}

func (s *Store) IntegrationCredentialForInstance(ctx context.Context, pluginID, externalInstanceID string) (IntegrationCredentialRecord, error) {
	var result IntegrationCredentialRecord
	if !validPluginID(pluginID) || externalInstanceID == "" {
		return result, invalid("Invalid integration instance")
	}
	err := s.tx(ctx, func(t *transaction) error {
		instanceRow, err := t.client.IntegrationInstance.Query().Where(integrationinstance.OrganizationEQ(s.Organization), integrationinstance.PluginIDEQ(pluginID), integrationinstance.ExternalInstanceIDEQ(externalInstanceID), integrationinstance.StateEQ("active")).Only(ctx)
		if ent.IsNotFound(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		row, err := t.client.IntegrationCredential.Query().Where(integrationcredential.IDEQ(instanceRow.CredentialID), integrationcredential.OrganizationEQ(s.Organization), integrationcredential.StateEQ("active")).Only(ctx)
		if ent.IsNotFound(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		result = IntegrationCredentialRecord{ID: row.ID, PluginID: row.PluginID, Label: row.Label, SecretReference: row.SecretReference, SecretVersion: row.SecretVersion, State: row.State}
		return nil
	})
	return result, err
}

func (s *Store) IntegrationInstanceActive(ctx context.Context, pluginID, externalInstanceID string) (bool, error) {
	if !validPluginID(pluginID) || externalInstanceID == "" {
		return false, invalid("Invalid integration instance")
	}
	var active bool
	err := s.tx(ctx, func(t *transaction) error {
		var err error
		active, err = t.client.IntegrationInstance.Query().Where(integrationinstance.OrganizationEQ(s.Organization), integrationinstance.PluginIDEQ(pluginID), integrationinstance.ExternalInstanceIDEQ(externalInstanceID), integrationinstance.StateEQ("active")).Exist(ctx)
		return err
	})
	return active, err
}

func (s *Store) DisableIntegrationInstance(ctx context.Context, id string) error {
	if !ValidID(id) {
		return invalid("Invalid integration instance ID")
	}
	return s.tx(ctx, func(t *transaction) error {
		row, err := t.client.IntegrationInstance.Query().Where(integrationinstance.IDEQ(strings.ToLower(id)), integrationinstance.OrganizationEQ(s.Organization)).Only(ctx)
		if ent.IsNotFound(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if err = t.client.IntegrationInstance.UpdateOneID(row.ID).SetState("disabled").SetUpdatedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		remaining, err := t.client.IntegrationInstance.Query().Where(integrationinstance.CredentialIDEQ(row.CredentialID), integrationinstance.StateEQ("active")).Exist(ctx)
		if err != nil {
			return err
		}
		if !remaining {
			return t.client.IntegrationCredential.UpdateOneID(row.CredentialID).SetState("disabled").SetUpdatedAt(t.now).Exec(ctx)
		}
		return nil
	})
}

func IsNotFound(err error) bool {
	var fault *Fault
	return errors.As(err, &fault) && fault.Status == 404
}

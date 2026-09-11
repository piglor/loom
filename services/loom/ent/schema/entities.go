// Persistence schemas map to the existing versioned database. Do not auto-migrate production.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type Goal struct{ ent.Schema }

func (Goal) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("organization"),
		field.String("title"),
		field.String("objective"),
		field.String("state"),
		field.Int("phase").Default(0),
		field.JSON("completion_criteria", map[string]any{}),
		field.Time("created_at").Optional(),
		field.Time("updated_at").Optional(),
		field.Time("ended_at").Optional().Nillable(),
		field.String("waiting_reason").Optional().Nillable(),
	}
}
func (Goal) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "goals"}}
}

type Run struct{ ent.Schema }

func (Run) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("goal_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.JSON("policy", map[string]any{}),
		field.JSON("context", map[string]any{}).Optional(),
		field.String("workflow_id").Optional().Nillable(),
		field.String("workflow_definition_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).Optional().Nillable(),
		field.String("workflow_version_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).Optional().Nillable(),
		field.String("parent_run_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).Optional().Nillable(),
		field.String("invoking_step_key").Optional().Nillable(),
		field.String("state").Default("legacy"),
		field.String("current_step_key").Optional().Nillable(),
		field.String("orchestration_reference").Optional().Nillable(),
		field.Time("created_at").Optional(),
		field.Time("updated_at").Optional(),
		field.Time("ended_at").Optional().Nillable(),
	}
}

type WorkflowDefinition struct{ ent.Schema }

func (WorkflowDefinition) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("organization"),
		field.String("name"),
		field.String("description").Default(""),
		field.String("state").Default("draft"),
		field.JSON("draft_spec", map[string]any{}),
		field.Int("latest_version").Default(0),
		field.Time("created_at").Optional(),
		field.Time("updated_at").Optional(),
	}
}
func (WorkflowDefinition) Indexes() []ent.Index {
	return []ent.Index{index.Fields("organization", "name").Unique()}
}
func (WorkflowDefinition) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "workflow_definitions"}}
}

type WorkflowVersion struct{ ent.Schema }

func (WorkflowVersion) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("definition_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("organization"),
		field.Int("version").Positive(),
		field.JSON("spec", map[string]any{}),
		field.String("spec_digest"),
		field.Time("created_at").Optional(),
	}
}
func (WorkflowVersion) Indexes() []ent.Index {
	return []ent.Index{index.Fields("definition_id", "version").Unique()}
}
func (WorkflowVersion) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "workflow_versions"}}
}

type WorkflowTriggerBinding struct{ ent.Schema }

func (WorkflowTriggerBinding) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("organization"),
		field.String("workflow_version_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("integration_instance_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("source"),
		field.String("ingress_instance"),
		field.String("event_type"),
		field.String("resource").Default("*"),
		field.String("version").Default("*"),
		field.Bool("enabled").Default(true),
		field.Time("created_at").Optional(),
	}
}
func (WorkflowTriggerBinding) Indexes() []ent.Index {
	return []ent.Index{index.Fields("organization", "source", "ingress_instance", "event_type")}
}
func (WorkflowTriggerBinding) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "workflow_trigger_bindings"}}
}

type WorkflowStepRun struct{ ent.Schema }

func (WorkflowStepRun) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("run_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("step_key"),
		field.String("step_type"),
		field.Int("position").NonNegative(),
		field.String("state"),
		field.Int("attempt").Default(1),
		field.JSON("input", map[string]any{}).Optional(),
		field.JSON("output", map[string]any{}).Optional(),
		field.String("error_code").Optional().Nillable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("ended_at").Optional().Nillable(),
	}
}
func (WorkflowStepRun) Indexes() []ent.Index {
	return []ent.Index{index.Fields("run_id", "step_key", "attempt").Unique()}
}
func (WorkflowStepRun) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "workflow_step_runs"}}
}
func (Run) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "runs"}}
}

type Worker struct{ ent.Schema }

func (Worker) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString),
		field.String("runtime"),
		field.JSON("capabilities", []string{}),
		field.String("organization").Optional().Nillable(),
		field.String("token_hash").Optional().Nillable(),
		field.Time("revoked_at").Optional().Nillable(),
		field.Time("last_seen_at").Optional().Nillable(),
		field.String("workspace_ref").Optional().Nillable(),
		field.JSON("labels", map[string]any{}).Optional(),
	}
}
func (Worker) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "workers"}}
}

type Session struct{ ent.Schema }

func (Session) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("run_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("worker_id"),
		field.String("runtime"),
		field.String("provider_session_id").Optional().Nillable(),
	}
}
func (Session) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "sessions"}}
}

type Wait struct{ ent.Schema }

func (Wait) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("goal_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("run_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).Optional().Nillable(),
		field.Int("generation").Default(1),
		field.JSON("condition", map[string]any{}),
		field.Time("armed_at").Optional().Nillable(),
		field.Time("satisfied_at").Optional().Nillable(),
		field.String("event_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).Optional().Nillable(),
		field.Time("closed_at").Optional().Nillable(),
		field.String("prepared_by_attempt").SchemaType(map[string]string{dialect.Postgres: "uuid"}).Optional().Nillable(),
	}
}
func (Wait) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "waits"}}
}

type WaitHistory struct{ ent.Schema }

func (WaitHistory) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("goal_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("run_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).Optional().Nillable(),
		field.Int("generation"),
		field.JSON("condition", map[string]any{}),
		field.Time("armed_at").Optional().Nillable(),
		field.Time("satisfied_at").Optional().Nillable(),
		field.String("event_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).Optional().Nillable(),
		field.Time("closed_at").Optional().Nillable(),
		field.String("prepared_by_attempt").SchemaType(map[string]string{dialect.Postgres: "uuid"}).Optional().Nillable(),
	}
}
func (WaitHistory) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "wait_history"}}
}

type Attempt struct{ ent.Schema }

func (Attempt) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("goal_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("run_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("session_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("worker_id"),
		field.Int("phase"),
		field.String("state"),
		field.String("outcome").Optional().Nillable(),
		field.Time("started_at").Optional(),
		field.Time("stopped_at").Optional().Nillable(),
		field.Float("duration_ms").Optional().Nillable(),
	}
}
func (Attempt) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "attempts"}}
}

type Event struct{ ent.Schema }

func (Event) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("organization"),
		field.String("source"),
		field.String("delivery_id"),
		field.String("digest"),
		field.JSON("body", map[string]any{}),
		field.String("disposition"),
		field.Time("received_at").Optional(),
	}
}
func (Event) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "events"}}
}

type Command struct{ ent.Schema }

func (Command) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("goal_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("attempt_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("worker_id"),
		field.String("state"),
		field.String("claim_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).Optional().Nillable(),
		field.String("report_digest").Optional().Nillable(),
		field.Time("created_at").Optional(),
		field.Time("claimed_at").Optional().Nillable(),
		field.Time("stopped_at").Optional().Nillable(),
	}
}
func (Command) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "commands"}}
}

type Audit struct{ ent.Schema }

func (Audit) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").StorageKey("sequence"),
		field.String("goal_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("action"),
		field.JSON("details", map[string]any{}),
		field.Time("recorded_at").Optional(),
	}
}
func (Audit) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "audit"}}
}

type Outbox struct{ ent.Schema }

func (Outbox) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("goal_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("run_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("step_key").Default(""),
		field.String("kind"),
		field.Time("available_at").Optional(),
		field.Time("delivered_at").Optional().Nillable(),
		field.Int("failures").Default(0),
	}
}
func (Outbox) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "outbox"}}
}

type IntegrationBinding struct{ ent.Schema }

func (IntegrationBinding) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("goal_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.Int("generation"),
		field.String("organization"),
		field.String("source"),
		field.String("instance"),
		field.String("event_type"),
		field.String("resource"),
		field.String("version"),
		field.JSON("attributes", map[string]any{}).Optional(),
		field.Time("reconciled_at").Optional().Nillable(),
	}
}
func (IntegrationBinding) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "integration_bindings"}}
}

type IntegrationDelivery struct{ ent.Schema }

func (IntegrationDelivery) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("organization"),
		field.String("source"),
		field.String("instance"),
		field.String("delivery_id"),
		field.String("digest"),
		field.String("fingerprint").Optional().Nillable(),
		field.JSON("condition", map[string]any{}).Optional(),
		field.JSON("details", map[string]any{}),
		field.Bytes("raw_body").Optional(),
		field.String("disposition"),
		field.Time("received_at").Optional(),
	}
}
func (IntegrationDelivery) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "integration_deliveries"}}
}

type IntegrationCredential struct{ ent.Schema }

func (IntegrationCredential) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("organization"),
		field.String("plugin_id"),
		field.String("label"),
		field.String("secret_reference").Unique(),
		field.Int("secret_version").Positive(),
		field.String("state"),
		field.Time("created_at").Optional(),
		field.Time("updated_at").Optional(),
	}
}
func (IntegrationCredential) Indexes() []ent.Index {
	return []ent.Index{index.Fields("organization", "plugin_id")}
}
func (IntegrationCredential) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "integration_credentials"}}
}

type IntegrationInstance struct{ ent.Schema }

func (IntegrationInstance) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("organization"),
		field.String("credential_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}),
		field.String("plugin_id"),
		field.String("external_instance_id"),
		field.String("routing_identity"),
		field.String("account_id"),
		field.String("account_label"),
		field.String("repository_selection"),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.String("state"),
		field.Time("last_verified_at").Optional().Nillable(),
		field.Time("created_at").Optional(),
		field.Time("updated_at").Optional(),
	}
}
func (IntegrationInstance) Indexes() []ent.Index {
	return []ent.Index{index.Fields("organization", "plugin_id", "external_instance_id").Unique()}
}
func (IntegrationInstance) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "integration_instances"}}
}

type IntegrationSetupSession struct{ ent.Schema }

func (IntegrationSetupSession) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).DefaultFunc(uuid.NewString),
		field.String("organization"),
		field.String("plugin_id"),
		field.String("mode"),
		field.String("state_hash").Unique(),
		field.String("stage"),
		field.String("credential_id").SchemaType(map[string]string{dialect.Postgres: "uuid"}).Optional().Nillable(),
		field.String("pending_installation_id").Optional().Nillable(),
		field.Time("expires_at"),
		field.Time("consumed_at").Optional().Nillable(),
		field.Time("created_at").Optional(),
	}
}
func (IntegrationSetupSession) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "integration_setup_sessions"}}
}

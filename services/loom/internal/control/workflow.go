package control

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/piglor/loom/services/loom/ent"
	"github.com/piglor/loom/services/loom/ent/integrationbinding"
	"github.com/piglor/loom/services/loom/ent/integrationdelivery"
	"github.com/piglor/loom/services/loom/ent/integrationinstance"
	"github.com/piglor/loom/services/loom/ent/wait"
	"github.com/piglor/loom/services/loom/ent/worker"
	"github.com/piglor/loom/services/loom/ent/workflowdefinition"
	"github.com/piglor/loom/services/loom/ent/workflowsteprun"
	"github.com/piglor/loom/services/loom/ent/workflowtriggerbinding"
	"github.com/piglor/loom/services/loom/ent/workflowversion"
)

const WorkflowSchemaVersion = 1
const maxWorkflowSubflowDepth = 8

var workflowKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type WorkflowTrigger struct {
	Type                  string `json:"type"`
	IntegrationInstanceID string `json:"integration_instance_id,omitempty"`
	Source                string `json:"source,omitempty"`
	EventType             string `json:"event_type,omitempty"`
	Resource              string `json:"resource,omitempty"`
	Version               string `json:"version,omitempty"`
}

type WorkflowStep struct {
	Key    string         `json:"key"`
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}

type WorkflowEdge struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Outcome string `json:"outcome"`
}

type WorkflowSpec struct {
	SchemaVersion int               `json:"schema_version"`
	Triggers      []WorkflowTrigger `json:"triggers"`
	Steps         []WorkflowStep    `json:"steps"`
	Edges         []WorkflowEdge    `json:"edges"`
}

type WorkflowDefinitionRecord struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Description   string       `json:"description"`
	State         string       `json:"state"`
	DraftSpec     WorkflowSpec `json:"draft_spec"`
	LatestVersion int          `json:"latest_version"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

type CreateWorkflow struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Spec        WorkflowSpec `json:"spec"`
}

type UpdateWorkflow struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Spec        WorkflowSpec `json:"spec"`
}

type WorkflowValidation struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
}

type PublishedWorkflow struct {
	DefinitionID string       `json:"definition_id"`
	VersionID    string       `json:"version_id"`
	Version      int          `json:"version"`
	Digest       string       `json:"digest"`
	Spec         WorkflowSpec `json:"spec"`
}

func defaultWorkflowSpec() WorkflowSpec {
	return WorkflowSpec{
		SchemaVersion: WorkflowSchemaVersion,
		Triggers:      []WorkflowTrigger{{Type: "manual"}},
		Steps: []WorkflowStep{
			{Key: "agent", Name: "Agent works", Type: "agent", Config: map[string]any{"runtime": "demo"}},
			{Key: "complete", Name: "Complete goal", Type: "complete", Config: map[string]any{}},
		},
		Edges: []WorkflowEdge{{From: "agent", To: "complete", Outcome: "success"}},
	}
}

func decodeSpec(value map[string]any) (WorkflowSpec, error) {
	var spec WorkflowSpec
	body, err := json.Marshal(value)
	if err == nil {
		err = json.Unmarshal(body, &spec)
	}
	return spec, err
}

func workflowSpecMap(spec WorkflowSpec) map[string]any { return jsonObject(spec) }

func ValidateWorkflowSpec(spec WorkflowSpec) WorkflowValidation {
	errors := []string{}
	if spec.SchemaVersion != WorkflowSchemaVersion {
		errors = append(errors, "schema_version must be 1")
	}
	if len(spec.Triggers) == 0 || len(spec.Triggers) > 20 {
		errors = append(errors, "one to twenty triggers are required")
	}
	for _, trigger := range spec.Triggers {
		switch trigger.Type {
		case "manual":
		case "integration_event":
			if !ValidID(trigger.IntegrationInstanceID) || !validPluginID(trigger.Source) || !textBetween(trigger.EventType, 120) {
				errors = append(errors, "integration triggers require a connection, source, and event type")
			}
		default:
			errors = append(errors, "trigger type must be manual or integration_event")
		}
	}
	if len(spec.Steps) < 2 || len(spec.Steps) > 50 {
		errors = append(errors, "two to fifty steps are required")
	}
	steps := map[string]WorkflowStep{}
	complete := 0
	agentBinding := ""
	for _, step := range spec.Steps {
		if !workflowKeyPattern.MatchString(step.Key) || !textBetween(strings.TrimSpace(step.Name), 120) {
			errors = append(errors, "every step requires a stable key and name")
		}
		if _, exists := steps[step.Key]; exists {
			errors = append(errors, "step keys must be unique")
		}
		steps[step.Key] = step
		for _, key := range unknownWorkflowConfigKeys(step) {
			errors = append(errors, "unsupported "+step.Type+" config key: "+key)
		}
		switch step.Type {
		case "agent":
			runtime, _ := step.Config["runtime"].(string)
			if runtime != "demo" && runtime != "remote-demo" && runtime != "codex-container" {
				errors = append(errors, "agent steps require a supported runtime")
			}
			workerID, _ := step.Config["worker_id"].(string)
			if runtime != "demo" && !ValidID(workerID) {
				errors = append(errors, "remote agent steps require a worker_id")
			}
			binding := runtime + "\x00" + workerID
			if agentBinding == "" {
				agentBinding = binding
			} else if agentBinding != binding {
				errors = append(errors, "all agent steps in one workflow run must use the same runtime and worker")
			}
		case "wait_event":
			if _, ok := conditionFromConfig(step.Config); !ok {
				errors = append(errors, "wait_event steps require an exact event condition")
			}
		case "condition":
			if _, _, _, ok := workflowPredicateFromConfig(step.Config); !ok {
				errors = append(errors, "condition steps require path and equals, not_equals, or exists")
			}
		case "subflow":
			if !ValidID(subflowDefinitionID(step.Config)) {
				errors = append(errors, "subflow steps require a workflow_definition_id")
			}
		case "complete":
			complete++
		default:
			errors = append(errors, "unsupported step type")
		}
	}
	if complete == 0 {
		errors = append(errors, "a complete step is required")
	}
	indegree := map[string]int{}
	adjacency := map[string][]string{}
	edges := map[string]bool{}
	for _, edge := range spec.Edges {
		if _, ok := steps[edge.From]; !ok {
			errors = append(errors, "edge source does not exist")
		}
		if _, ok := steps[edge.To]; !ok {
			errors = append(errors, "edge destination does not exist")
		}
		if edge.From == edge.To || (edge.Outcome != "success" && edge.Outcome != "failure") {
			errors = append(errors, "edges require different steps and a success or failure outcome")
		}
		identity := edge.From + "\x00" + edge.Outcome
		if edges[identity] {
			errors = append(errors, "a step may have only one edge for each outcome")
		}
		edges[identity] = true
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
		indegree[edge.To]++
		if steps[edge.From].Type == "complete" {
			errors = append(errors, "complete steps cannot have outgoing edges")
		}
	}
	for _, step := range spec.Steps {
		if step.Type != "complete" && !edges[step.Key+"\x00success"] {
			errors = append(errors, "every executable step requires a success relationship")
		}
		if step.Type != "condition" {
			continue
		}
		if !edges[step.Key+"\x00success"] || !edges[step.Key+"\x00failure"] {
			errors = append(errors, "condition steps require both success and failure relationships")
		}
	}
	roots := []string{}
	for key := range steps {
		if indegree[key] == 0 {
			roots = append(roots, key)
		}
	}
	if len(roots) != 1 {
		errors = append(errors, "workflow must have exactly one entry step")
	} else {
		seen, active := map[string]bool{}, map[string]bool{}
		var visit func(string)
		visit = func(key string) {
			if active[key] {
				errors = append(errors, "workflow relationships must be acyclic")
				return
			}
			if seen[key] {
				return
			}
			seen[key], active[key] = true, true
			for _, next := range adjacency[key] {
				visit(next)
			}
			active[key] = false
		}
		visit(roots[0])
		if len(seen) != len(steps) {
			errors = append(errors, "every step must be reachable from the entry step")
		}
	}
	sort.Strings(errors)
	return WorkflowValidation{Valid: len(errors) == 0, Errors: errors}
}

func unknownWorkflowConfigKeys(step WorkflowStep) []string {
	allowed := map[string]bool{}
	switch step.Type {
	case "agent":
		allowed["runtime"], allowed["worker_id"] = true, true
	case "wait_event":
		allowed["integration_instance_id"], allowed["condition"] = true, true
	case "condition":
		allowed["path"], allowed["equals"], allowed["not_equals"], allowed["exists"] = true, true, true, true
	case "subflow":
		allowed["workflow_definition_id"], allowed["workflow_version_id"] = true, true
	case "complete":
		// Complete steps intentionally have no configuration.
	}
	keys := make([]string, 0)
	for key := range step.Config {
		if !allowed[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func conditionFromConfig(config map[string]any) (Condition, bool) {
	value, ok := config["condition"]
	if !ok {
		return Condition{}, false
	}
	object, ok := value.(map[string]any)
	if !ok {
		return Condition{}, false
	}
	for key := range object {
		if key != "source" && key != "type" && key != "resource" && key != "version" {
			return Condition{}, false
		}
	}
	body, err := json.Marshal(value)
	var condition Condition
	if err != nil || json.Unmarshal(body, &condition) != nil || condition.Validate() != nil {
		return Condition{}, false
	}
	return condition, true
}

func workflowPredicateFromConfig(config map[string]any) (string, any, string, bool) {
	path, _ := config["path"].(string)
	path = strings.TrimSpace(path)
	if !textBetween(path, 256) || strings.HasPrefix(path, ".") || strings.HasSuffix(path, ".") || strings.Contains(path, "..") {
		return "", nil, "", false
	}
	for _, part := range strings.Split(path, ".") {
		if !workflowKeyPattern.MatchString(part) {
			return "", nil, "", false
		}
	}
	if path != "trigger" && !strings.HasPrefix(path, "trigger.") && path != "last_event" && !strings.HasPrefix(path, "last_event.") {
		return "", nil, "", false
	}
	value, equals := config["equals"]
	notValue, notEquals := config["not_equals"]
	existsValue, exists := config["exists"]
	if equals == notEquals || (exists && (equals || notEquals)) {
		return "", nil, "", false
	}
	if equals {
		return path, value, "equals", true
	}
	if notEquals {
		return path, notValue, "not_equals", true
	}
	if exists {
		value, ok := existsValue.(bool)
		if ok {
			return path, value, "exists", true
		}
	}
	return "", nil, "", false
}

func subflowDefinitionID(config map[string]any) string {
	id, _ := config["workflow_definition_id"].(string)
	return strings.ToLower(strings.TrimSpace(id))
}

func (s *Store) pinAndValidateSubflows(ctx context.Context, t *transaction, rootID string, spec *WorkflowSpec) error {
	for index, step := range spec.Steps {
		if step.Type != "subflow" {
			continue
		}
		childID := subflowDefinitionID(step.Config)
		child, err := t.client.WorkflowDefinition.Query().Where(workflowdefinition.IDEQ(childID), workflowdefinition.OrganizationEQ(s.Organization), workflowdefinition.StateEQ("published")).Only(ctx)
		if ent.IsNotFound(err) {
			return conflict("Subflow workflow is not published")
		}
		if err != nil {
			return err
		}
		version, err := t.client.WorkflowVersion.Query().Where(workflowversion.DefinitionIDEQ(child.ID), workflowversion.VersionEQ(child.LatestVersion), workflowversion.OrganizationEQ(s.Organization)).Only(ctx)
		if err != nil {
			return err
		}
		config := map[string]any{}
		for key, value := range step.Config {
			config[key] = value
		}
		config["workflow_version_id"] = version.ID
		spec.Steps[index].Config = config
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string, WorkflowSpec, int) error
	visit = func(id string, current WorkflowSpec, depth int) error {
		if depth > maxWorkflowSubflowDepth {
			return conflict("Workflow subflow nesting exceeds the safety limit")
		}
		if visiting[id] {
			return conflict("Workflow subflow relationships must be acyclic")
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, step := range current.Steps {
			if step.Type != "subflow" {
				continue
			}
			childID := subflowDefinitionID(step.Config)
			if childID == rootID {
				return conflict("Workflow subflow relationships must be acyclic")
			}
			versionID, _ := step.Config["workflow_version_id"].(string)
			var childSpec WorkflowSpec
			var err error
			if ValidID(versionID) {
				version, lookupErr := t.client.WorkflowVersion.Query().Where(workflowversion.IDEQ(versionID), workflowversion.OrganizationEQ(s.Organization), workflowversion.DefinitionIDEQ(childID)).Only(ctx)
				if lookupErr != nil {
					return lookupErr
				}
				childSpec, err = decodeSpec(version.Spec)
			} else {
				child, lookupErr := t.client.WorkflowDefinition.Query().Where(workflowdefinition.IDEQ(childID), workflowdefinition.OrganizationEQ(s.Organization), workflowdefinition.StateEQ("published")).Only(ctx)
				if lookupErr != nil {
					return lookupErr
				}
				version, lookupErr := t.client.WorkflowVersion.Query().Where(workflowversion.DefinitionIDEQ(child.ID), workflowversion.VersionEQ(child.LatestVersion), workflowversion.OrganizationEQ(s.Organization)).Only(ctx)
				if lookupErr != nil {
					return lookupErr
				}
				childSpec, err = decodeSpec(version.Spec)
			}
			if err != nil {
				return err
			}
			if err = visit(childID, childSpec, depth+1); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	return visit(rootID, *spec, 0)
}

func workflowContextValue(context map[string]any, path string) (any, bool) {
	var value any = context
	for _, part := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return value, true
}

func evaluateWorkflowCondition(config, context map[string]any) (bool, bool) {
	path, expected, operator, ok := workflowPredicateFromConfig(config)
	if !ok {
		return false, false
	}
	actual, exists := workflowContextValue(context, path)
	switch operator {
	case "exists":
		return exists == expected.(bool), true
	case "equals":
		return exists && reflect.DeepEqual(actual, expected), true
	case "not_equals":
		return !exists || !reflect.DeepEqual(actual, expected), true
	default:
		return false, false
	}
}

func workflowRunContext(condition Condition, details map[string]any) map[string]any {
	if details == nil {
		details = map[string]any{}
	}
	return map[string]any{
		"trigger": map[string]any{"condition": jsonObject(condition), "details": details},
	}
}

func integrationInstanceIDFromConfig(config map[string]any) string {
	id, _ := config["integration_instance_id"].(string)
	return id
}

func workflowEntry(spec WorkflowSpec) string {
	indegree := map[string]int{}
	for _, edge := range spec.Edges {
		indegree[edge.To]++
	}
	for _, step := range spec.Steps {
		if indegree[step.Key] == 0 {
			return step.Key
		}
	}
	return ""
}

func (s *Store) CreateWorkflow(ctx context.Context, request CreateWorkflow) (WorkflowDefinitionRecord, error) {
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	if !textBetween(request.Name, 120) || len(request.Description) > 1000 {
		return WorkflowDefinitionRecord{}, invalid("Workflow name is required")
	}
	if request.Spec.SchemaVersion == 0 {
		request.Spec = defaultWorkflowSpec()
	}
	var result WorkflowDefinitionRecord
	err := s.tx(ctx, func(t *transaction) error {
		row, err := t.client.WorkflowDefinition.Create().SetOrganization(s.Organization).SetName(request.Name).SetDescription(request.Description).SetState("draft").SetDraftSpec(workflowSpecMap(request.Spec)).SetCreatedAt(t.now).SetUpdatedAt(t.now).Save(ctx)
		if ent.IsConstraintError(err) {
			return conflict("Workflow name already exists")
		}
		if err == nil {
			result = workflowRecord(row)
		}
		return err
	})
	return result, err
}

func workflowRecord(row *ent.WorkflowDefinition) WorkflowDefinitionRecord {
	spec, _ := decodeSpec(row.DraftSpec)
	return WorkflowDefinitionRecord{ID: row.ID, Name: row.Name, Description: row.Description, State: row.State, DraftSpec: spec, LatestVersion: row.LatestVersion, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func (s *Store) ListWorkflows(ctx context.Context) ([]WorkflowDefinitionRecord, error) {
	result := []WorkflowDefinitionRecord{}
	err := s.tx(ctx, func(t *transaction) error {
		rows, err := t.client.WorkflowDefinition.Query().Where(workflowdefinition.OrganizationEQ(s.Organization)).Order(ent.Asc(workflowdefinition.FieldName)).All(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			result = append(result, workflowRecord(row))
		}
		return nil
	})
	return result, err
}

func (s *Store) Workflow(ctx context.Context, id string) (WorkflowDefinitionRecord, error) {
	if !ValidID(id) {
		return WorkflowDefinitionRecord{}, invalid("Invalid workflow ID")
	}
	var result WorkflowDefinitionRecord
	err := s.tx(ctx, func(t *transaction) error {
		row, err := t.client.WorkflowDefinition.Query().Where(workflowdefinition.IDEQ(strings.ToLower(id)), workflowdefinition.OrganizationEQ(s.Organization)).Only(ctx)
		if ent.IsNotFound(err) {
			return notFound()
		}
		if err == nil {
			result = workflowRecord(row)
		}
		return err
	})
	return result, err
}

func (s *Store) WorkflowVersion(ctx context.Context, id string) (PublishedWorkflow, error) {
	if !ValidID(id) {
		return PublishedWorkflow{}, invalid("Invalid workflow version ID")
	}
	var result PublishedWorkflow
	err := s.tx(ctx, func(t *transaction) error {
		row, err := t.client.WorkflowVersion.Query().Where(workflowversion.IDEQ(strings.ToLower(id)), workflowversion.OrganizationEQ(s.Organization)).Only(ctx)
		if ent.IsNotFound(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		spec, err := decodeSpec(row.Spec)
		if err != nil {
			return err
		}
		result = PublishedWorkflow{DefinitionID: row.DefinitionID, VersionID: row.ID, Version: row.Version, Digest: row.SpecDigest, Spec: spec}
		return nil
	})
	return result, err
}

func (s *Store) UpdateWorkflow(ctx context.Context, id string, request UpdateWorkflow) (WorkflowDefinitionRecord, error) {
	if !ValidID(id) || !textBetween(strings.TrimSpace(request.Name), 120) || len(request.Description) > 1000 {
		return WorkflowDefinitionRecord{}, invalid("Invalid workflow")
	}
	var result WorkflowDefinitionRecord
	err := s.tx(ctx, func(t *transaction) error {
		row, err := t.client.WorkflowDefinition.Query().Where(workflowdefinition.IDEQ(strings.ToLower(id)), workflowdefinition.OrganizationEQ(s.Organization)).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		row, err = t.client.WorkflowDefinition.UpdateOneID(row.ID).SetName(strings.TrimSpace(request.Name)).SetDescription(strings.TrimSpace(request.Description)).SetDraftSpec(workflowSpecMap(request.Spec)).SetUpdatedAt(t.now).Save(ctx)
		if ent.IsConstraintError(err) {
			return conflict("Workflow name already exists")
		}
		if err == nil {
			result = workflowRecord(row)
		}
		return err
	})
	return result, err
}

func (s *Store) PublishWorkflow(ctx context.Context, id string) (PublishedWorkflow, error) {
	if !ValidID(id) {
		return PublishedWorkflow{}, invalid("Invalid workflow ID")
	}
	var result PublishedWorkflow
	err := s.tx(ctx, func(t *transaction) error {
		definition, err := t.client.WorkflowDefinition.Query().Where(workflowdefinition.IDEQ(strings.ToLower(id)), workflowdefinition.OrganizationEQ(s.Organization)).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		spec, err := decodeSpec(definition.DraftSpec)
		if err != nil {
			return invalid("Invalid workflow specification")
		}
		validation := ValidateWorkflowSpec(spec)
		if !validation.Valid {
			return invalid(strings.Join(validation.Errors, "; "))
		}
		for _, step := range spec.Steps {
			if step.Type != "agent" {
				continue
			}
			runtime, _ := step.Config["runtime"].(string)
			if runtime == "codex-container" && !s.EnableCodex {
				return conflict("Contained runtime is disabled")
			}
			if runtime == "demo" {
				continue
			}
			workerID, _ := step.Config["worker_id"].(string)
			_, lookupErr := t.client.Worker.Query().Where(worker.IDEQ(workerID), worker.RuntimeEQ(runtime), worker.OrganizationEQ(s.Organization), worker.RevokedAtIsNil()).Only(ctx)
			if ent.IsNotFound(lookupErr) {
				return conflict("Workflow agent is unavailable")
			}
			if lookupErr != nil {
				return lookupErr
			}
		}
		for _, trigger := range spec.Triggers {
			if trigger.Type != "integration_event" {
				continue
			}
			instance, lookupErr := t.client.IntegrationInstance.Query().Where(integrationinstance.IDEQ(trigger.IntegrationInstanceID), integrationinstance.OrganizationEQ(s.Organization), integrationinstance.PluginIDEQ(trigger.Source), integrationinstance.StateEQ("active")).Only(ctx)
			if ent.IsNotFound(lookupErr) {
				return conflict("Workflow trigger connection is unavailable")
			}
			if lookupErr != nil {
				return lookupErr
			}
			if instance.RoutingIdentity == "" {
				return conflict("Workflow trigger connection has no routing identity")
			}
		}
		for _, step := range spec.Steps {
			if step.Type != "wait_event" || integrationInstanceIDFromConfig(step.Config) == "" {
				continue
			}
			condition, _ := conditionFromConfig(step.Config)
			instanceID := integrationInstanceIDFromConfig(step.Config)
			instance, lookupErr := t.client.IntegrationInstance.Query().Where(integrationinstance.IDEQ(instanceID), integrationinstance.OrganizationEQ(s.Organization), integrationinstance.PluginIDEQ(condition.Source), integrationinstance.StateEQ("active")).Only(ctx)
			if ent.IsNotFound(lookupErr) {
				return conflict("Workflow wait connection is unavailable")
			}
			if lookupErr != nil {
				return lookupErr
			}
			if instance.RoutingIdentity == "" {
				return conflict("Workflow wait connection has no routing identity")
			}
		}
		for _, step := range spec.Steps {
			if step.Type != "subflow" {
				continue
			}
			childID := subflowDefinitionID(step.Config)
			if childID == definition.ID {
				return conflict("A workflow cannot invoke itself")
			}
			child, lookupErr := t.client.WorkflowDefinition.Query().Where(workflowdefinition.IDEQ(childID), workflowdefinition.OrganizationEQ(s.Organization), workflowdefinition.StateEQ("published")).Only(ctx)
			if ent.IsNotFound(lookupErr) {
				return conflict("Subflow workflow is not published")
			}
			if lookupErr != nil {
				return lookupErr
			}
			if child.LatestVersion == 0 {
				return conflict("Subflow workflow has no published version")
			}
		}
		if err = s.pinAndValidateSubflows(ctx, t, definition.ID, &spec); err != nil {
			return err
		}
		body, _ := canonicalJSON(spec, true)
		previousVersions, err := t.client.WorkflowVersion.Query().Where(workflowversion.DefinitionIDEQ(definition.ID), workflowversion.OrganizationEQ(s.Organization)).IDs(ctx)
		if err != nil {
			return err
		}
		if len(previousVersions) > 0 {
			if err = t.client.WorkflowTriggerBinding.Update().Where(workflowtriggerbinding.WorkflowVersionIDIn(previousVersions...)).SetEnabled(false).Exec(ctx); err != nil {
				return err
			}
		}
		versionNumber := definition.LatestVersion + 1
		versionID := ID()
		if err = t.client.WorkflowVersion.Create().SetID(versionID).SetDefinitionID(definition.ID).SetOrganization(s.Organization).SetVersion(versionNumber).SetSpec(workflowSpecMap(spec)).SetSpecDigest(hash(body)).SetCreatedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		for _, trigger := range spec.Triggers {
			if trigger.Type != "integration_event" {
				continue
			}
			instance, lookupErr := t.client.IntegrationInstance.Get(ctx, trigger.IntegrationInstanceID)
			if lookupErr != nil {
				return lookupErr
			}
			resource, version := trigger.Resource, trigger.Version
			if resource == "" {
				resource = "*"
			}
			if version == "" {
				version = "*"
			}
			if err = t.client.WorkflowTriggerBinding.Create().SetOrganization(s.Organization).SetWorkflowVersionID(versionID).SetIntegrationInstanceID(instance.ID).SetSource(trigger.Source).SetIngressInstance(instance.RoutingIdentity).SetEventType(trigger.EventType).SetResource(resource).SetVersion(version).SetEnabled(true).SetCreatedAt(t.now).Exec(ctx); err != nil {
				return err
			}
		}
		if err = t.client.WorkflowDefinition.UpdateOneID(definition.ID).SetState("published").SetLatestVersion(versionNumber).SetUpdatedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		result = PublishedWorkflow{DefinitionID: definition.ID, VersionID: versionID, Version: versionNumber, Digest: hash(body), Spec: spec}
		return nil
	})
	return result, err
}

func matchWorkflowSelector(value, expected string) bool { return expected == "*" || value == expected }

func (s *Store) routeWorkflowTriggers(ctx context.Context, t *transaction, receipt *ent.IntegrationDelivery, condition Condition) (int, error) {
	bindings, err := t.client.WorkflowTriggerBinding.Query().Where(workflowtriggerbinding.OrganizationEQ(s.Organization), workflowtriggerbinding.SourceEQ(receipt.Source), workflowtriggerbinding.IngressInstanceEQ(receipt.Instance), workflowtriggerbinding.EventTypeEQ(condition.Type), workflowtriggerbinding.EnabledEQ(true)).All(ctx)
	if err != nil {
		return 0, err
	}
	started := 0
	for _, binding := range bindings {
		if !matchWorkflowSelector(condition.Resource, binding.Resource) || !matchWorkflowSelector(condition.Version, binding.Version) {
			continue
		}
		version, lookupErr := t.client.WorkflowVersion.Query().Where(workflowversion.IDEQ(binding.WorkflowVersionID), workflowversion.OrganizationEQ(s.Organization)).Only(ctx)
		if lookupErr != nil {
			return started, lookupErr
		}
		if _, startErr := s.startWorkflowFromReceipt(ctx, t, version, receipt, condition); startErr != nil {
			return started, startErr
		}
		started++
	}
	return started, nil
}

func (s *Store) startWorkflowFromReceipt(ctx context.Context, t *transaction, version *ent.WorkflowVersion, receipt *ent.IntegrationDelivery, condition Condition) (string, error) {
	spec, err := decodeSpec(version.Spec)
	if err != nil {
		return "", err
	}
	definition, err := t.client.WorkflowDefinition.Get(ctx, version.DefinitionID)
	if err != nil {
		return "", err
	}
	goalID, runID := ID(), ID()
	entry := workflowEntry(spec)
	entryStep := WorkflowStep{}
	agentStep := WorkflowStep{}
	waitCondition := condition
	for _, step := range spec.Steps {
		if step.Key == entry {
			entryStep = step
		}
		if agentStep.Key == "" && step.Type == "agent" {
			agentStep = step
		}
		if step.Key == entry && step.Type == "wait_event" {
			if configured, ok := conditionFromConfig(step.Config); ok {
				waitCondition = configured
			}
		}
	}
	runtime, _ := agentStep.Config["runtime"].(string)
	if runtime == "" {
		runtime = "demo"
	}
	workerID, _ := agentStep.Config["worker_id"].(string)
	if workerID == "" {
		workerID = "demo-local"
	}
	state := "WAITING"
	if entryStep.Type == "agent" {
		state = "READY"
	}
	if entryStep.Type == "complete" {
		state = "COMPLETED"
	}
	if err = t.client.Goal.Create().SetID(goalID).SetOrganization(s.Organization).SetTitle(definition.Name).SetObjective(definition.Description).SetState(state).SetCompletionCriteria(map[string]any{"workflow_version_id": version.ID}).SetCreatedAt(t.now).SetUpdatedAt(t.now).Exec(ctx); err != nil {
		return "", err
	}
	policy := Policy{Runtime: runtime, MaxAttempts: 100, Lifecycle: "workflow-v1"}
	runState := "pending"
	if entryStep.Type == "wait_event" {
		runState = "waiting"
	} else if entryStep.Type == "complete" {
		runState = "succeeded"
	}
	runCreate := t.client.Run.Create().SetID(runID).SetGoalID(goalID).SetPolicy(jsonObject(policy)).SetContext(workflowRunContext(condition, receipt.Details)).SetWorkflowDefinitionID(definition.ID).SetWorkflowVersionID(version.ID).SetState(runState).SetCurrentStepKey(entry).SetCreatedAt(t.now).SetUpdatedAt(t.now)
	if entryStep.Type == "complete" {
		runCreate.SetEndedAt(t.now)
	}
	if err = runCreate.Exec(ctx); err != nil {
		return "", err
	}
	if err = t.client.Session.Create().SetRunID(runID).SetWorkerID(workerID).SetRuntime(runtime).Exec(ctx); err != nil {
		return "", err
	}
	waitRow, err := t.client.Wait.Create().SetGoalID(goalID).SetRunID(runID).SetCondition(jsonObject(waitCondition)).Save(ctx)
	if err != nil {
		return "", err
	}
	for position, step := range spec.Steps {
		stepState := "pending"
		if step.Key == entry {
			if step.Type == "wait_event" {
				stepState = "waiting"
			} else if step.Type == "complete" {
				stepState = "succeeded"
			}
		}
		if err = t.client.WorkflowStepRun.Create().SetRunID(runID).SetStepKey(step.Key).SetStepType(step.Type).SetPosition(position).SetState(stepState).SetInput(map[string]any{}).SetOutput(map[string]any{}).Exec(ctx); err != nil {
			return "", err
		}
	}
	if entryStep.Type == "wait_event" {
		if err = s.bindWorkflowWait(ctx, t, goalID, waitRow.Generation, entryStep); err != nil {
			return "", err
		}
	}
	if err = audit(ctx, t, goalID, "workflow_started", map[string]any{"workflow_definition_id": definition.ID, "workflow_version_id": version.ID, "run_id": runID, "event_receipt_id": receipt.ID, "entry_step": entry}); err != nil {
		return "", err
	}
	if entryStep.Type == "agent" {
		if err = enqueue(ctx, t, goalID, "agent_step_ready"); err != nil {
			return "", err
		}
	} else if entryStep.Type == "condition" || entryStep.Type == "subflow" {
		workflowRun, lookupErr := t.client.Run.Get(ctx, runID)
		if lookupErr != nil {
			return "", lookupErr
		}
		if err = s.advanceWorkflowDeterministic(ctx, t, goalID, workflowRun, spec, entryStep); err != nil {
			return "", err
		}
	}
	return goalID, nil
}

func (s *Store) StartWorkflow(ctx context.Context, definitionID string) (string, error) {
	if !ValidID(definitionID) {
		return "", invalid("Invalid workflow ID")
	}
	var goalID string
	err := s.tx(ctx, func(t *transaction) error {
		definition, err := t.client.WorkflowDefinition.Query().Where(workflowdefinition.IDEQ(strings.ToLower(definitionID)), workflowdefinition.OrganizationEQ(s.Organization), workflowdefinition.StateEQ("published")).Only(ctx)
		if ent.IsNotFound(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		version, err := t.client.WorkflowVersion.Query().Where(workflowversion.DefinitionIDEQ(definition.ID), workflowversion.VersionEQ(definition.LatestVersion), workflowversion.OrganizationEQ(s.Organization)).Only(ctx)
		if err != nil {
			return err
		}
		spec, err := decodeSpec(version.Spec)
		if err != nil {
			return err
		}
		manual := false
		for _, trigger := range spec.Triggers {
			manual = manual || trigger.Type == "manual"
		}
		if !manual {
			return conflict("Workflow has no manual trigger")
		}
		goalID, err = s.startWorkflowFromReceipt(ctx, t, version, &ent.IntegrationDelivery{ID: "manual:" + ID()}, Condition{Source: "loom", Type: "workflow.manual", Resource: definition.ID, Version: version.ID})
		return err
	})
	return goalID, err
}

func nextWorkflowStep(spec WorkflowSpec, current, outcome string) (WorkflowStep, bool) {
	nextKey := ""
	for _, edge := range spec.Edges {
		if edge.From == current && edge.Outcome == outcome {
			nextKey = edge.To
			break
		}
	}
	for _, step := range spec.Steps {
		if step.Key == nextKey {
			return step, true
		}
	}
	return WorkflowStep{}, false
}

// advanceWorkflowAfterAgent records the workflow decision after an agent step.
// The integration adapter never calls this function; only an admitted agent
// result or a deterministic workflow step can advance the run.
func (s *Store) advanceWorkflowAfterAgent(ctx context.Context, t *transaction, goalID string, workflowRun *ent.Run, succeeded bool) error {
	if workflowRun.WorkflowVersionID == nil || workflowRun.CurrentStepKey == nil {
		return conflict("Workflow run is missing its pinned version or current step")
	}
	version, err := t.client.WorkflowVersion.Get(ctx, *workflowRun.WorkflowVersionID)
	if err != nil {
		return err
	}
	spec, err := decodeSpec(version.Spec)
	if err != nil {
		return err
	}
	current := *workflowRun.CurrentStepKey
	outcome := "success"
	state := "succeeded"
	if !succeeded {
		outcome, state = "failure", "failed"
	}
	if err = t.client.WorkflowStepRun.Update().Where(workflowsteprun.RunIDEQ(workflowRun.ID), workflowsteprun.StepKeyEQ(current), workflowsteprun.StateIn("pending", "running")).SetState(state).SetEndedAt(t.now).Exec(ctx); err != nil {
		return err
	}
	next, exists := nextWorkflowStep(spec, current, outcome)
	if !exists {
		if !succeeded {
			if err = t.client.Run.UpdateOneID(workflowRun.ID).SetState("failed").SetEndedAt(t.now).SetUpdatedAt(t.now).Exec(ctx); err != nil {
				return err
			}
			if workflowRun.ParentRunID != nil {
				return s.finishChildWorkflow(ctx, t, goalID, workflowRun, false)
			}
			return transition(ctx, t, goalID, "FAILED")
		}
		return conflict("Workflow step has no success relationship")
	}
	return s.advanceWorkflowDeterministic(ctx, t, goalID, workflowRun, spec, next)
}

func (s *Store) advanceWorkflowAfterWait(ctx context.Context, t *transaction, goalID string, workflowRun *ent.Run) error {
	if workflowRun.WorkflowVersionID == nil || workflowRun.CurrentStepKey == nil {
		return conflict("Workflow run is missing its pinned version or current step")
	}
	version, err := t.client.WorkflowVersion.Get(ctx, *workflowRun.WorkflowVersionID)
	if err != nil {
		return err
	}
	spec, err := decodeSpec(version.Spec)
	if err != nil {
		return err
	}
	current := *workflowRun.CurrentStepKey
	if err = t.client.WorkflowStepRun.Update().Where(workflowsteprun.RunIDEQ(workflowRun.ID), workflowsteprun.StepKeyEQ(current), workflowsteprun.StepTypeEQ("wait_event"), workflowsteprun.StateEQ("waiting")).SetState("succeeded").SetEndedAt(t.now).Exec(ctx); err != nil {
		return err
	}
	next, exists := nextWorkflowStep(spec, current, "success")
	if !exists {
		return conflict("Wait step has no success relationship")
	}
	return s.advanceWorkflowDeterministic(ctx, t, goalID, workflowRun, spec, next)
}

func (s *Store) advanceWorkflowDeterministic(ctx context.Context, t *transaction, goalID string, workflowRun *ent.Run, spec WorkflowSpec, current WorkflowStep) error {
	for {
		if err := t.client.Run.UpdateOneID(workflowRun.ID).SetCurrentStepKey(current.Key).SetUpdatedAt(t.now).Exec(ctx); err != nil {
			return err
		}
		switch current.Type {
		case "complete":
			if err := t.client.WorkflowStepRun.Update().Where(workflowsteprun.RunIDEQ(workflowRun.ID), workflowsteprun.StepKeyEQ(current.Key)).SetState("succeeded").SetStartedAt(t.now).SetEndedAt(t.now).Exec(ctx); err != nil {
				return err
			}
			return s.completeWorkflowRun(ctx, t, goalID, workflowRun)
		case "condition":
			if err := t.client.WorkflowStepRun.Update().Where(workflowsteprun.RunIDEQ(workflowRun.ID), workflowsteprun.StepKeyEQ(current.Key)).SetState("succeeded").SetStartedAt(t.now).SetEndedAt(t.now).Exec(ctx); err != nil {
				return err
			}
			matched, valid := evaluateWorkflowCondition(current.Config, workflowRun.Context)
			if !valid {
				return conflict("Condition step has an invalid predicate")
			}
			outcome := "failure"
			if matched {
				outcome = "success"
			}
			next, ok := nextWorkflowStep(spec, current.Key, outcome)
			if !ok {
				return conflict("Condition step has no relationship for its result")
			}
			current = next
		case "agent":
			if err := t.client.WorkflowStepRun.Update().Where(workflowsteprun.RunIDEQ(workflowRun.ID), workflowsteprun.StepKeyEQ(current.Key)).SetState("pending").Exec(ctx); err != nil {
				return err
			}
			if err := t.client.Goal.UpdateOneID(goalID).AddPhase(1).ClearWaitingReason().Exec(ctx); err != nil {
				return err
			}
			if err := t.client.Run.UpdateOneID(workflowRun.ID).SetState("running").Exec(ctx); err != nil {
				return err
			}
			if err := transition(ctx, t, goalID, "READY"); err != nil {
				return err
			}
			return enqueueRun(ctx, t, goalID, workflowRun.ID, "agent_step_ready")
		case "wait_event":
			condition, ok := conditionFromConfig(current.Config)
			if !ok {
				return conflict("Wait step has no valid event condition")
			}
			waitRow, err := t.client.Wait.Query().Where(wait.GoalIDEQ(goalID)).Only(ctx)
			if err != nil {
				return err
			}
			generation := waitRow.Generation
			if waitRow.ClosedAt != nil {
				if err = t.client.WaitHistory.Create().SetID(ID()).SetGoalID(waitRow.GoalID).SetNillableRunID(waitRow.RunID).SetGeneration(waitRow.Generation).SetCondition(waitRow.Condition).SetNillableArmedAt(waitRow.ArmedAt).SetNillableSatisfiedAt(waitRow.SatisfiedAt).SetNillableEventID(waitRow.EventID).SetNillableClosedAt(waitRow.ClosedAt).SetNillablePreparedByAttempt(waitRow.PreparedByAttempt).Exec(ctx); err != nil {
					return err
				}
				if _, err = t.client.IntegrationBinding.Delete().Where(integrationbinding.GoalIDEQ(goalID), integrationbinding.GenerationEQ(waitRow.Generation)).Exec(ctx); err != nil {
					return err
				}
				generation++
			}
			if err = t.client.Wait.UpdateOneID(waitRow.ID).SetRunID(workflowRun.ID).SetGeneration(generation).SetCondition(jsonObject(condition)).SetArmedAt(t.now).ClearSatisfiedAt().ClearEventID().ClearClosedAt().Exec(ctx); err != nil {
				return err
			}
			if err = t.client.WorkflowStepRun.Update().Where(workflowsteprun.RunIDEQ(workflowRun.ID), workflowsteprun.StepKeyEQ(current.Key)).SetState("waiting").SetStartedAt(t.now).Exec(ctx); err != nil {
				return err
			}
			if err = t.client.Run.UpdateOneID(workflowRun.ID).SetState("waiting").Exec(ctx); err != nil {
				return err
			}
			if err = t.client.Goal.UpdateOneID(goalID).SetWaitingReason("workflow_event").Exec(ctx); err != nil {
				return err
			}
			if err = transition(ctx, t, goalID, "WAITING"); err != nil {
				return err
			}
			return s.bindWorkflowWait(ctx, t, goalID, generation, current)
		case "subflow":
			return s.startChildWorkflow(ctx, t, goalID, workflowRun, current)
		default:
			return conflict("Unsupported workflow step")
		}
	}
}

func (s *Store) completeWorkflowRun(ctx context.Context, t *transaction, goalID string, workflowRun *ent.Run) error {
	if err := t.client.Run.UpdateOneID(workflowRun.ID).SetState("succeeded").SetEndedAt(t.now).SetUpdatedAt(t.now).Exec(ctx); err != nil {
		return err
	}
	if workflowRun.ParentRunID == nil {
		return transition(ctx, t, goalID, "COMPLETED")
	}
	return s.finishChildWorkflow(ctx, t, goalID, workflowRun, true)
}

func (s *Store) finishChildWorkflow(ctx context.Context, t *transaction, goalID string, child *ent.Run, succeeded bool) error {
	if child.ParentRunID == nil {
		if succeeded {
			return transition(ctx, t, goalID, "COMPLETED")
		}
		return transition(ctx, t, goalID, "FAILED")
	}
	parent, err := t.client.Run.Get(ctx, *child.ParentRunID)
	if err != nil {
		return err
	}
	if parent.CurrentStepKey == nil || parent.WorkflowVersionID == nil {
		return conflict("Child workflow parent is missing its invoking step")
	}
	stepState, outcome := "failed", "failure"
	if succeeded {
		stepState, outcome = "succeeded", "success"
	}
	if err = t.client.WorkflowStepRun.Update().Where(workflowsteprun.RunIDEQ(parent.ID), workflowsteprun.StepKeyEQ(*parent.CurrentStepKey), workflowsteprun.StepTypeEQ("subflow"), workflowsteprun.StateEQ("waiting")).SetState(stepState).SetEndedAt(t.now).Exec(ctx); err != nil {
		return err
	}
	version, err := t.client.WorkflowVersion.Get(ctx, *parent.WorkflowVersionID)
	if err != nil {
		return err
	}
	spec, err := decodeSpec(version.Spec)
	if err != nil {
		return err
	}
	next, ok := nextWorkflowStep(spec, *parent.CurrentStepKey, outcome)
	if !ok {
		return s.failWorkflowRun(ctx, t, goalID, parent)
	}
	if err = t.client.Run.UpdateOneID(parent.ID).SetState("pending").SetUpdatedAt(t.now).Exec(ctx); err != nil {
		return err
	}
	return s.advanceWorkflowDeterministic(ctx, t, goalID, parent, spec, next)
}

func (s *Store) failWorkflowRun(ctx context.Context, t *transaction, goalID string, workflowRun *ent.Run) error {
	if err := t.client.Run.UpdateOneID(workflowRun.ID).SetState("failed").SetEndedAt(t.now).SetUpdatedAt(t.now).Exec(ctx); err != nil {
		return err
	}
	if workflowRun.ParentRunID == nil {
		return transition(ctx, t, goalID, "FAILED")
	}
	return s.finishChildWorkflow(ctx, t, goalID, workflowRun, false)
}

func firstWorkflowAgent(spec WorkflowSpec) WorkflowStep {
	for _, step := range spec.Steps {
		if step.Type == "agent" {
			return step
		}
	}
	return WorkflowStep{}
}

func (s *Store) startChildWorkflow(ctx context.Context, t *transaction, goalID string, parent *ent.Run, invoking WorkflowStep) error {
	childDefinitionID := subflowDefinitionID(invoking.Config)
	if !ValidID(childDefinitionID) {
		return conflict("Subflow step has an invalid workflow definition")
	}
	var err error
	depth := 1
	for ancestor := parent; ancestor.ParentRunID != nil; {
		depth++
		if depth > maxWorkflowSubflowDepth {
			return conflict("Workflow subflow nesting exceeds the safety limit")
		}
		ancestor, err = t.client.Run.Get(ctx, *ancestor.ParentRunID)
		if err != nil {
			return err
		}
	}
	definition, err := t.client.WorkflowDefinition.Query().Where(workflowdefinition.IDEQ(childDefinitionID), workflowdefinition.OrganizationEQ(s.Organization), workflowdefinition.StateEQ("published")).Only(ctx)
	if ent.IsNotFound(err) {
		return conflict("Subflow workflow is not published")
	}
	if err != nil {
		return err
	}
	versionID, _ := invoking.Config["workflow_version_id"].(string)
	versionQuery := t.client.WorkflowVersion.Query().Where(workflowversion.DefinitionIDEQ(definition.ID), workflowversion.OrganizationEQ(s.Organization))
	if ValidID(versionID) {
		versionQuery = versionQuery.Where(workflowversion.IDEQ(versionID))
	} else {
		versionQuery = versionQuery.Where(workflowversion.VersionEQ(definition.LatestVersion))
	}
	version, err := versionQuery.Only(ctx)
	if err != nil {
		return err
	}
	spec, err := decodeSpec(version.Spec)
	if err != nil {
		return err
	}
	entry := workflowEntry(spec)
	var entryStep WorkflowStep
	for _, step := range spec.Steps {
		if step.Key == entry {
			entryStep = step
			break
		}
	}
	agent := firstWorkflowAgent(spec)
	runtime, _ := agent.Config["runtime"].(string)
	if runtime == "" {
		runtime = "demo"
	}
	workerID, _ := agent.Config["worker_id"].(string)
	if workerID == "" {
		workerID = "demo-local"
	}
	if runtime == "codex-container" && !s.EnableCodex {
		return conflict("Contained runtime is disabled")
	}
	if runtime != "demo" {
		w, lookupErr := t.client.Worker.Query().Where(worker.IDEQ(workerID), worker.RuntimeEQ(runtime), worker.OrganizationEQ(s.Organization), worker.RevokedAtIsNil()).Only(ctx)
		if ent.IsNotFound(lookupErr) {
			return conflict("Subflow agent is unavailable")
		}
		if lookupErr != nil {
			return lookupErr
		}
		workerID = w.ID
	}
	if err = t.client.WorkflowStepRun.Update().Where(workflowsteprun.RunIDEQ(parent.ID), workflowsteprun.StepKeyEQ(invoking.Key), workflowsteprun.StateIn("pending", "running")).SetState("waiting").SetStartedAt(t.now).Exec(ctx); err != nil {
		return err
	}
	if err = t.client.Run.UpdateOneID(parent.ID).SetState("waiting").SetUpdatedAt(t.now).Exec(ctx); err != nil {
		return err
	}
	childID := ID()
	childState := "pending"
	if entryStep.Type == "wait_event" {
		childState = "waiting"
	} else if entryStep.Type == "complete" {
		childState = "succeeded"
	}
	create := t.client.Run.Create().SetID(childID).SetGoalID(goalID).SetPolicy(jsonObject(Policy{Runtime: runtime, MaxAttempts: 100, Lifecycle: "workflow-v1"})).SetContext(parent.Context).SetWorkflowDefinitionID(definition.ID).SetWorkflowVersionID(version.ID).SetParentRunID(parent.ID).SetInvokingStepKey(invoking.Key).SetState(childState).SetCurrentStepKey(entry).SetCreatedAt(t.now).SetUpdatedAt(t.now)
	if childState == "succeeded" {
		create.SetEndedAt(t.now)
	}
	if err = create.Exec(ctx); err != nil {
		return err
	}
	if err = t.client.Session.Create().SetRunID(childID).SetWorkerID(workerID).SetRuntime(runtime).Exec(ctx); err != nil {
		return err
	}
	for position, step := range spec.Steps {
		state := "pending"
		if step.Key == entry {
			if step.Type == "wait_event" {
				state = "waiting"
			} else if step.Type == "complete" {
				state = "succeeded"
			}
		}
		if err = t.client.WorkflowStepRun.Create().SetRunID(childID).SetStepKey(step.Key).SetStepType(step.Type).SetPosition(position).SetState(state).SetInput(map[string]any{}).SetOutput(map[string]any{}).Exec(ctx); err != nil {
			return err
		}
	}
	if entryStep.Type == "wait_event" {
		waitRow, waitErr := t.client.Wait.Query().Where(wait.GoalIDEQ(goalID)).Only(ctx)
		if waitErr != nil {
			return waitErr
		}
		condition, ok := conditionFromConfig(entryStep.Config)
		if !ok {
			return conflict("Child wait step has no valid event condition")
		}
		generation := waitRow.Generation
		if waitRow.ClosedAt != nil {
			if err = t.client.WaitHistory.Create().SetID(ID()).SetGoalID(waitRow.GoalID).SetNillableRunID(waitRow.RunID).SetGeneration(waitRow.Generation).SetCondition(waitRow.Condition).SetNillableArmedAt(waitRow.ArmedAt).SetNillableSatisfiedAt(waitRow.SatisfiedAt).SetNillableEventID(waitRow.EventID).SetNillableClosedAt(waitRow.ClosedAt).SetNillablePreparedByAttempt(waitRow.PreparedByAttempt).Exec(ctx); err != nil {
				return err
			}
			if _, err = t.client.IntegrationBinding.Delete().Where(integrationbinding.GoalIDEQ(goalID), integrationbinding.GenerationEQ(waitRow.Generation)).Exec(ctx); err != nil {
				return err
			}
			generation++
		}
		if err = t.client.Wait.UpdateOneID(waitRow.ID).SetRunID(childID).SetGeneration(generation).SetCondition(jsonObject(condition)).SetArmedAt(t.now).ClearSatisfiedAt().ClearEventID().ClearClosedAt().Exec(ctx); err != nil {
			return err
		}
		if err = s.bindWorkflowWait(ctx, t, goalID, generation, entryStep); err != nil {
			return err
		}
		currentWait, waitErr := t.client.Wait.Get(ctx, waitRow.ID)
		if waitErr != nil {
			return waitErr
		}
		if currentWait.SatisfiedAt != nil {
			return nil
		}
		if err = t.client.Goal.UpdateOneID(goalID).SetWaitingReason("workflow_event").Exec(ctx); err != nil {
			return err
		}
		return transition(ctx, t, goalID, "WAITING")
	}
	if entryStep.Type == "complete" {
		child, getErr := t.client.Run.Get(ctx, childID)
		if getErr != nil {
			return getErr
		}
		return s.completeWorkflowRun(ctx, t, goalID, child)
	}
	if entryStep.Type == "condition" {
		child, getErr := t.client.Run.Get(ctx, childID)
		if getErr != nil {
			return getErr
		}
		return s.advanceWorkflowDeterministic(ctx, t, goalID, child, spec, entryStep)
	}
	if entryStep.Type == "subflow" {
		child, getErr := t.client.Run.Get(ctx, childID)
		if getErr != nil {
			return getErr
		}
		return s.startChildWorkflow(ctx, t, goalID, child, entryStep)
	}
	if entryStep.Type != "agent" {
		return conflict("Child workflow entry step is unsupported")
	}
	if err = t.client.Goal.UpdateOneID(goalID).SetWaitingReason("child_workflow").Exec(ctx); err != nil {
		return err
	}
	if err = transition(ctx, t, goalID, "READY"); err != nil {
		return err
	}
	return enqueueRun(ctx, t, goalID, childID, "agent_step_ready")
}

// bindWorkflowWait connects an exact wait to one operator-configured plugin
// instance. The plugin remains an evidence source; this binding only lets the
// Loom workflow consume a matching verified receipt.
func (s *Store) bindWorkflowWait(ctx context.Context, t *transaction, goalID string, generation int, step WorkflowStep) error {
	instanceID := integrationInstanceIDFromConfig(step.Config)
	if instanceID == "" {
		return nil
	}
	condition, ok := conditionFromConfig(step.Config)
	if !ok {
		return conflict("Wait step has no valid event condition")
	}
	instance, err := t.client.IntegrationInstance.Query().Where(integrationinstance.IDEQ(instanceID), integrationinstance.OrganizationEQ(s.Organization), integrationinstance.PluginIDEQ(condition.Source), integrationinstance.StateEQ("active")).Only(ctx)
	if ent.IsNotFound(err) {
		return conflict("Workflow wait connection is unavailable")
	}
	if err != nil {
		return err
	}
	if instance.RoutingIdentity == "" {
		return conflict("Workflow wait connection has no routing identity")
	}
	if err = integrationLock(ctx, t, s.Organization, condition.Source, instance.RoutingIdentity); err != nil {
		return err
	}
	err = t.client.IntegrationBinding.Create().SetGoalID(goalID).SetGeneration(generation).SetOrganization(s.Organization).SetSource(condition.Source).SetInstance(instance.RoutingIdentity).SetEventType(condition.Type).SetResource(condition.Resource).SetVersion(condition.Version).SetAttributes(map[string]any{"workflow_step_key": step.Key, "integration_instance_id": instance.ID}).Exec(ctx)
	if ent.IsConstraintError(err) {
		return conflict("This external event is already assigned to another active wait")
	}
	if err != nil {
		return err
	}
	if err = audit(ctx, t, goalID, "workflow_wait_bound", map[string]any{"step_key": step.Key, "source": condition.Source, "generation": generation, "integration_instance_id": instance.ID}); err != nil {
		return err
	}
	pending, err := t.client.IntegrationDelivery.Query().Where(integrationdelivery.OrganizationEQ(s.Organization), integrationdelivery.SourceEQ(condition.Source), integrationdelivery.InstanceEQ(instance.RoutingIdentity), integrationdelivery.DispositionEQ("ignored")).Order(ent.Asc(integrationdelivery.FieldReceivedAt), ent.Asc(integrationdelivery.FieldID)).All(ctx)
	if err != nil {
		return err
	}
	for _, receipt := range pending {
		stored, ok := receiptCondition(receipt)
		if !ok || stored != condition {
			continue
		}
		if _, err = s.routeReceipt(ctx, t, receipt); err != nil {
			return err
		}
	}
	return nil
}

func receiptCondition(receipt *ent.IntegrationDelivery) (Condition, bool) {
	if receipt.Condition == nil {
		return Condition{}, false
	}
	body, err := json.Marshal(receipt.Condition)
	var condition Condition
	if err != nil || json.Unmarshal(body, &condition) != nil || condition.Validate() != nil {
		return Condition{}, false
	}
	return condition, true
}

func WorkflowJSONSchema() map[string]any {
	conditionSchema := map[string]any{
		"type":     "object",
		"required": []string{"source", "type", "resource", "version"},
		"properties": map[string]any{
			"source":   map[string]any{"type": "string", "minLength": 1, "maxLength": 80},
			"type":     map[string]any{"type": "string", "minLength": 1, "maxLength": 120},
			"resource": map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
			"version":  map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
		},
		"additionalProperties": false,
	}
	stepIdentity := map[string]any{
		"key":  map[string]any{"type": "string", "pattern": workflowKeyPattern.String()},
		"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 120},
	}
	typedStep := func(stepType string, config map[string]any) map[string]any {
		properties := map[string]any{}
		for key, value := range stepIdentity {
			properties[key] = value
		}
		properties["type"] = map[string]any{"const": stepType}
		properties["config"] = config
		return map[string]any{
			"type":                 "object",
			"required":             []string{"key", "name", "type", "config"},
			"properties":           properties,
			"additionalProperties": false,
		}
	}
	agentConfig := map[string]any{
		"type":     "object",
		"required": []string{"runtime"},
		"properties": map[string]any{
			"runtime":   map[string]any{"enum": []string{"demo", "remote-demo", "codex-container"}},
			"worker_id": map[string]any{"type": "string", "format": "uuid"},
		},
		"additionalProperties": false,
	}
	waitConfig := map[string]any{
		"type":     "object",
		"required": []string{"condition"},
		"properties": map[string]any{
			"integration_instance_id": map[string]any{"type": "string", "format": "uuid"},
			"condition":               conditionSchema,
		},
		"additionalProperties": false,
	}
	conditionConfig := map[string]any{
		"type":     "object",
		"required": []string{"path"},
		"properties": map[string]any{
			"path":       map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
			"equals":     map[string]any{},
			"not_equals": map[string]any{},
			"exists":     map[string]any{"type": "boolean"},
		},
		"oneOf": []any{
			map[string]any{"required": []string{"equals"}},
			map[string]any{"required": []string{"not_equals"}},
			map[string]any{"required": []string{"exists"}},
		},
		"additionalProperties": false,
	}
	subflowConfig := map[string]any{
		"type":     "object",
		"required": []string{"workflow_definition_id"},
		"properties": map[string]any{
			"workflow_definition_id": map[string]any{"type": "string", "format": "uuid"},
			"workflow_version_id":    map[string]any{"type": "string", "format": "uuid"},
		},
		"additionalProperties": false,
	}
	completeConfig := map[string]any{"type": "object", "maxProperties": 0, "additionalProperties": false}
	return map[string]any{
		"$schema":  "https://json-schema.org/draft/2020-12/schema",
		"title":    "Loom Workflow Specification",
		"type":     "object",
		"required": []string{"schema_version", "triggers", "steps", "edges"},
		"properties": map[string]any{
			"schema_version": map[string]any{"const": WorkflowSchemaVersion},
			"triggers": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{
				"type": "object", "required": []string{"type"}, "properties": map[string]any{
					"type": map[string]any{"enum": []string{"manual", "integration_event"}}, "integration_instance_id": map[string]any{"type": "string", "format": "uuid"}, "source": map[string]any{"type": "string"}, "event_type": map[string]any{"type": "string"}, "resource": map[string]any{"type": "string"}, "version": map[string]any{"type": "string"},
				}, "additionalProperties": false,
			}},
			"steps": map[string]any{
				"type":     "array",
				"minItems": 2,
				"maxItems": 50,
				"items": map[string]any{"oneOf": []any{
					typedStep("agent", agentConfig),
					typedStep("wait_event", waitConfig),
					typedStep("condition", conditionConfig),
					typedStep("subflow", subflowConfig),
					typedStep("complete", completeConfig),
				}},
			},
			"edges": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "required": []string{"from", "to", "outcome"}, "properties": map[string]any{"from": map[string]any{"type": "string"}, "to": map[string]any{"type": "string"}, "outcome": map[string]any{"enum": []string{"success", "failure"}}}, "additionalProperties": false,
			}},
		},
		"additionalProperties": false,
	}
}

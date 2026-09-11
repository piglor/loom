package control

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/piglor/loom/services/loom/ent"
	"github.com/piglor/loom/services/loom/ent/goal"
	"github.com/piglor/loom/services/loom/ent/integrationbinding"
	"github.com/piglor/loom/services/loom/ent/integrationdelivery"
	"github.com/piglor/loom/services/loom/ent/wait"
)

// Binding is an operator-authorized resource association for one wait
// generation. Attributes are plugin validation data, not executable policy.
type Binding struct {
	GoalID     string         `json:"goal_id"`
	Generation int            `json:"generation"`
	Instance   string         `json:"instance"`
	Condition  Condition      `json:"condition"`
	Attributes map[string]any `json:"attributes"`
}

// IntegrationReceipt must come from an authenticated, authorizing adapter.
// A signature alone is insufficient: the adapter also checks task ownership,
// actor authority and provider-specific freshness before supplying Condition.
// Nil Condition retains an unnormalized receipt without allowing a wake-up.
type IntegrationReceipt struct {
	Source     string         `json:"source"`
	Instance   string         `json:"instance"`
	DeliveryID string         `json:"delivery_id"`
	Condition  *Condition     `json:"condition"`
	Details    map[string]any `json:"details"`
	RawBody    []byte         `json:"raw_body"`
}

// Authorization identifiers belong in strings. Bound numeric attributes are
// restricted to exact JSON safe integers so Ent's map decoder cannot collapse
// two different provider identifiers through float64 rounding.
func validateAttributeNumbers(v any) error {
	switch value := v.(type) {
	case json.Number:
		n, err := strconv.ParseInt(string(value), 10, 64)
		if err != nil || n < -9007199254740991 || n > 9007199254740991 {
			return invalid("Use strings for attribute identifiers; numbers must be safe integers")
		}
	case map[string]any:
		for _, item := range value {
			if err := validateAttributeNumbers(item); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range value {
			if err := validateAttributeNumbers(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func integrationLock(ctx context.Context, tx *transaction, organization, source, instance string) error {
	key, _ := canonicalJSON([]string{"integration", organization, source, instance}, false)
	// Serialize receipt registration and binding/reconciliation within one
	// integration instance. Independent tenants/instances remain concurrent.
	_, err := tx.sql.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", string(key))
	return err
}

func (s *Store) Bind(ctx context.Context, b Binding) error {
	if err := b.Condition.Validate(); err != nil {
		return err
	}
	if !ValidID(b.GoalID) || b.Generation < 1 || !textBetween(b.Instance, 256) {
		return invalid("Invalid resource binding")
	}
	b.GoalID = strings.ToLower(b.GoalID)
	attrs, err := json.Marshal(b.Attributes)
	if err != nil || len(attrs) > 65536 {
		return invalid("Invalid binding attributes")
	}
	if b.Attributes == nil {
		b.Attributes = map[string]any{}
	} else {
		var exact any
		decoder := json.NewDecoder(bytes.NewReader(attrs))
		decoder.UseNumber()
		if err = decoder.Decode(&exact); err != nil {
			return err
		}
		if err = validateAttributeNumbers(exact); err != nil {
			return err
		}
		if err = json.Unmarshal(attrs, &b.Attributes); err != nil {
			return err
		}
	}
	return s.tx(ctx, func(tx *transaction) error {
		if err := integrationLock(ctx, tx, s.Organization, b.Condition.Source, b.Instance); err != nil {
			return err
		}
		pending, err := tx.client.IntegrationDelivery.Query().Where(integrationdelivery.OrganizationEQ(s.Organization), integrationdelivery.SourceEQ(b.Condition.Source), integrationdelivery.InstanceEQ(b.Instance), integrationdelivery.DispositionEQ("ignored"), func(q *entsql.Selector) {
			q.Where(entsql.And(sqljson.ValueEQ("condition", b.Condition.Type, sqljson.Path("type")), sqljson.ValueEQ("condition", b.Condition.Resource, sqljson.Path("resource")), sqljson.ValueEQ("condition", b.Condition.Version, sqljson.Path("version"))))
		}).Order(ent.Asc(integrationdelivery.FieldReceivedAt), ent.Asc(integrationdelivery.FieldID)).All(ctx)
		if err != nil {
			return err
		}
		// Acquire every replay delivery lock before the Goal lock. This preserves
		// delivery -> Goal ordering while serializing admission with cancellation
		// and generation changes made by non-integration domain writers.
		for _, receipt := range pending {
			if err = eventLock(ctx, tx, s.Organization, receipt.Source, receipt.ID); err != nil {
				return err
			}
		}
		g, err := tx.client.Goal.Query().Where(goal.IDEQ(b.GoalID), goal.OrganizationEQ(s.Organization)).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if terminal(g.State) {
			return conflict("Goal is inactive")
		}
		w, err := tx.client.Wait.Query().Where(wait.GoalIDEQ(g.ID)).Only(ctx)
		if err != nil {
			return err
		}
		if w.Generation != b.Generation || !reflect.DeepEqual(w.Condition, jsonObject(b.Condition)) {
			return conflict("Binding must match the authorized wait")
		}
		old, err := tx.client.IntegrationBinding.Query().Where(integrationbinding.GoalIDEQ(g.ID), integrationbinding.GenerationEQ(b.Generation)).Only(ctx)
		if err == nil {
			if old.Organization != s.Organization || old.Source != b.Condition.Source || old.Instance != b.Instance || old.EventType != b.Condition.Type || old.Resource != b.Condition.Resource || old.Version != b.Condition.Version || !reflect.DeepEqual(old.Attributes, b.Attributes) {
				return conflict("Wait binding is immutable")
			}
		} else if !ent.IsNotFound(err) {
			return err
		} else {
			err = tx.client.IntegrationBinding.Create().SetGoalID(g.ID).SetGeneration(b.Generation).SetOrganization(s.Organization).SetSource(b.Condition.Source).SetInstance(b.Instance).SetEventType(b.Condition.Type).SetResource(b.Condition.Resource).SetVersion(b.Condition.Version).SetAttributes(b.Attributes).Exec(ctx)
			if ent.IsConstraintError(err) {
				return conflict("External resource already has an owner")
			}
			if err != nil {
				return err
			}
			if err = audit(ctx, tx, g.ID, "resource_bound", map[string]any{"source": b.Condition.Source, "instance": b.Instance, "generation": b.Generation}); err != nil {
				return err
			}
		}
		for _, receipt := range pending {
			if _, err = s.routeReceipt(ctx, tx, receipt); err != nil {
				return err
			}
		}
		return tx.client.IntegrationBinding.Update().Where(integrationbinding.GoalIDEQ(g.ID), integrationbinding.GenerationEQ(b.Generation)).SetReconciledAt(tx.now).Exec(ctx)
	})
}

// ReceiveIntegration records an authorized adapter receipt and resolves its
// Goal through persisted bindings, never through model inference or payload
// instructions. It cannot create an execution attempt or mark a Goal complete.
func (s *Store) ReceiveIntegration(ctx context.Context, r IntegrationReceipt) (EventResult, error) {
	if !textBetween(r.Source, 80) || !textBetween(r.Instance, 256) || !textBetween(r.DeliveryID, 128) || len(r.RawBody) > 1048576 {
		return EventResult{}, invalid("Invalid integration receipt")
	}
	if r.Condition != nil {
		if err := r.Condition.Validate(); err != nil {
			return EventResult{}, err
		}
		if r.Condition.Source != r.Source {
			return EventResult{}, invalid("Receipt source mismatch")
		}
	}
	if r.Details == nil {
		r.Details = map[string]any{}
	}
	content, err := json.Marshal(r)
	if err != nil || len(content) > 2097152 {
		return EventResult{}, invalid("Invalid receipt details")
	}
	digest := hash(r.RawBody)
	fingerprint := hash(content)
	var result EventResult
	err = s.tx(ctx, func(tx *transaction) error {
		if err := integrationLock(ctx, tx, s.Organization, r.Source, r.Instance); err != nil {
			return err
		}
		old, err := tx.client.IntegrationDelivery.Query().Where(integrationdelivery.OrganizationEQ(s.Organization), integrationdelivery.SourceEQ(r.Source), integrationdelivery.InstanceEQ(r.Instance), integrationdelivery.DeliveryIDEQ(r.DeliveryID)).Only(ctx)
		if err == nil {
			if old.Digest != digest {
				return conflict("Delivery ID reused with different content")
			}
			if old.Fingerprint != nil && *old.Fingerprint != fingerprint {
				if old.Condition != nil && r.Condition == nil {
					result = EventResult{old.ID, "duplicate"}
					return nil
				}
				if !(old.Disposition == "ignored" && old.Condition == nil && r.Condition != nil) {
					return conflict("Delivery authorization changed incompatibly")
				}
			}
			if old.Disposition == "ignored" && old.Condition == nil && r.Condition != nil {
				updated, err := tx.client.IntegrationDelivery.UpdateOneID(old.ID).SetCondition(jsonObject(*r.Condition)).SetDetails(r.Details).SetFingerprint(fingerprint).Save(ctx)
				if err != nil {
					return err
				}
				result, err = s.routeReceipt(ctx, tx, updated)
				return err
			}
			if old.Fingerprint == nil && old.Disposition == "ignored" && r.Condition != nil {
				// Revalidation is supplied by the authorizing adapter, not inferred
				// from retained legacy bytes. Preserve original details/raw evidence.
				if old.Condition != nil && !reflect.DeepEqual(old.Condition, jsonObject(*r.Condition)) {
					return conflict("Legacy receipt condition changed")
				}
				updated, err := tx.client.IntegrationDelivery.UpdateOneID(old.ID).SetCondition(jsonObject(*r.Condition)).SetFingerprint(fingerprint).Save(ctx)
				if err != nil {
					return err
				}
				result, err = s.routeReceipt(ctx, tx, updated)
				return err
			}
			result = EventResult{old.ID, "duplicate"}
			return nil
		}
		if !ent.IsNotFound(err) {
			return err
		}
		q := tx.client.IntegrationDelivery.Create().SetOrganization(s.Organization).SetSource(r.Source).SetInstance(r.Instance).SetDeliveryID(r.DeliveryID).SetDigest(digest).SetFingerprint(fingerprint).SetDetails(r.Details).SetRawBody(r.RawBody).SetDisposition("ignored").SetReceivedAt(tx.now)
		if r.Condition != nil {
			q.SetCondition(jsonObject(*r.Condition))
		}
		receipt, err := q.Save(ctx)
		if err != nil {
			return err
		}
		result, err = s.routeReceipt(ctx, tx, receipt)
		return err
	})
	if err != nil {
		return EventResult{}, err
	}
	return result, nil
}

func (s *Store) routeReceipt(ctx context.Context, tx *transaction, r *ent.IntegrationDelivery) (EventResult, error) {
	result := EventResult{r.ID, "ignored"}
	if r.Condition == nil {
		return result, nil
	}
	var c Condition
	body, err := json.Marshal(r.Condition)
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(body, &c); err != nil {
		return result, err
	}
	if err = c.Validate(); err != nil || c.Source != r.Source {
		return result, invalid("Invalid stored receipt condition")
	}
	b, err := tx.client.IntegrationBinding.Query().Where(integrationbinding.OrganizationEQ(s.Organization), integrationbinding.SourceEQ(r.Source), integrationbinding.InstanceEQ(r.Instance), integrationbinding.EventTypeEQ(c.Type), integrationbinding.ResourceEQ(c.Resource), integrationbinding.VersionEQ(c.Version)).Only(ctx)
	if ent.IsNotFound(err) {
		started, startErr := s.routeWorkflowTriggers(ctx, tx, r, c)
		if startErr != nil {
			return result, startErr
		}
		if started > 0 {
			result.Disposition = "workflow_started"
			return result, tx.client.IntegrationDelivery.UpdateOneID(r.ID).SetDisposition(result.Disposition).Exec(ctx)
		}
		return result, nil
	}
	if err != nil {
		return result, err
	}
	e, err := s.receive(ctx, tx, Event{Condition: c, GoalID: b.GoalID, Generation: b.Generation, DeliveryID: r.ID})
	if err != nil {
		return result, err
	}
	result.Disposition = e.Disposition
	return result, tx.client.IntegrationDelivery.UpdateOneID(r.ID).SetDisposition(result.Disposition).Exec(ctx)
}

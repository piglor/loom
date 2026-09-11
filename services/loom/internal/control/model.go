// Package control owns transactional Goal state, independent of runtime and integration adapters.
package control

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Fault struct {
	Status  int
	Message string
}

func (e *Fault) Error() string      { return e.Message }
func conflict(message string) error { return &Fault{409, message} }
func invalid(message string) error  { return &Fault{422, message} }
func notFound() error               { return &Fault{404, "Resource not found"} }
func unauthorized() error           { return &Fault{401, "Invalid worker credential"} }
func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func ValidID(s string) bool {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(s[:8] + s[9:13] + s[14:18] + s[19:23] + s[24:])
	return err == nil
}
func textBetween(s string, max int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) > 0 && utf8.RuneCountInString(s) <= max
}

type Condition struct {
	Source   string `json:"source"`
	Type     string `json:"type"`
	Resource string `json:"resource"`
	Version  string `json:"version"`
}

func (c Condition) Validate() error {
	if !textBetween(c.Source, 80) || !textBetween(c.Type, 120) || !textBetween(c.Resource, 256) || !textBetween(c.Version, 128) {
		return invalid("Invalid event condition")
	}
	return nil
}

type CreateGoal struct {
	Title               string     `json:"title"`
	Objective           string     `json:"objective"`
	Condition           Condition  `json:"condition"`
	Runtime             string     `json:"runtime"`
	WorkerID            *string    `json:"worker_id"`
	CompletionCondition *Condition `json:"completion_condition"`
	MaxAttempts         *int       `json:"max_attempts"`
}

func (r *CreateGoal) Validate() error {
	if r.Runtime == "" {
		r.Runtime = "demo"
	}
	if !textBetween(r.Title, 200) || !textBetween(r.Objective, 8000) {
		return invalid("Title and objective are required")
	}
	if err := r.Condition.Validate(); err != nil {
		return err
	}
	if r.CompletionCondition != nil {
		if err := r.CompletionCondition.Validate(); err != nil {
			return err
		}
	}
	if r.Runtime != "demo" && r.Runtime != "remote-demo" && r.Runtime != "codex-container" {
		return invalid("Unsupported runtime")
	}
	if (r.Runtime != "demo") != (r.WorkerID != nil) {
		return invalid("Remote runtime requires worker binding")
	}
	if r.WorkerID != nil && !ValidID(*r.WorkerID) {
		return invalid("Invalid worker ID")
	}
	if r.WorkerID != nil {
		id := strings.ToLower(*r.WorkerID)
		r.WorkerID = &id
	}
	if r.MaxAttempts != nil && (*r.MaxAttempts < 2 || *r.MaxAttempts > 1000) {
		return invalid("Attempt budget must be between 2 and 1000")
	}
	if r.Runtime == "codex-container" && r.CompletionCondition == nil {
		return invalid("Codex requires explicit completion criteria")
	}
	return nil
}

type Event struct {
	Condition
	DeliveryID string         `json:"delivery_id"`
	GoalID     string         `json:"goal_id"`
	Generation int            `json:"generation"`
	Details    map[string]any `json:"-"`
}

func (e *Event) Validate() error {
	if err := e.Condition.Validate(); err != nil {
		return err
	}
	if !ValidID(e.GoalID) || e.Generation < 1 || !textBetween(e.DeliveryID, 128) {
		return invalid("Invalid event identity")
	}
	e.GoalID = strings.ToLower(e.GoalID)
	return nil
}

type Policy struct {
	Runtime     string `json:"runtime"`
	MaxAttempts int    `json:"max_attempts"`
	Lifecycle   string `json:"lifecycle,omitempty"`
}
type Goal struct {
	ID            string  `json:"id"`
	State         string  `json:"state"`
	Phase         int     `json:"phase"`
	Objective     string  `json:"objective"`
	WaitingReason *string `json:"waiting_reason"`
	Completion    struct {
		EventMatches Condition `json:"event_matches"`
	} `json:"completion_criteria"`
}
type Wait struct {
	ID          string    `json:"id"`
	GoalID      string    `json:"goal_id"`
	Generation  int       `json:"generation"`
	Condition   Condition `json:"condition"`
	ArmedAt     *string   `json:"armed_at"`
	SatisfiedAt *string   `json:"satisfied_at"`
	ClosedAt    *string   `json:"closed_at"`
	EventID     *string   `json:"event_id"`
	PreparedBy  *string   `json:"prepared_by_attempt"`
}
type Session struct {
	ID         string  `json:"id"`
	RunID      string  `json:"run_id"`
	WorkerID   string  `json:"worker_id"`
	Runtime    string  `json:"runtime"`
	ProviderID *string `json:"provider_session_id"`
}
type Attempt struct {
	ID        string   `json:"id"`
	GoalID    string   `json:"goal_id"`
	SessionID string   `json:"session_id"`
	WorkerID  string   `json:"worker_id"`
	Phase     int      `json:"phase"`
	State     string   `json:"state"`
	Duration  *float64 `json:"duration_ms"`
	Outcome   *string  `json:"outcome"`
}
type Enrollment struct {
	Workspace string            `json:"workspace_ref"`
	Labels    map[string]string `json:"labels"`
	Protocol  int               `json:"protocol_version"`
	Runtime   string            `json:"runtime"`
}

var workspacePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

func (r *Enrollment) Validate() error {
	if r.Protocol == 0 {
		r.Protocol = 1
	}
	if r.Runtime == "" {
		r.Runtime = "remote-demo"
	}
	if r.Labels == nil {
		r.Labels = map[string]string{}
	}
	if !workspacePattern.MatchString(r.Workspace) || len(r.Labels) > 20 || (r.Protocol != 1 && r.Protocol != 2) || (r.Runtime != "remote-demo" && r.Runtime != "codex-container") || (r.Runtime == "codex-container" && r.Protocol != 2) {
		return invalid("Invalid worker enrollment")
	}
	return nil
}

type Claim struct {
	Protocol int    `json:"protocol_version"`
	ClaimID  string `json:"claim_id"`
}

func (r *Claim) Validate() error {
	if r.Protocol == 0 {
		r.Protocol = 1
	}
	if (r.Protocol != 1 && r.Protocol != 2) || !ValidID(r.ClaimID) {
		return invalid("Invalid execution claim")
	}
	r.ClaimID = strings.ToLower(r.ClaimID)
	return nil
}

type Stop struct {
	Claim
	SessionID  string  `json:"session_id"`
	Duration   float64 `json:"duration_ms"`
	Success    bool    `json:"success"`
	ProviderID *string `json:"provider_session_id,omitempty"`
	Outcome    *string `json:"outcome,omitempty"`
}

func validProvider(s *string) bool {
	if s == nil {
		return true
	}
	if !textBetween(*s, 256) {
		return false
	}
	for _, r := range *s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func (r *Stop) Validate() error {
	if err := r.Claim.Validate(); err != nil {
		return err
	}
	if !ValidID(r.SessionID) || r.Duration < 0 || r.Duration > 86400000 || math.IsNaN(r.Duration) || math.IsInf(r.Duration, 0) || !validProvider(r.ProviderID) {
		return invalid("Invalid stop receipt")
	}
	r.SessionID = strings.ToLower(r.SessionID)
	if r.Outcome != nil && (r.Protocol != 2 || (*r.Outcome != "yield" && *r.Outcome != "complete" && *r.Outcome != "blocked")) {
		return invalid("Invalid versioned outcome")
	}
	return nil
}

type BindSession struct {
	Claim
	SessionID  string `json:"session_id"`
	ProviderID string `json:"provider_session_id"`
}
type PrepareWait struct {
	Claim
	SessionID  string    `json:"session_id"`
	Generation int       `json:"expected_generation"`
	Condition  Condition `json:"condition"`
}

func terminal(state string) bool {
	return state == "COMPLETED" || state == "FAILED" || state == "CANCELLED" || state == "BLOCKED"
}
func str(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// Package github authenticates and normalizes GitHub workflow events before
// handing them to Loom's provider-neutral integration inbox.
package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/piglor/loom/services/loom/internal/control"
)

const maxBody = 1 << 20

var (
	shaPattern  = regexp.MustCompile(`^[a-f0-9]{40}$`)
	repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)
)

type BindingRequest struct {
	GoalID        string `json:"goal_id"`
	Generation    int    `json:"generation"`
	Installation  int64  `json:"installation_id"`
	Repository    int64  `json:"repository_id"`
	RepositoryRef string `json:"repository_full_name"`
	PullRequest   int64  `json:"pull_request"`
	HeadSHA       string `json:"head_sha"`
	RunID         int64  `json:"run_id"`
	RunAttempt    int64  `json:"run_attempt"`
	WorkflowID    int64  `json:"workflow_id"`
}

func (b BindingRequest) condition() control.Condition {
	return control.Condition{
		Source:   "github",
		Type:     "workflow.completed",
		Resource: fmt.Sprintf("%d/%d/%d/%d/%d", b.Repository, b.PullRequest, b.WorkflowID, b.RunID, b.RunAttempt),
		Version:  b.HeadSHA,
	}
}

func (b *BindingRequest) validate() error {
	b.GoalID = strings.ToLower(b.GoalID)
	b.RepositoryRef = strings.ToLower(b.RepositoryRef)
	if !control.ValidID(b.GoalID) || b.Generation < 1 || b.Installation < 0 || b.Repository < 1 || b.PullRequest < 1 || b.RunID < 1 || b.RunAttempt < 1 || b.WorkflowID < 1 || !shaPattern.MatchString(b.HeadSHA) || !repoPattern.MatchString(b.RepositoryRef) {
		return errors.New("invalid GitHub binding")
	}
	return nil
}

func githubInstance(installation, repository int64) string {
	if installation > 0 {
		return "installation:" + strconv.FormatInt(installation, 10)
	}
	return "repository:" + strconv.FormatInt(repository, 10)
}

type workflowPayload struct {
	Action       string `json:"action"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	WorkflowRun struct {
		ID             int64  `json:"id"`
		RunAttempt     int64  `json:"run_attempt"`
		WorkflowID     int64  `json:"workflow_id"`
		HeadSHA        string `json:"head_sha"`
		Status         string `json:"status"`
		Conclusion     string `json:"conclusion"`
		Event          string `json:"event"`
		HeadRepository struct {
			ID int64 `json:"id"`
		} `json:"head_repository"`
		PullRequests []struct {
			Number int64 `json:"number"`
			Head   struct {
				SHA  string `json:"sha"`
				Repo struct {
					ID int64 `json:"id"`
				} `json:"repo"`
			} `json:"head"`
			Base struct {
				Repo struct {
					ID int64 `json:"id"`
				} `json:"repo"`
			} `json:"base"`
		} `json:"pull_requests"`
	} `json:"workflow_run"`
}

// Workflow is the bounded, deterministic identity used for correlation and a
// current-state check. No free-form GitHub text becomes trusted instruction.
type Workflow struct {
	Installation  int64
	Repository    int64
	RepositoryRef string
	PullRequest   int64
	HeadSHA       string
	RunID         int64
	RunAttempt    int64
	WorkflowID    int64
	Conclusion    string
}

func (w Workflow) condition() control.Condition {
	return control.Condition{
		Source:   "github",
		Type:     "workflow.completed",
		Resource: fmt.Sprintf("%d/%d/%d/%d/%d", w.Repository, w.PullRequest, w.WorkflowID, w.RunID, w.RunAttempt),
		Version:  w.HeadSHA,
	}
}

// Normalize rejects forks, branch-name correlation, ambiguous PR associations,
// pull_request_target, stale internal identities, and malformed SHA values.
func Normalize(eventType string, body []byte) (Workflow, bool, error) {
	var payload workflowPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return Workflow{}, false, err
	}
	if eventType != "workflow_run" || payload.Action != "completed" || payload.WorkflowRun.Status != "completed" {
		return Workflow{}, false, nil
	}
	if len(payload.WorkflowRun.PullRequests) != 1 {
		return Workflow{}, false, nil
	}
	pr := payload.WorkflowRun.PullRequests[0]
	repositoryRef := strings.ToLower(payload.Repository.FullName)
	w := Workflow{
		Installation:  payload.Installation.ID,
		Repository:    payload.Repository.ID,
		RepositoryRef: repositoryRef,
		PullRequest:   pr.Number,
		HeadSHA:       payload.WorkflowRun.HeadSHA,
		RunID:         payload.WorkflowRun.ID,
		RunAttempt:    payload.WorkflowRun.RunAttempt,
		WorkflowID:    payload.WorkflowRun.WorkflowID,
		Conclusion:    payload.WorkflowRun.Conclusion,
	}
	trusted := w.Installation >= 0 && w.Repository > 0 && w.PullRequest > 0 && w.RunID > 0 && w.RunAttempt > 0 && w.WorkflowID > 0 && repoPattern.MatchString(repositoryRef) && shaPattern.MatchString(w.HeadSHA) && payload.WorkflowRun.Event == "pull_request" && payload.WorkflowRun.HeadRepository.ID == w.Repository && pr.Head.Repo.ID == w.Repository && pr.Base.Repo.ID == w.Repository && pr.Head.SHA == w.HeadSHA
	return w, trusted, nil
}

func Verify(body []byte, signature, secret string) bool {
	if secret == "" || len(body) > maxBody || !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write(body)
	return subtle.ConstantTimeCompare(provided, h.Sum(nil)) == 1
}

type Freshness interface {
	Bindable(context.Context, Workflow) (bool, error)
	Current(context.Context, Workflow) (bool, error)
}

type API struct {
	client *http.Client
	token  string
	base   string
}

func NewAPI(token string) *API {
	return &API{client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, token: token, base: "https://api.github.com"}
}

func (a *API) get(ctx context.Context, endpoint string, destination any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+endpoint, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "piglor-loom/0.2")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	response, err := a.client.Do(req)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("GitHub API returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return false, err
	}
	if len(body) > maxBody {
		return false, errors.New("GitHub API response exceeds limit")
	}
	if err = json.Unmarshal(body, destination); err != nil {
		return false, err
	}
	return true, nil
}

// Current revalidates the public PR head and workflow run at wake time. Private
// repositories require a least-privilege installation token in the secret store.
func (a *API) check(ctx context.Context, expected Workflow, requireCompleted bool) (bool, error) {
	parts := strings.Split(expected.RepositoryRef, "/")
	if len(parts) != 2 || !repoPattern.MatchString(expected.RepositoryRef) {
		return false, nil
	}
	base := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
	var pr struct {
		Number int64 `json:"number"`
		Head   struct {
			SHA  string `json:"sha"`
			Repo struct {
				ID int64 `json:"id"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Repo struct {
				ID int64 `json:"id"`
			} `json:"repo"`
		} `json:"base"`
	}
	ok, err := a.get(ctx, fmt.Sprintf("%s/pulls/%d", base, expected.PullRequest), &pr)
	if err != nil || !ok {
		return ok, err
	}
	if pr.Number != expected.PullRequest || pr.Head.SHA != expected.HeadSHA || pr.Head.Repo.ID != expected.Repository || pr.Base.Repo.ID != expected.Repository {
		return false, nil
	}
	var run struct {
		ID         int64  `json:"id"`
		Attempt    int64  `json:"run_attempt"`
		WorkflowID int64  `json:"workflow_id"`
		HeadSHA    string `json:"head_sha"`
		Status     string `json:"status"`
		Event      string `json:"event"`
		Repository struct {
			ID int64 `json:"id"`
		} `json:"repository"`
	}
	ok, err = a.get(ctx, fmt.Sprintf("%s/actions/runs/%d", base, expected.RunID), &run)
	if err != nil || !ok {
		return ok, err
	}
	valid := run.ID == expected.RunID && run.Attempt == expected.RunAttempt && run.WorkflowID == expected.WorkflowID && run.HeadSHA == expected.HeadSHA && run.Event == "pull_request" && run.Repository.ID == expected.Repository
	if requireCompleted {
		valid = valid && run.Status == "completed"
	} else {
		valid = valid && run.Status != ""
	}
	return valid, nil
}

func (a *API) Bindable(ctx context.Context, expected Workflow) (bool, error) {
	return a.check(ctx, expected, false)
}

func (a *API) Current(ctx context.Context, expected Workflow) (bool, error) {
	return a.check(ctx, expected, true)
}

type Handler struct {
	store      *control.Store
	adminToken string
	secret     string
	freshness  Freshness
}

func NewHandler(store *control.Store, adminToken, secret string, freshness Freshness) http.Handler {
	return &Handler{store: store, adminToken: adminToken, secret: secret, freshness: freshness}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/github/bindings":
		h.bind(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/github/webhook":
		h.webhook(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) bind(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+h.adminToken)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Invalid authentication"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request BindingRequest
	if err := decoder.Decode(&request); err != nil || request.validate() != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "Invalid GitHub binding"})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "Invalid GitHub binding"})
		return
	}
	runtime, err := h.store.GoalRuntime(r.Context(), request.GoalID)
	if err != nil {
		h.failure(w, err)
		return
	}
	if runtime == "codex-container" {
		writeJSON(w, http.StatusConflict, map[string]string{"detail": "Privileged GitHub wake remains disabled until installation authorization is implemented"})
		return
	}
	if h.freshness == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "GitHub freshness validation is unavailable"})
		return
	}
	bindable, validationErr := h.freshness.Bindable(r.Context(), Workflow{Installation: request.Installation, Repository: request.Repository, RepositoryRef: request.RepositoryRef, PullRequest: request.PullRequest, HeadSHA: request.HeadSHA, RunID: request.RunID, RunAttempt: request.RunAttempt, WorkflowID: request.WorkflowID})
	if validationErr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "GitHub freshness validation failed"})
		return
	}
	if !bindable {
		writeJSON(w, http.StatusConflict, map[string]string{"detail": "GitHub binding is stale or unauthorized"})
		return
	}
	attributes := map[string]any{
		"installation_id": strconv.FormatInt(request.Installation, 10), "repository_id": strconv.FormatInt(request.Repository, 10), "repository_full_name": request.RepositoryRef, "pull_request": strconv.FormatInt(request.PullRequest, 10), "head_sha": request.HeadSHA, "run_id": strconv.FormatInt(request.RunID, 10), "run_attempt": strconv.FormatInt(request.RunAttempt, 10), "workflow_id": strconv.FormatInt(request.WorkflowID, 10),
	}
	err = h.store.Bind(r.Context(), control.Binding{GoalID: request.GoalID, Generation: request.Generation, Instance: githubInstance(request.Installation, request.Repository), Condition: request.condition(), Attributes: attributes})
	if err != nil {
		h.failure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "bound"})
}

func (h *Handler) webhook(w http.ResponseWriter, r *http.Request) {
	if len(h.secret) < 32 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "GitHub integration is not configured"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"detail": "Webhook exceeds size limit"})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Invalid webhook envelope"})
		}
		return
	}
	if !Verify(body, r.Header.Get("X-Hub-Signature-256"), h.secret) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Invalid webhook signature"})
		return
	}
	delivery := strings.ToLower(r.Header.Get("X-GitHub-Delivery"))
	if !control.ValidID(delivery) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Invalid webhook envelope"})
		return
	}
	workflow, trusted, err := Normalize(r.Header.Get("X-GitHub-Event"), body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Invalid webhook envelope"})
		return
	}
	if workflow.Repository < 1 {
		writeJSON(w, http.StatusAccepted, map[string]string{"disposition": "ignored"})
		return
	}
	details := map[string]any{"trust": "signed", "conclusion": workflow.Conclusion}
	var condition *control.Condition
	if trusted {
		if h.freshness == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "GitHub freshness validation is unavailable"})
			return
		}
		current, validationErr := h.freshness.Current(r.Context(), workflow)
		if validationErr != nil {
			_, _ = h.store.ReceiveIntegration(r.Context(), control.IntegrationReceipt{Source: "github", Instance: githubInstance(workflow.Installation, workflow.Repository), DeliveryID: delivery, Details: details, RawBody: body})
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "GitHub freshness validation failed"})
			return
		}
		if current {
			value := workflow.condition()
			condition = &value
		}
	}
	result, err := h.store.ReceiveIntegration(r.Context(), control.IntegrationReceipt{Source: "github", Instance: githubInstance(workflow.Installation, workflow.Repository), DeliveryID: delivery, Condition: condition, Details: details, RawBody: body})
	if err != nil {
		h.failure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) failure(w http.ResponseWriter, err error) {
	var fault *control.Fault
	if errors.As(err, &fault) {
		writeJSON(w, fault.Status, map[string]string{"detail": fault.Message})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "GitHub integration temporarily unavailable"})
}

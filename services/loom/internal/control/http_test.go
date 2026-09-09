package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const adminTestToken = "admin-test-token-with-at-least-32-characters"

func apiRequest(t *testing.T, handler http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &encoded)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func responseObject(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatalf("invalid response %q: %v", response.Body.String(), err)
	}
	return value
}

func TestNativeHTTPGoalAndOutboundWorkerLifecycle(t *testing.T) {
	store := testStore(t, true)
	handler := NewAPIHandler(store, adminTestToken)
	condition := Condition{"human", "approval", "change-42", "1"}

	if response := apiRequest(t, handler, http.MethodPost, "/v1/workers", "wrong", Enrollment{Workspace: "http", Protocol: 2}); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized enrollment status=%d", response.Code)
	}
	enrollmentResponse := apiRequest(t, handler, http.MethodPost, "/v1/workers", adminTestToken, Enrollment{Workspace: "http", Protocol: 2})
	if enrollmentResponse.Code != http.StatusCreated {
		t.Fatalf("enrollment status=%d body=%s", enrollmentResponse.Code, enrollmentResponse.Body.String())
	}
	enrollment := responseObject(t, enrollmentResponse)
	workerID, workerToken := enrollment["worker_id"].(string), enrollment["token"].(string)

	createResponse := apiRequest(t, handler, http.MethodPost, "/v1/goals", adminTestToken, CreateGoal{Title: "HTTP lifecycle", Objective: "Yield without inference", Runtime: "remote-demo", WorkerID: &workerID, Condition: condition, CompletionCondition: &condition})
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	goalID := responseObject(t, createResponse)["id"].(string)
	if err := store.Dispatch(context.Background(), goalID); err != nil {
		t.Fatal(err)
	}

	if response := apiRequest(t, handler, http.MethodGet, "/v1/worker/commands", "wrong", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized poll status=%d", response.Code)
	}
	pollResponse := apiRequest(t, handler, http.MethodGet, "/v1/worker/commands", workerToken, nil)
	if pollResponse.Code != http.StatusOK {
		t.Fatalf("poll status=%d body=%s", pollResponse.Code, pollResponse.Body.String())
	}
	commands := responseObject(t, pollResponse)["commands"].([]any)
	commandID := commands[0].(map[string]any)["id"].(string)
	claimID := ID()
	claimResponse := apiRequest(t, handler, http.MethodPost, "/v1/worker/commands/"+commandID+"/claim", workerToken, Claim{Protocol: 2, ClaimID: claimID})
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", claimResponse.Code, claimResponse.Body.String())
	}
	sessionID := responseObject(t, claimResponse)["session_id"].(string)
	providerID := "fixture-session"
	sessionResponse := apiRequest(t, handler, http.MethodPost, "/v1/worker/commands/"+commandID+"/session", workerToken, BindSession{Claim: Claim{Protocol: 2, ClaimID: claimID}, SessionID: sessionID, ProviderID: providerID})
	if sessionResponse.Code != http.StatusOK || responseObject(t, sessionResponse)["provider_session_id"] != providerID {
		t.Fatalf("session status=%d body=%s", sessionResponse.Code, sessionResponse.Body.String())
	}
	outcome := "yield"
	stopResponse := apiRequest(t, handler, http.MethodPost, "/v1/worker/commands/"+commandID+"/stop", workerToken, Stop{Claim: Claim{Protocol: 2, ClaimID: claimID}, SessionID: sessionID, ProviderID: &providerID, Duration: 4, Success: true, Outcome: &outcome})
	if stopResponse.Code != http.StatusOK || responseObject(t, stopResponse)["status"] != "accepted" {
		t.Fatalf("stop status=%d body=%s", stopResponse.Code, stopResponse.Body.String())
	}

	event := Event{Condition: condition, GoalID: goalID, Generation: 1, DeliveryID: "approval-delivery"}
	eventResponse := apiRequest(t, handler, http.MethodPost, "/v1/events", adminTestToken, event)
	if eventResponse.Code != http.StatusOK || responseObject(t, eventResponse)["disposition"] != "accepted" {
		t.Fatalf("event status=%d body=%s", eventResponse.Code, eventResponse.Body.String())
	}
	duplicateResponse := apiRequest(t, handler, http.MethodPost, "/v1/events", adminTestToken, event)
	if duplicateResponse.Code != http.StatusOK || responseObject(t, duplicateResponse)["disposition"] != "duplicate" {
		t.Fatalf("duplicate status=%d body=%s", duplicateResponse.Code, duplicateResponse.Body.String())
	}
	cancelResponse := apiRequest(t, handler, http.MethodPost, "/v1/goals/"+goalID+"/cancel", adminTestToken, nil)
	if cancelResponse.Code != http.StatusOK || responseObject(t, cancelResponse)["status"] != "cancelled" {
		t.Fatalf("cancel status=%d body=%s", cancelResponse.Code, cancelResponse.Body.String())
	}
	revokeResponse := apiRequest(t, handler, http.MethodPost, "/v1/workers/"+workerID+"/revoke", adminTestToken, nil)
	if revokeResponse.Code != http.StatusOK || responseObject(t, revokeResponse)["status"] != "revoked" {
		t.Fatalf("revoke status=%d body=%s", revokeResponse.Code, revokeResponse.Body.String())
	}
	if response := apiRequest(t, handler, http.MethodGet, "/v1/worker/commands", workerToken, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked worker poll status=%d", response.Code)
	}
}

func TestNativeHTTPRejectsUnknownAndOversizedJSON(t *testing.T) {
	handler := NewAPIHandler(testStore(t, true), adminTestToken)
	unknown := apiRequest(t, handler, http.MethodPost, "/v1/workers", adminTestToken, map[string]any{"workspace_ref": "http", "unknown": true})
	if unknown.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown field status=%d body=%s", unknown.Code, unknown.Body.String())
	}
	largeBody := append([]byte(`{"delivery_id":"`), bytes.Repeat([]byte("x"), (1<<20)+1)...)
	largeBody = append(largeBody, []byte(`"}`)...)
	large := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewReader(largeBody))
	large.Header.Set("Authorization", "Bearer "+adminTestToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, large)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d", response.Code)
	}
}

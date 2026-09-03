package localserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func bootstrapRequest(server *BootstrapServer, body []byte, session, origin bool) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, server.origin+"/__passport/v2/setup", bytes.NewReader(body))
	request.Host = "127.0.0.1:43128"
	if session {
		request.AddCookie(&http.Cookie{Name: cookieName, Value: server.token})
	}
	if origin {
		request.Header.Set("Origin", server.origin)
	}
	server.handler().ServeHTTP(recorder, request)
	return recorder
}

func TestBootstrapProvisionsOnlyAfterExplicitPublicRecordConsent(t *testing.T) {
	calls := 0
	server, err := NewBootstrap(func(_ context.Context, selection BootstrapSelection) (string, error) {
		calls++
		if selection.Platform != "linux" {
			t.Fatalf("unexpected selection: %#v", selection)
		}
		return "http://127.0.0.1:43129/__passport/start/" + serverTokenForTest, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	server.origin = "http://127.0.0.1:43128"

	withoutConsent, _ := json.Marshal(BootstrapSelection{Platform: "linux"})
	response := bootstrapRequest(server, withoutConsent, true, true)
	if response.Code != http.StatusUnprocessableEntity || calls != 0 {
		t.Fatalf("consent gate returned %d after %d calls", response.Code, calls)
	}

	withConsent, _ := json.Marshal(BootstrapSelection{Platform: "linux", PublicRecordConsent: true})
	response = bootstrapRequest(server, withConsent, true, true)
	if response.Code != http.StatusCreated || calls != 1 {
		t.Fatalf("consented setup returned %d after %d calls: %s", response.Code, calls, response.Body.String())
	}
	response = bootstrapRequest(server, withConsent, true, true)
	if response.Code != http.StatusConflict || calls != 1 {
		t.Fatalf("repeated setup returned %d after %d calls", response.Code, calls)
	}
}

const serverTokenForTest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestBootstrapRejectsMissingSessionAndCrossOriginMutation(t *testing.T) {
	server, err := NewBootstrap(func(context.Context, BootstrapSelection) (string, error) {
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	server.origin = "http://127.0.0.1:43128"
	body, _ := json.Marshal(BootstrapSelection{Platform: "linux", PublicRecordConsent: true})
	if response := bootstrapRequest(server, body, false, true); response.Code != http.StatusUnauthorized {
		t.Fatalf("missing session returned %d", response.Code)
	}
	if response := bootstrapRequest(server, body, true, false); response.Code != http.StatusForbidden {
		t.Fatalf("missing origin returned %d", response.Code)
	}
}

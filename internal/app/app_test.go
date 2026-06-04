package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	reconcilesync "github.com/dexogen/iplist-go-unifi/internal/sync"
)

func TestStatusReportsRunInProgress(t *testing.T) {
	service := &Service{}
	startedAt := time.Date(2026, 6, 4, 19, 22, 44, 0, time.UTC)
	service.markRunStarted(startedAt)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/status", nil)
	service.routes().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"sources":[]`) {
		t.Fatalf("sources should be an empty array while running, body = %s", body)
	}

	var status reconcilesync.RunStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.StartedAt.Equal(startedAt) {
		t.Fatalf("started_at = %s, want %s", status.StartedAt, startedAt)
	}
	if !status.FinishedAt.IsZero() {
		t.Fatalf("finished_at = %s, want zero", status.FinishedAt)
	}
}

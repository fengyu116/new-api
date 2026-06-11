package sora

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
)

func TestParseTaskResultUnknownKeepsPolling(t *testing.T) {
	adaptor := &TaskAdaptor{}
	result, err := adaptor.ParseTaskResult([]byte(`{
		"id":"task-upstream",
		"model":"grok-video-3-10s",
		"object":"video",
		"status":"unknown",
		"progress":0,
		"created_at":1781160354,
		"completed_at":1781160354
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != model.TaskStatusInProgress {
		t.Fatalf("expected unknown upstream status to keep polling, got %q", result.Status)
	}
}

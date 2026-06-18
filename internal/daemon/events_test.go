package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAppendAndStreamLifecycleEvents(t *testing.T) {
	root := t.TempDir()
	ts := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	if err := AppendLifecycleEvent(root, &LifecycleEvent{
		ID:       "event-1",
		TS:       ts,
		Action:   "dispatch",
		Instance: "manager",
		Agent:    "manager",
		Status:   StatusRunning,
		PID:      42,
		Message:  "started",
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	var buf bytes.Buffer
	if err := StreamLifecycleEvents(context.Background(), &buf, root, false, 0); err != nil {
		t.Fatalf("stream events: %v", err)
	}
	var got LifecycleEvent
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("decode event: %v\nbody=%s", err, buf.String())
	}
	if got.ID != "event-1" || got.Action != "dispatch" || got.Instance != "manager" || got.PID != 42 || !got.TS.Equal(ts) {
		t.Fatalf("event = %+v", got)
	}
}

func TestStreamLifecycleEventsTail(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := AppendLifecycleEvent(root, &LifecycleEvent{Action: "dispatch", Instance: name}); err != nil {
			t.Fatalf("append %s: %v", name, err)
		}
	}
	var buf bytes.Buffer
	if err := StreamLifecycleEvents(context.Background(), &buf, root, false, 2); err != nil {
		t.Fatalf("stream tail: %v", err)
	}
	body := buf.String()
	if strings.Contains(body, `"instance":"a"`) || !strings.Contains(body, `"instance":"b"`) || !strings.Contains(body, `"instance":"c"`) {
		t.Fatalf("tail body = %s", body)
	}
}

func TestStreamLifecycleEventsMissingFileIsEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := StreamLifecycleEvents(context.Background(), &buf, t.TempDir(), false, 0); err != nil {
		t.Fatalf("stream missing events: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("body = %q, want empty", buf.String())
	}
}

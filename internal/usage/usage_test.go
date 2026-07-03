package usage

import (
	"strings"
	"testing"
	"time"
)

func TestParseCodexJSONLSumsTurnCompletedUsage(t *testing.T) {
	var rec Record
	log := strings.Join([]string{
		`{"type":"thread.started","thread_id":"t1"}`,
		`not json`,
		`{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":7,"output_tokens":3,"reasoning_output_tokens":2}}`,
		`{"type":"turn.completed","usage":{"input_tokens":20,"cached_input_tokens":11,"output_tokens":5,"reasoning_output_tokens":4}}`,
	}, "\n")
	if err := ParseCodexJSONL(&rec, strings.NewReader(log)); err != nil {
		t.Fatalf("ParseCodexJSONL: %v", err)
	}
	if !rec.TokensAvailable {
		t.Fatal("tokens should be available")
	}
	if rec.Turns != 2 ||
		rec.InputTokens != 30 ||
		rec.CachedInputTokens != 18 ||
		rec.OutputTokens != 8 ||
		rec.ReasoningOutputTokens != 6 {
		t.Fatalf("record = %+v", rec)
	}
}

func TestMergeRecordIsIdempotentByInstanceAndStart(t *testing.T) {
	started := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	rec := Record{
		Instance:          "worker-squ-73",
		Agent:             "worker",
		Runtime:           "codex",
		TokensAvailable:   true,
		InputTokens:       10,
		CachedInputTokens: 8,
		OutputTokens:      3,
		Turns:             1,
		DurationMS:        1500,
		StartedAt:         started,
		EndedAt:           started.Add(1500 * time.Millisecond),
	}
	u, changed := MergeRecord(nil, rec)
	if !changed {
		t.Fatal("first merge should change usage")
	}
	u, changed = MergeRecord(u, rec)
	if changed {
		t.Fatal("identical merge should be unchanged")
	}
	if len(u.Records) != 1 || u.Summary.Runs != 1 || u.Summary.InputTokens != 10 {
		t.Fatalf("usage = %+v", u)
	}
	rec.InputTokens = 14
	u, changed = MergeRecord(u, rec)
	if !changed {
		t.Fatal("changed record should update usage")
	}
	if len(u.Records) != 1 || u.Summary.InputTokens != 14 {
		t.Fatalf("updated usage = %+v", u)
	}
}

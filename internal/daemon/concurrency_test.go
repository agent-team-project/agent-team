package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/topology"
)

func TestConcurrencyControllerMachineLoadAdmission(t *testing.T) {
	c := newConcurrencyController(&topology.Concurrency{
		Enabled:           true,
		MaxCeiling:        10,
		InitialCeiling:    10,
		TargetLoadPerCore: 0.5,
		LoadPerDispatch:   1,
	})
	c.sampler = func() (machineLoadSample, error) {
		return machineLoadSample{Load1: 4, Cores: 4}, nil
	}

	admission, ev := c.admit(time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC), 0)
	if admission.Allowed || admission.Ceiling != 0 || admission.Running != 0 {
		t.Fatalf("admission = %+v, want blocked at ceiling 0", admission)
	}
	if ev == nil || ev.Action != "concurrency_ceiling_adjusted" || !strings.Contains(ev.Message, "concurrency ceiling adjusted to 0 (load average 4.00/4 cores)") {
		t.Fatalf("event = %+v", ev)
	}
}

func TestConcurrencyControllerCrashBackoffAndStableIncrease(t *testing.T) {
	c := newConcurrencyController(&topology.Concurrency{
		Enabled:        true,
		MinCeiling:     1,
		MaxCeiling:     8,
		InitialCeiling: 8,
		CrashWindow:    10 * time.Minute,
		CrashThreshold: 2,
		DecreaseFactor: 0.5,
		StableWindow:   time.Second,
		IncreaseStep:   1,
	})
	c.sampler = func() (machineLoadSample, error) {
		return machineLoadSample{Load1: 0, Cores: 100}, nil
	}

	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	if ev := c.observeCrash(now, 0); ev != nil {
		t.Fatalf("first crash event = %+v, want nil before threshold", ev)
	}
	ev := c.observeCrash(now.Add(time.Second), 0)
	if ev == nil || !strings.Contains(ev.Message, "concurrency ceiling adjusted to 4 (AIMD decrease after 2 crashes in 10m0s)") {
		t.Fatalf("decrease event = %+v", ev)
	}
	if c.current != 4 {
		t.Fatalf("current ceiling = %d, want 4", c.current)
	}

	admission, ev := c.admit(now.Add(3*time.Second), 0)
	if admission.Ceiling != 5 || !admission.Allowed {
		t.Fatalf("admission after stable window = %+v, want ceiling 5 allowed", admission)
	}
	if ev == nil || !strings.Contains(ev.Message, "concurrency ceiling adjusted to 5 (AIMD increase after 1s stable)") {
		t.Fatalf("increase event = %+v", ev)
	}
}

func TestEventConcurrencyQueuesWhenMachineLoadIsSaturated(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	top := mustParseCustomTopo(t, `
[concurrency]
enabled = true
max_ceiling = 10
initial_ceiling = 10
target_load_per_core = 0.5
load_per_dispatch = 1

[instances.worker]
agent = "worker"
ephemeral = true
replicas = 10

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"
`)
	resolver := NewEventResolver(m, fixtureTeamDir(t), top)
	resolver.concurrency.sampler = func() (machineLoadSample, error) {
		return machineLoadSample{Load1: 4, Cores: 4}, nil
	}

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "worker",
		"name":   "worker-gh202-load",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "queued" || result.Outcomes[0].Reason != QueueReasonConcurrencyCeiling {
		t.Fatalf("outcomes = %+v, want concurrency queued", result.Outcomes)
	}
	if fake.callCount() != 0 {
		t.Fatalf("spawn calls=%d, want 0", fake.callCount())
	}
	items, err := ListQueueItems(root)
	if err != nil {
		t.Fatalf("ListQueueItems: %v", err)
	}
	if len(items) != 1 || items[0].Reason != QueueReasonConcurrencyCeiling || items[0].InstanceID != "worker-gh202-load" {
		t.Fatalf("queue items = %+v", items)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 1 || !strings.Contains(events[0].Message, "concurrency ceiling adjusted to 0") {
		t.Fatalf("events = %+v", events)
	}
}

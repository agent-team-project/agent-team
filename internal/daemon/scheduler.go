package daemon

import (
	"context"
	"time"

	"github.com/jamesaud/agent-team/internal/topology"
)

var schedulePollInterval = time.Second

// RunSchedules publishes due topology schedules until ctx is cancelled.
func (r *EventResolver) RunSchedules(ctx context.Context) {
	if r == nil {
		return
	}
	state := map[string]time.Time{}
	r.fireDueSchedules(time.Now().UTC(), state)
	ticker := time.NewTicker(schedulePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			r.fireDueSchedules(now.UTC(), state)
		}
	}
}

func (r *EventResolver) fireDueSchedules(now time.Time, state map[string]time.Time) []string {
	topo := r.Topology()
	if topo == nil || len(topo.Schedules) == 0 {
		for name := range state {
			delete(state, name)
		}
		return nil
	}
	current := map[string]bool{}
	fired := []string{}
	for _, sched := range topo.SortedSchedules() {
		current[sched.Name] = true
		last, seen := state[sched.Name]
		if !seen {
			state[sched.Name] = now
			if !sched.RunOnStart {
				continue
			}
		} else if now.Sub(last) < sched.Every {
			continue
		}
		state[sched.Name] = now
		payload := sched.EventPayload()
		_, _ = r.Event(topology.EventSchedule, payload)
		fired = append(fired, sched.Name)
	}
	for name := range state {
		if !current[name] {
			delete(state, name)
		}
	}
	return fired
}

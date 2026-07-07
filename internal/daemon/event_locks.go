package daemon

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/topology"
)

func (r *EventResolver) dispatchLocksLocked(inst *topology.Instance, payload map[string]any) []string {
	names := []string{}
	if inst != nil {
		names = append(names, inst.Locks...)
	}
	if r.topo != nil {
		pipelineName := payloadString(payload, "pipeline")
		stepID := payloadString(payload, "pipeline_step")
		if pipelineName != "" && stepID != "" {
			if pipeline := r.topo.Pipelines[pipelineName]; pipeline != nil {
				for _, step := range pipeline.Steps {
					if step.ID == stepID {
						names = append(names, step.Locks...)
						break
					}
				}
			}
		}
	}
	return normalizeLockNames(names)
}

func normalizeLockNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (r *EventResolver) acquireLocksLocked(names []string, instance string, env origin.Envelope, now time.Time) (bool, error) {
	names = normalizeLockNames(names)
	if len(names) == 0 {
		return true, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, name := range names {
		tr := r.lockTrackerLocked(name, env)
		if tr == nil {
			return false, fmt.Errorf("lock %q is not declared", name)
		}
		if tr.holders[instance] != nil {
			continue
		}
		if len(tr.holders) >= tr.slots {
			return false, nil
		}
	}
	acquired := []string{}
	for _, name := range names {
		tr := r.lockTrackerLocked(name, env)
		if tr.holders[instance] != nil {
			continue
		}
		lease := &LockLease{
			Lock:       tr.storage,
			Name:       tr.name,
			Scope:      tr.scope,
			Instance:   instance,
			AcquiredAt: now,
			UpdatedAt:  now,
			Origin:     env,
		}
		if err := WriteLockLease(r.mgr.daemonRoot, lease); err != nil {
			for _, held := range acquired {
				delete(r.locks[held].holders, instance)
				_ = RemoveLockLease(r.mgr.daemonRoot, held, instance)
			}
			return false, err
		}
		tr.holders[instance] = lease
		acquired = append(acquired, tr.storage)
	}
	return true, nil
}

func (r *EventResolver) lockTrackerLocked(name string, env origin.Envelope) *dispatchLockTracker {
	if r.locks == nil {
		r.locks = map[string]*dispatchLockTracker{}
	}
	declared := (*topology.Lock)(nil)
	if r.topo != nil {
		declared = r.topo.Locks[name]
	}
	if declared == nil {
		return nil
	}
	storage := topology.ScopedResourceName(name, declared.Scope, env.Team, env.Job)
	if tr := r.locks[storage]; tr != nil {
		return tr
	}
	tr := &dispatchLockTracker{
		name:    name,
		storage: storage,
		scope:   declared.Scope,
		team:    env.Team,
		job:     env.Job,
		slots:   declared.Slots,
		holders: map[string]*LockLease{},
	}
	r.locks[storage] = tr
	return tr
}

func (r *EventResolver) updateLockLeasePID(instance string, pid int) {
	if strings.TrimSpace(instance) == "" || pid <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	for name, tr := range r.locks {
		if tr == nil || tr.holders == nil {
			continue
		}
		lease := tr.holders[instance]
		if lease == nil {
			continue
		}
		lease.PID = pid
		lease.UpdatedAt = now
		_ = WriteLockLease(r.mgr.daemonRoot, lease)
		r.locks[name].holders[instance] = lease
	}
}

// releaseLocksForInstanceLocked frees every lock slot held by instance and
// reports how many were released so callers can kick waiters: lock_held queue
// items may belong to OTHER declared instances, which the per-instance reap
// queue pop never retries (SQU-76).
func (r *EventResolver) releaseLocksForInstanceLocked(instance string) int {
	if strings.TrimSpace(instance) == "" {
		return 0
	}
	released := 0
	for name, tr := range r.locks {
		if tr == nil || tr.holders == nil {
			continue
		}
		if tr.holders[instance] == nil {
			continue
		}
		delete(tr.holders, instance)
		_ = RemoveLockLease(r.mgr.daemonRoot, name, instance)
		released++
	}
	return released
}

func (r *EventResolver) recoverLockStateLocked(now time.Time) {
	r.locks = map[string]*dispatchLockTracker{}
	if r.topo != nil {
		for _, lock := range r.topo.SortedLocks() {
			if lock.Scope != topology.ScopeMachine {
				continue
			}
			r.locks[lock.Name] = &dispatchLockTracker{
				name:    lock.Name,
				storage: lock.Name,
				scope:   lock.Scope,
				slots:   lock.Slots,
				holders: map[string]*LockLease{},
			}
		}
	}
	if r.mgr == nil {
		return
	}
	leases, err := ListLockLeases(r.mgr.daemonRoot)
	if err != nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, lease := range leases {
		declaredName := strings.TrimSpace(lease.Name)
		if declaredName == "" {
			declaredName = lease.Lock
		}
		declared := (*topology.Lock)(nil)
		if r.topo != nil {
			declared = r.topo.Locks[declaredName]
		}
		if declared == nil {
			_ = RemoveLockLease(r.mgr.daemonRoot, lease.Lock, lease.Instance)
			continue
		}
		tr := r.locks[lease.Lock]
		if tr == nil {
			tr = &dispatchLockTracker{
				name:    declaredName,
				storage: lease.Lock,
				scope:   firstNonEmpty(lease.Scope, declared.Scope),
				team:    lease.Origin.Team,
				job:     lease.Origin.Job,
				slots:   declared.Slots,
				holders: map[string]*LockLease{},
			}
			r.locks[lease.Lock] = tr
		}
		live, recoveredPID := r.lockLeaseLivePID(lease)
		if !live {
			_ = RemoveLockLease(r.mgr.daemonRoot, lease.Lock, lease.Instance)
			continue
		}
		if recoveredPID > 0 && lease.PID != recoveredPID {
			lease.PID = recoveredPID
			lease.UpdatedAt = now
			_ = WriteLockLease(r.mgr.daemonRoot, lease)
		}
		tr.holders[lease.Instance] = lease
	}
}

func (r *EventResolver) lockLeaseLivePID(lease *LockLease) (bool, int) {
	if lease == nil {
		return false, 0
	}
	if lease.PID > 0 && PidLiveCheck(lease.PID) {
		return true, lease.PID
	}
	if r.mgr == nil {
		return false, 0
	}
	meta, err := ReadMetadata(r.mgr.daemonRoot, lease.Instance)
	if err != nil || meta == nil || meta.Status != StatusRunning || meta.PID <= 0 {
		return false, 0
	}
	if !PidLiveCheck(meta.PID) {
		return false, 0
	}
	return true, meta.PID
}

// RecoverLockState rebuilds in-memory dispatch lock holders from the durable
// ledger, dropping entries whose instances no longer have a live PID.
func (r *EventResolver) RecoverLockState() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recoverLockStateLocked(time.Now().UTC())
}

// LockSnapshots returns declared lock utilization from current resolver state.
func (r *EventResolver) LockSnapshots() []LockSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recoverLockStateLocked(time.Now().UTC())
	out := make([]LockSnapshot, 0, len(r.locks))
	seenDeclared := map[string]bool{}
	names := make([]string, 0, len(r.locks))
	for name := range r.locks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		tr := r.locks[name]
		if tr == nil {
			continue
		}
		holders := make([]LockHolder, 0, len(tr.holders))
		holderNames := make([]string, 0, len(tr.holders))
		for instance := range tr.holders {
			holderNames = append(holderNames, instance)
		}
		sort.Strings(holderNames)
		for _, instance := range holderNames {
			lease := tr.holders[instance]
			holders = append(holders, LockHolder{
				Instance:   lease.Instance,
				PID:        lease.PID,
				AcquiredAt: lease.AcquiredAt,
				UpdatedAt:  lease.UpdatedAt,
			})
		}
		available := tr.slots - len(holders)
		if available < 0 {
			available = 0
		}
		out = append(out, LockSnapshot{
			Name:      tr.name,
			Storage:   tr.storage,
			Scope:     tr.scope,
			Team:      tr.team,
			Job:       tr.job,
			Slots:     tr.slots,
			Used:      len(holders),
			Available: available,
			Holders:   holders,
		})
		seenDeclared[tr.name] = true
	}
	if r.topo != nil {
		for _, lock := range r.topo.SortedLocks() {
			if seenDeclared[lock.Name] {
				continue
			}
			out = append(out, LockSnapshot{
				Name:      lock.Name,
				Storage:   topology.ScopedResourceName(lock.Name, lock.Scope, "", ""),
				Scope:     lock.Scope,
				Slots:     lock.Slots,
				Available: lock.Slots,
				Holders:   []LockHolder{},
			})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Name == out[j].Name {
				return out[i].Storage < out[j].Storage
			}
			return out[i].Name < out[j].Name
		})
	}
	return out
}

func (r *EventResolver) previewLockCountsLocked() (map[string]map[string]bool, map[string]int) {
	usage := map[string]map[string]bool{}
	slots := map[string]int{}
	for name, tr := range r.locks {
		if tr == nil {
			continue
		}
		slots[name] = tr.slots
		usage[name] = map[string]bool{}
		for instance := range tr.holders {
			usage[name][instance] = true
		}
	}
	return usage, slots
}

func (r *EventResolver) ensurePreviewLocksLocked(locks []string, env origin.Envelope, usage map[string]map[string]bool, slots map[string]int) {
	if r == nil || r.topo == nil {
		return
	}
	for _, name := range normalizeLockNames(locks) {
		declared := r.topo.Locks[name]
		if declared == nil {
			continue
		}
		storage := topology.ScopedResourceName(name, declared.Scope, env.Team, env.Job)
		if usage[storage] == nil {
			usage[storage] = map[string]bool{}
		}
		slots[storage] = declared.Slots
	}
}

func (r *EventResolver) scopedLockNamesLocked(locks []string, env origin.Envelope) []string {
	names := normalizeLockNames(locks)
	if r == nil || r.topo == nil {
		return names
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		declared := r.topo.Locks[name]
		if declared == nil {
			out = append(out, name)
			continue
		}
		out = append(out, topology.ScopedResourceName(name, declared.Scope, env.Team, env.Job))
	}
	return out
}

func previewLocksAvailable(locks []string, instance string, usage map[string]map[string]bool, slots map[string]int) bool {
	for _, name := range locks {
		holders := usage[name]
		if holders == nil {
			return false
		}
		if holders[instance] {
			continue
		}
		if len(holders) >= slots[name] {
			return false
		}
	}
	return true
}

func previewReserveLocks(locks []string, instance string, usage map[string]map[string]bool) {
	for _, name := range locks {
		if usage[name] == nil {
			usage[name] = map[string]bool{}
		}
		usage[name][instance] = true
	}
}

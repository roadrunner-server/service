package service

import (
	"log/slog"
	"slices"
	"sync"
	"time"

	serviceV1 "github.com/roadrunner-server/api-go/v6/service/v1"
)

// group serializes configuration, exits, and queued starts with mu.
// Plugin.mu serializes explicit lifecycle operations across groups.
type group struct {
	mu        sync.Mutex
	name      string
	log       *slog.Logger
	desired   Service
	processes []*Process
	running   map[*Process]struct{}
	pending   map[*queuedStart]struct{}
	active    bool
}

type queuedStart struct {
	timer *time.Timer
}

func newGroup(s *Service, name string, log *slog.Logger) *group {
	return &group{
		name: name, log: log, desired: s.clone(),
		running: make(map[*Process]struct{}),
		pending: make(map[*queuedStart]struct{}),
	}
}

func (g *group) start() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active = true
	for range g.desired.ProcessNum {
		if err := g.startLocked(); err != nil {
			g.active = false
			return err
		}
	}
	return nil
}

// startLocked also retains failed commands for status reads.
func (g *group) startLocked() error {
	p := NewServiceProcess(&g.desired, g.name, g.log)
	p.onExit = g.exited
	err := p.start()
	index := slices.IndexFunc(g.processes, func(p *Process) bool {
		_, live := g.running[p]
		return !live
	})
	if err != nil {
		if index < 0 {
			g.processes = append(g.processes, p)
		}
		return err
	}
	if index < 0 {
		g.processes = append(g.processes, p)
	} else {
		g.processes[index] = p
	}
	g.running[p] = struct{}{}
	g.log.Info("service was started", "name", g.name, "command", g.desired.Command)
	return nil
}

func (g *group) exited(p *Process) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.running, p)
	g.trimExitedLocked()
	if !g.active || !g.desired.RemainAfterExit {
		return
	}
	missing := g.desired.ProcessNum - len(g.running) - len(g.pending)
	for range missing {
		start := &queuedStart{}
		start.timer = time.AfterFunc(time.Duration(g.desired.RestartSec)*time.Second, func() { //nolint:gosec
			g.restartReady(start)
		})
		g.pending[start] = struct{}{}
		g.log.Debug("service restart scheduled", "name", g.name, "restart_sec", g.desired.RestartSec)
	}
}

func (g *group) restartReady(start *queuedStart) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, queued := g.pending[start]; !queued {
		return
	}
	delete(g.pending, start)
	// Other queued starts reserve their slots until their timers run.
	missing := g.desired.ProcessNum - len(g.running) - len(g.pending)
	for range missing {
		if err := g.startLocked(); err != nil {
			g.log.Error("process start error", "error", err)
			return
		}
	}
}

func (g *group) trimExitedLocked() {
	excess := len(g.processes) - g.desired.ProcessNum
	g.processes = slices.DeleteFunc(g.processes, func(p *Process) bool {
		if _, live := g.running[p]; !live && excess > 0 {
			excess--
			return true
		}
		return false
	})
}

func (g *group) cancelPendingLocked(keep int) {
	for start := range g.pending {
		if len(g.pending) <= keep {
			break
		}
		start.timer.Stop()
		delete(g.pending, start)
	}
}

func (g *group) snapshot() []*Process {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.processes)
}

// pause invalidates callbacks before releasing mu to wait for children.
func (g *group) pause() []*Process {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active = false
	g.cancelPendingLocked(0)
	return slices.Clone(g.processes)
}

func stopProcesses(procs []*Process) {
	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Go(p.stop)
	}
	wg.Wait()
}

func (g *group) stop() {
	procs := g.pause()
	stopProcesses(procs)
	for _, p := range procs {
		g.log.Info("service was stopped", "name", g.name, "command", p.service.Command)
	}
}

func (g *group) restart() error {
	stopProcesses(g.pause())
	return g.start()
}

func (g *group) update(in *serviceV1.Update) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	count, execution := int64(g.desired.ProcessNum), int64(g.desired.ExecTimeout/time.Second)
	restart, stop := g.desired.RestartSec, g.desired.TimeoutStopSec
	if in.ProcessNum != nil {
		count = *in.ProcessNum
	}
	if in.ExecTimeout != nil {
		execution = *in.ExecTimeout
	}
	if in.RestartSec != nil {
		restart = *in.RestartSec
	}
	if in.TimeoutStopSec != nil {
		stop = *in.TimeoutStopSec
	}
	if err := validateRuntimeValues(count, execution, restart, stop); err != nil {
		return err
	}
	next := g.desired
	next.ProcessNum = int(count)
	if in.ExecTimeout != nil {
		next.ExecTimeout = time.Duration(execution) * time.Second
	}
	next.RestartSec, next.TimeoutStopSec = restart, stop
	if in.RemainAfterExit != nil {
		next.RemainAfterExit = *in.RemainAfterExit
	}
	if in.ServiceNameInLogs != nil {
		next.UseServiceName = *in.ServiceNameInLogs
	}
	if in.Env != nil {
		next.Env = in.Env.Values
	}
	g.desired = next.clone()
	keep := max(0, g.desired.ProcessNum-len(g.running))
	if !g.desired.RemainAfterExit {
		keep = 0
	}
	g.cancelPendingLocked(keep)
	g.trimExitedLocked()
	return nil
}

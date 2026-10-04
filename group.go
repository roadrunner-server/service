package service

import (
	"cmp"
	"log/slog"
	"maps"
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
	if err := validateRuntimeValues(in.ProcessNum, in.ExecTimeout, in.RestartSec, in.TimeoutStopSec); err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if in.ProcessNum != nil {
		g.desired.ProcessNum = int(*in.ProcessNum)
	}
	if in.ExecTimeout != nil {
		g.desired.ExecTimeout = time.Duration(*in.ExecTimeout) * time.Second
	}
	if in.RestartSec != nil {
		g.desired.RestartSec = cmp.Or(*in.RestartSec, 30)
	}
	if in.TimeoutStopSec != nil {
		g.desired.TimeoutStopSec = cmp.Or(*in.TimeoutStopSec, 5)
	}
	if in.RemainAfterExit != nil {
		g.desired.RemainAfterExit = *in.RemainAfterExit
	}
	if in.ServiceNameInLogs != nil {
		g.desired.UseServiceName = *in.ServiceNameInLogs
	}
	if in.Env != nil {
		g.desired.Env = maps.Clone(in.Env.Values)
	}
	keep := max(0, g.desired.ProcessNum-len(g.running))
	if !g.desired.RemainAfterExit {
		keep = 0
	}
	g.cancelPendingLocked(keep)
	g.trimExitedLocked()
	return nil
}

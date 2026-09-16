package service

import (
	stderr "errors"
	"fmt"
	"time"

	shared "github.com/roadrunner-server/api-go/v6/common/v1"
	serviceV1 "github.com/roadrunner-server/api-go/v6/service/v1"
)

var (
	errNoSuchService = stderr.New("no such service")
	errServiceExists = stderr.New("service already exists")
	errPluginStopped = stderr.New("service plugin is stopped")
)

type rpc struct {
	p *Plugin
}

func (r *rpc) loadGroup(name string) (*group, error) {
	v, ok := r.p.processes.Load(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", errNoSuchService, name)
	}
	return v.(*group), nil
}

func (r *rpc) loadProcesses(name string) ([]*Process, error) {
	g, err := r.loadGroup(name)
	if err != nil {
		return nil, err
	}
	return g.snapshot(), nil
}

func (r *rpc) Create(in *serviceV1.Create, out *serviceV1.Response) error {
	r.p.logger.Debug("create service", "name", in.GetName(), "restart_sec", in.GetRestartSec(), "command", in.GetCommand(), "process number", in.GetProcessNum())

	if err := validateRuntimeValues(in.GetProcessNum(), in.GetExecTimeout(), in.GetRestartSec(), in.GetTimeoutStopSec()); err != nil {
		return err
	}

	r.p.mu.Lock()
	defer r.p.mu.Unlock()
	if r.p.stopped {
		return errPluginStopped
	}

	if _, ok := r.p.processes.Load(in.GetName()); ok {
		return fmt.Errorf("%w: %s", errServiceExists, in.GetName())
	}

	g := newGroup(&Service{
		Command:         in.GetCommand(),
		ProcessNum:      int(in.GetProcessNum()),
		ExecTimeout:     time.Second * time.Duration(in.GetExecTimeout()),
		RemainAfterExit: in.GetRemainAfterExit(),
		RestartSec:      in.GetRestartSec(),
		UseServiceName:  in.GetServiceNameInLogs(),
		TimeoutStopSec:  in.GetTimeoutStopSec(),
		Env:             in.GetEnv(),
	}, in.GetName(), r.p.logger)
	if err := g.start(); err != nil {
		g.stop()
		return err
	}

	r.p.processes.Store(in.GetName(), g)
	out.Ok = true
	return nil
}

// Update accepts desired configuration for subsequent executions.
func (r *rpc) Update(in *serviceV1.Update, out *serviceV1.Response) error {
	g, err := r.loadGroup(in.GetName())
	if err != nil {
		return err
	}
	if err = g.update(in); err != nil {
		return err
	}
	out.Ok = true
	return nil
}

func (r *rpc) Terminate(in *serviceV1.Service, out *serviceV1.Response) error {
	r.p.logger.Debug("terminate service", "name", in.GetName())

	r.p.mu.Lock()
	defer r.p.mu.Unlock()

	g, err := r.loadGroup(in.GetName())
	if err != nil {
		return err
	}
	g.stop()
	r.p.processes.Delete(in.GetName())

	out.Ok = true
	return nil
}

func (r *rpc) Restart(in *serviceV1.Service, out *serviceV1.Response) error {
	name := in.GetName()
	r.p.logger.Debug("restart service", "name", name)

	r.p.mu.Lock()
	defer r.p.mu.Unlock()

	g, err := r.loadGroup(name)
	if err != nil {
		return err
	}

	if err = g.restart(); err != nil {
		g.stop()
		r.p.processes.Delete(name)
		return err
	}

	out.Ok = true
	return nil
}

// Deprecated: use Statuses to get correct info.
func (r *rpc) Status(in *serviceV1.Service, out *serviceV1.Status) error {
	r.p.logger.Debug("service status", "name", in.GetName())

	procs, err := r.loadProcesses(in.GetName())
	if err != nil {
		return err
	}

	for i := range procs {
		state, err := generalProcessState(procs[i].pid, procs[i].command.String())
		if err != nil {
			return err
		}

		out.Pid = int32(state.Pid) //nolint:gosec
		out.Command = state.Command
		out.CpuPercent = float32(state.CPUPercent)
		out.MemoryUsage = state.MemoryUsage
	}

	return nil
}

func (r *rpc) Statuses(in *serviceV1.Service, out *serviceV1.Statuses) error {
	r.p.logger.Debug("service status", "name", in.GetName())

	procs, err := r.loadProcesses(in.GetName())
	if err != nil {
		return err
	}

	for i := range procs {
		state, err := generalProcessState(procs[i].pid, procs[i].command.String())
		if err != nil {
			// in case of error, just add the error status + common info (pid, command)
			out.Status = append(out.Status, &serviceV1.Status{
				CpuPercent:  0,
				Pid:         int32(procs[i].pid), //nolint:gosec
				MemoryUsage: 0,
				Command:     procs[i].command.String(),
				Status: &shared.Status{
					Code:    0,
					Message: err.Error(),
				},
			})
			continue
		}

		out.Status = append(out.Status, &serviceV1.Status{
			CpuPercent:  float32(state.CPUPercent),
			Pid:         int32(state.Pid), //nolint:gosec
			MemoryUsage: state.MemoryUsage,
			Command:     state.Command,
			Status:      nil,
		})
	}

	return nil
}

func (r *rpc) List(_ *serviceV1.Service, out *serviceV1.List) error {
	r.p.processes.Range(func(key, _ any) bool {
		r.p.logger.Debug("services list", "service", key.(string))
		out.Services = append(out.Services, key.(string))
		return true
	})

	return nil
}

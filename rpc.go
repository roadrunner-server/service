package service

import (
	stderr "errors"
	"fmt"
	"sync"
	"time"

	shared "github.com/roadrunner-server/api-go/v6/common/v1"
	serviceV1 "github.com/roadrunner-server/api-go/v6/service/v1"
)

var (
	errNoSuchService = stderr.New("no such service")
	errServiceExists = stderr.New("service already exists")
)

type rpc struct {
	mu sync.RWMutex
	p  *Plugin
}

func (r *rpc) loadProcesses(name string) ([]*Process, error) {
	v, ok := r.p.processes.Load(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", errNoSuchService, name)
	}
	return v.([]*Process), nil
}

func (r *rpc) CreateService(in *serviceV1.Create, out *serviceV1.Response) error {
	r.p.logger.Debug("create service", "name", in.GetName(), "restart_sec", in.GetRestartSec(), "command", in.GetCommand(), "process number", in.GetProcessNum())

	if in.GetProcessNum() == 0 {
		return fmt.Errorf("the service with %s name should have at least 1 process", in.GetName())
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.p.processes.Load(in.GetName()); ok {
		return fmt.Errorf("%w: %s", errServiceExists, in.GetName())
	}

	procs := make([]*Process, 0, in.GetProcessNum())
	for range int(in.GetProcessNum()) {
		proc := NewServiceProcess(&Service{
			Command:         in.GetCommand(),
			ProcessNum:      int(in.GetProcessNum()),
			ExecTimeout:     time.Second * time.Duration(in.GetExecTimeout()),
			RemainAfterExit: in.GetRemainAfterExit(),
			RestartSec:      in.GetRestartSec(),
			UseServiceName:  in.GetServiceNameInLogs(),
			TimeoutStopSec:  in.GetTimeoutStopSec(),
			Env:             in.GetEnv(),
		}, in.GetName(), r.p.logger)

		if err := proc.start(); err != nil {
			// if some process from the group failed -> deallocate the whole group
			if len(procs) > 0 {
				r.p.logger.Warn("stopping already allocated processes")
				for i := range procs {
					procs[i].stop()
				}
			}
			return err
		}

		procs = append(procs, proc)
	}

	r.p.processes.Store(in.GetName(), procs)
	out.Ok = true
	return nil
}

func (r *rpc) Terminate(in *serviceV1.Service, out *serviceV1.Response) error {
	r.p.logger.Debug("terminate service", "name", in.GetName())

	r.mu.Lock()
	defer r.mu.Unlock()

	v, ok := r.p.processes.LoadAndDelete(in.GetName())
	if !ok {
		return fmt.Errorf("%w: %s", errNoSuchService, in.GetName())
	}
	for _, proc := range v.([]*Process) {
		proc.stop()
	}

	out.Ok = true
	return nil
}

func (r *rpc) Restart(in *serviceV1.Service, out *serviceV1.Response) error {
	name := in.GetName()
	r.p.logger.Debug("restart service", "name", name)

	r.mu.Lock()
	defer r.mu.Unlock()

	procs, err := r.loadProcesses(name)
	if err != nil {
		return err
	}

	// Stop every old process up front; we already hold the write lock, and
	// nothing else writes to the same map entry while we rebuild.
	for i := range procs {
		procs[i].stop()
	}

	newProcs := make([]*Process, 0, len(procs))
	for i := range procs {
		svc := &Service{}
		*svc = *(procs[i]).service

		newProc := NewServiceProcess(svc, name, r.p.logger)
		if err := newProc.start(); err != nil {
			// roll back any already-started replacements so we don't leak processes
			for j := range newProcs {
				newProcs[j].stop()
			}
			r.p.processes.Delete(name)
			return err
		}

		newProcs = append(newProcs, newProc)
	}

	r.p.processes.Store(name, newProcs)
	out.Ok = true
	return nil
}

// Deprecated: use GetStatuses to get correct info.
func (r *rpc) GetStatus(in *serviceV1.Service, out *serviceV1.Status) error {
	r.p.logger.Debug("service status", "name", in.GetName())

	r.mu.RLock()
	defer r.mu.RUnlock()

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

func (r *rpc) GetStatuses(in *serviceV1.Service, out *serviceV1.Statuses) error {
	r.p.logger.Debug("service status", "name", in.GetName())

	r.mu.RLock()
	defer r.mu.RUnlock()

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

func (r *rpc) ListServices(_ *serviceV1.Service, out *serviceV1.List) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	r.p.processes.Range(func(key, _ any) bool {
		r.p.logger.Debug("services list", "service", key.(string))
		out.Services = append(out.Services, key.(string))
		return true
	})

	return nil
}

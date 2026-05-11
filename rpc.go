package service

import (
	"context"
	stderr "errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
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
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("%w: %s", errNoSuchService, name))
	}
	return v.([]*Process), nil
}

func (r *rpc) CreateService(_ context.Context, req *connect.Request[serviceV1.Create]) (*connect.Response[serviceV1.Response], error) {
	in := req.Msg
	r.p.logger.Debug("create service", "name", in.GetName(), "restart_sec", in.GetRestartSec(), "command", in.GetCommand(), "process number", in.GetProcessNum())

	if in.GetProcessNum() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("the service with %s name should have at least 1 process", in.GetName()))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.p.processes.Load(in.GetName()); ok {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("%w: %s", errServiceExists, in.GetName()))
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
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		procs = append(procs, proc)
	}

	r.p.processes.Store(in.GetName(), procs)
	return connect.NewResponse(&serviceV1.Response{Ok: true}), nil
}

func (r *rpc) Terminate(_ context.Context, req *connect.Request[serviceV1.Service]) (*connect.Response[serviceV1.Response], error) {
	in := req.Msg
	r.p.logger.Debug("terminate service", "name", in.GetName())

	r.mu.Lock()
	defer r.mu.Unlock()

	v, ok := r.p.processes.LoadAndDelete(in.GetName())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("%w: %s", errNoSuchService, in.GetName()))
	}
	for _, proc := range v.([]*Process) {
		proc.stop()
	}

	return connect.NewResponse(&serviceV1.Response{Ok: true}), nil
}

func (r *rpc) Restart(_ context.Context, req *connect.Request[serviceV1.Service]) (*connect.Response[serviceV1.Response], error) {
	in := req.Msg
	r.p.logger.Debug("restart service", "name", in.GetName())

	r.mu.Lock()
	defer r.mu.Unlock()

	procs, err := r.loadProcesses(in.GetName())
	if err != nil {
		return nil, err
	}

	newProcs := make([]*Process, len(procs))
	for i := range procs {
		procs[i].stop()

		svc := &Service{}
		*svc = *(procs[i]).service

		newProc := NewServiceProcess(svc, in.GetName(), r.p.logger)
		if err := newProc.start(); err != nil {
			r.p.processes.Delete(in.GetName())
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		newProcs[i] = newProc
		r.p.processes.Delete(in.GetName())
	}

	r.p.processes.Store(in.GetName(), newProcs)
	return connect.NewResponse(&serviceV1.Response{Ok: true}), nil
}

// Deprecated: use GetStatuses to get correct info.
func (r *rpc) GetStatus(_ context.Context, req *connect.Request[serviceV1.Service]) (*connect.Response[serviceV1.Status], error) {
	in := req.Msg
	r.p.logger.Debug("service status", "name", in.GetName())

	r.mu.RLock()
	defer r.mu.RUnlock()

	procs, err := r.loadProcesses(in.GetName())
	if err != nil {
		return nil, err
	}

	out := &serviceV1.Status{}
	for i := range procs {
		state, err := generalProcessState(procs[i].pid, procs[i].command.String())
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		out.Pid = int32(state.Pid) //nolint:gosec
		out.Command = state.Command
		out.CpuPercent = float32(state.CPUPercent)
		out.MemoryUsage = state.MemoryUsage
	}

	return connect.NewResponse(out), nil
}

func (r *rpc) GetStatuses(_ context.Context, req *connect.Request[serviceV1.Service]) (*connect.Response[serviceV1.Statuses], error) {
	in := req.Msg
	r.p.logger.Debug("service status", "name", in.GetName())

	r.mu.RLock()
	defer r.mu.RUnlock()

	procs, err := r.loadProcesses(in.GetName())
	if err != nil {
		return nil, err
	}

	out := &serviceV1.Statuses{}
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

	return connect.NewResponse(out), nil
}

func (r *rpc) ListServices(_ context.Context, _ *connect.Request[serviceV1.Service]) (*connect.Response[serviceV1.List], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := &serviceV1.List{}
	r.p.processes.Range(func(key, _ any) bool {
		r.p.logger.Debug("services list", "service", key.(string))
		out.Services = append(out.Services, key.(string))
		return true
	})

	return connect.NewResponse(out), nil
}

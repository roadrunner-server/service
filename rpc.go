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
	r.p.logger.Debug("create service", "name", req.Msg.GetName(), "restart_sec", req.Msg.GetRestartSec(), "command", req.Msg.GetCommand(), "process number", req.Msg.GetProcessNum())

	if req.Msg.GetProcessNum() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("the service with %s name should have at least 1 process", req.Msg.GetName()))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.p.processes.Load(req.Msg.GetName()); ok {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("%w: %s", errServiceExists, req.Msg.GetName()))
	}

	procs := make([]*Process, 0, req.Msg.GetProcessNum())
	for range int(req.Msg.GetProcessNum()) {
		proc := NewServiceProcess(&Service{
			Command:         req.Msg.GetCommand(),
			ProcessNum:      int(req.Msg.GetProcessNum()),
			ExecTimeout:     time.Second * time.Duration(req.Msg.GetExecTimeout()),
			RemainAfterExit: req.Msg.GetRemainAfterExit(),
			RestartSec:      req.Msg.GetRestartSec(),
			UseServiceName:  req.Msg.GetServiceNameInLogs(),
			TimeoutStopSec:  req.Msg.GetTimeoutStopSec(),
			Env:             req.Msg.GetEnv(),
		}, req.Msg.GetName(), r.p.logger)

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

	r.p.processes.Store(req.Msg.GetName(), procs)
	return connect.NewResponse(&serviceV1.Response{Ok: true}), nil
}

func (r *rpc) Terminate(_ context.Context, req *connect.Request[serviceV1.Service]) (*connect.Response[serviceV1.Response], error) {
	r.p.logger.Debug("terminate service", "name", req.Msg.GetName())

	r.mu.Lock()
	defer r.mu.Unlock()

	v, ok := r.p.processes.LoadAndDelete(req.Msg.GetName())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("%w: %s", errNoSuchService, req.Msg.GetName()))
	}
	for _, proc := range v.([]*Process) {
		proc.stop()
	}

	return connect.NewResponse(&serviceV1.Response{Ok: true}), nil
}

func (r *rpc) Restart(_ context.Context, req *connect.Request[serviceV1.Service]) (*connect.Response[serviceV1.Response], error) {
	r.p.logger.Debug("restart service", "name", req.Msg.GetName())

	r.mu.Lock()
	defer r.mu.Unlock()

	procs, err := r.loadProcesses(req.Msg.GetName())
	if err != nil {
		return nil, err
	}

	newProcs := make([]*Process, len(procs))
	for i := range procs {
		procs[i].stop()

		svc := &Service{}
		*svc = *(procs[i]).service

		newProc := NewServiceProcess(svc, req.Msg.GetName(), r.p.logger)
		if err := newProc.start(); err != nil {
			r.p.processes.Delete(req.Msg.GetName())
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		newProcs[i] = newProc
		r.p.processes.Delete(req.Msg.GetName())
	}

	r.p.processes.Store(req.Msg.GetName(), newProcs)
	return connect.NewResponse(&serviceV1.Response{Ok: true}), nil
}

// Deprecated: use GetStatuses to get correct info.
func (r *rpc) GetStatus(_ context.Context, req *connect.Request[serviceV1.Service]) (*connect.Response[serviceV1.Status], error) {
	r.p.logger.Debug("service status", "name", req.Msg.GetName())

	r.mu.RLock()
	defer r.mu.RUnlock()

	procs, err := r.loadProcesses(req.Msg.GetName())
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
	r.p.logger.Debug("service status", "name", req.Msg.GetName())

	r.mu.RLock()
	defer r.mu.RUnlock()

	procs, err := r.loadProcesses(req.Msg.GetName())
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

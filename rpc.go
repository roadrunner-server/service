package service

import (
	"fmt"
	"sync"
	"time"

	shared "github.com/roadrunner-server/api/v4/build/common/v1"
	serviceV1 "github.com/roadrunner-server/api/v4/build/service/v1"
	"go.uber.org/zap"
)

type rpc struct {
	mu sync.RWMutex
	p  *Plugin
}

func (r *rpc) Create(in *serviceV1.Create, out *serviceV1.Response) error {
	r.p.logger.Debug("create service", zap.String("name", in.GetName()), zap.Uint64("restart_sec", in.GetRestartSec()), zap.String("command", in.GetCommand()), zap.Int64("process number", in.GetProcessNum()))

	if in.GetProcessNum() == 0 {
		return fmt.Errorf("the service with %s name should have at least 1 process", in.GetName())
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	_, ok := r.p.processes.Load(in.GetName())
	if ok {
		return fmt.Errorf("the service with %s name already exists", in.GetName())
	}

	procs := make([]*Process, 0, in.GetProcessNum())

	for i := 0; i < int(in.GetProcessNum()); i++ {
		// create processor structure, which will process all the services
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

		err := proc.start()
		if err != nil {
			// if some process from the group failed -> deallocate the whole group
			if len(procs) > 0 {
				r.p.logger.Warn("stopping already allocated processes")
				for i := 0; i < len(procs); i++ {
					procs[i].stop()
				}
			}
			return err
		}

		procs = append(procs, proc)
	}

	// store all the processes idents
	r.p.processes.Store(in.GetName(), procs)

	out.Ok = true
	return nil
}

func (r *rpc) Terminate(in *serviceV1.Service, out *serviceV1.Response) error {
	r.p.logger.Debug("terminate service", zap.String("name", in.GetName()))

	r.mu.Lock()
	defer r.mu.Unlock()

	_, ok := r.p.processes.Load(in.GetName())
	if !ok {
		return fmt.Errorf("the service with %s name doesn't exist", in.GetName())
	}

	procInterface, ok := r.p.processes.LoadAndDelete(in.GetName())
	if !ok {
		return fmt.Errorf("no such service: %s", in.GetName())
	}

	procs := procInterface.([]*Process)
	for i := 0; i < len(procs); i++ {
		procs[i].stop()
	}

	out.Ok = true
	return nil
}

func (r *rpc) Restart(in *serviceV1.Service, out *serviceV1.Response) error {
	r.p.logger.Debug("restart service", zap.String("name", in.GetName()))

	r.mu.Lock()
	defer r.mu.Unlock()

	_, ok := r.p.processes.Load(in.GetName())
	if !ok {
		return fmt.Errorf("the service with %s name doesn't exist", in.GetName())
	}

	procInterface, ok := r.p.processes.Load(in.GetName())
	if !ok {
		return fmt.Errorf("no such service: %s", in.GetName())
	}

	procs := procInterface.([]*Process)

	newProcs := make([]*Process, len(procs))
	for i := 0; i < len(procs); i++ {
		procs[i].stop()

		service := &Service{}
		*service = *(procs[i]).service

		newProc := NewServiceProcess(service, in.GetName(), r.p.logger)
		err := newProc.start()
		if err != nil {
			r.p.processes.Delete(in.GetName())
			return err
		}

		newProcs[i] = newProc
		r.p.processes.Delete(in.GetName())
	}

	r.p.processes.Store(in.GetName(), newProcs)
	out.Ok = true
	return nil
}

// Status returns status for the service
// Deprecated: use Statuses to get correct info
func (r *rpc) Status(in *serviceV1.Service, out *serviceV1.Status) error {
	r.p.logger.Debug("service status", zap.String("name", in.GetName()))

	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.p.processes.Load(in.GetName())
	if !ok {
		return fmt.Errorf("the service with %s name doesn't exist", in.GetName())
	}

	procInterface, ok := r.p.processes.Load(in.GetName())
	if !ok {
		return fmt.Errorf("no such service: %s", in.GetName())
	}

	procs := procInterface.([]*Process)

	for i := 0; i < len(procs); i++ {
		state, err := generalProcessState(procs[i].pid, procs[i].command.String())
		if err != nil {
			return err
		}

		out.Pid = int32(state.Pid)
		out.Command = state.Command
		out.CpuPercent = float32(state.CPUPercent)
		out.MemoryUsage = state.MemoryUsage
	}

	return nil
}

// Statuses returns status for the service with all processes
func (r *rpc) Statuses(in *serviceV1.Service, out *serviceV1.Statuses) error {
	r.p.logger.Debug("service status", zap.String("name", in.GetName()))

	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.p.processes.Load(in.GetName())
	if !ok {
		return fmt.Errorf("the service with %s name doesn't exist", in.GetName())
	}

	procInterface, ok := r.p.processes.Load(in.GetName())
	if !ok {
		return fmt.Errorf("no such service: %s", in.GetName())
	}

	procs := procInterface.([]*Process)

	for i := 0; i < len(procs); i++ {
		state, err := generalProcessState(procs[i].pid, procs[i].command.String())
		if err != nil {
			/*
				in case of error, just add the error status + common info (pid, command)
			*/
			out.Status = append(out.Status, &serviceV1.Status{
				CpuPercent:  0,
				Pid:         int32(procs[i].pid),
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
			Pid:         int32(state.Pid),
			MemoryUsage: state.MemoryUsage,
			Command:     state.Command,
			Status:      nil,
		})
	}

	return nil
}

func (r *rpc) List(_ *serviceV1.Service, out *serviceV1.List) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	r.p.processes.Range(func(key, value interface{}) bool {
		r.p.logger.Debug("services list", zap.String("service", key.(string)))
		out.Services = append(out.Services, key.(string))
		return true
	})

	return nil
}

package service

import (
	"sync"

	"github.com/roadrunner-server/api/v2/plugins/config"
	"github.com/roadrunner-server/api/v2/state/process"
	"github.com/roadrunner-server/errors"
	"go.uber.org/zap"
)

const PluginName string = "service"

type Plugin struct {
	sync.Mutex

	logger *zap.Logger
	cfg    Config

	// all processes attached to the service
	processes sync.Map // key -> []*Process
}

func (p *Plugin) Init(cfg config.Configurer, log *zap.Logger) error {
	const op = errors.Op("service_plugin_init")
	if !cfg.Has(PluginName) {
		return errors.E(errors.Disabled)
	}
	err := cfg.UnmarshalKey(PluginName, &p.cfg.Services)
	if err != nil {
		return errors.E(op, err)
	}

	// init default parameters if not set by user
	p.cfg.InitDefault()
	// save the logger
	p.logger = log

	return nil
}

func (p *Plugin) Serve() chan error {
	errCh := make(chan error, 1)

	// start processing
	go func() {
		// lock here, because Stop command might be invoked during the Serve
		p.Lock()
		defer p.Unlock()

		for k := range p.cfg.Services {
			// create needed number of the processes
			procs := make([]*Process, p.cfg.Services[k].ProcessNum)

			for i := 0; i < p.cfg.Services[k].ProcessNum; i++ {
				// create processor structure, which will process all the services
				procs[i] = NewServiceProcess(p.cfg.Services[k], p.logger)
			}

			// store all the processes idents
			p.processes.Store(k, procs)
		}

		p.processes.Range(func(key, value any) bool {
			procs := value.([]*Process)

			for i := 0; i < len(procs); i++ {
				cmdStr := procs[i].service.Command
				err := procs[i].start()
				if err != nil {
					errCh <- err
					return false
				}
				p.logger.Info("service have started", zap.String("name", key.(string)), zap.String("command", cmdStr))
			}

			return true
		})
	}()

	return errCh
}

func (p *Plugin) Reset() error {
	p.processes.Range(func(key, value any) bool {
		procs := value.([]*Process)

		newProcs := make([]*Process, len(procs))

		for i := 0; i < len(procs); i++ {
			procs[i].stop()

			service := &Service{}
			*service = *(procs[i]).service

			newProc := NewServiceProcess(service, p.logger)
			err := newProc.start()
			if err != nil {
				p.logger.Error("unable to start the service", zap.String("name", key.(string)))
				p.processes.Delete(key)
				return true
			}

			newProcs[i] = newProc
			procs[i].command.Stderr = nil
			procs[i].command.Stdout = nil
			procs[i] = nil
			p.processes.Delete(key)
		}

		p.processes.Store(key, newProcs)
		return true
	})

	return nil
}

func (p *Plugin) Workers() []*process.State {
	p.Lock()
	defer p.Unlock()
	states := make([]*process.State, 0, 5)

	p.processes.Range(func(key, value interface{}) bool {
		k := key.(string)
		procs := value.([]*Process)

		for i := 0; i < len(procs); i++ {
			st, err := generalProcessState(procs[i].pid, procs[i].command.String())
			if err != nil {
				p.logger.Error("get process state", zap.String("name", k), zap.String("command", procs[i].command.String()))
				return true
			}
			states = append(states, st)
		}

		return true
	})

	return states
}

func (p *Plugin) Stop() error {
	p.processes.Range(func(key, value interface{}) bool {
		k := key.(string)
		procs := value.([]*Process)

		for i := 0; i < len(procs); i++ {
			procs[i].stop()

			p.logger.Info("service have stopped", zap.String("name", k), zap.String("command", procs[i].service.Command))
			p.processes.Delete(key)
		}

		return true
	})

	return nil
}

// Name contains service name.
func (p *Plugin) Name() string {
	return PluginName
}

func (p *Plugin) RPC() interface{} {
	return &rpc{p: p}
}

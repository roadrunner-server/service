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
	processes sync.Map // uuid -> *Process
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
			for i := 0; i < p.cfg.Services[k].ProcessNum; i++ {
				// create processor structure, which will process all the services
				p.processes.Store(k, NewServiceProcess(p.cfg.Services[k], p.logger))
			}
		}

		p.processes.Range(func(key, value interface{}) bool {
			proc := value.(*Process)

			err := proc.start()
			if err != nil {
				errCh <- err
				return false
			}
			p.logger.Info("service have started", zap.String("name", key.(string)), zap.String("command", proc.command.String()))
			return true
		})
	}()

	return errCh
}

func (p *Plugin) Workers() []*process.State {
	p.Lock()
	defer p.Unlock()
	states := make([]*process.State, 0, 5)

	p.processes.Range(func(key, value interface{}) bool {
		k := key.(string)
		proc := value.(*Process)

		st, err := generalProcessState(proc.pid, proc.command.String())
		if err != nil {
			p.logger.Error("get process state", zap.String("name", k), zap.String("command", proc.command.String()))
			return true
		}
		states = append(states, st)

		return true
	})

	return states
}

func (p *Plugin) Stop() error {
	p.processes.Range(func(key, value interface{}) bool {
		k := key.(string)
		proc := value.(*Process)

		proc.stop()

		p.logger.Info("service have stopped", zap.String("name", k), zap.String("command", proc.service.Command))
		p.processes.Delete(key)
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

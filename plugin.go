package service

import (
	"context"
	"sync"

	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/pool/state/process"
	"go.uber.org/zap"
)

const PluginName string = "service"

type Plugin struct {
	mu sync.Mutex

	logger *zap.Logger
	cfg    Config

	// all processes attached to the service
	processes sync.Map // key -> []*Process
}

type Configurer interface {
	// UnmarshalKey takes a single key and unmarshal it into a Struct.
	UnmarshalKey(name string, out any) error
	// Has checks if a config section exists.
	Has(name string) bool
}

type Logger interface {
	NamedLogger(name string) *zap.Logger
}

func (p *Plugin) Init(cfg Configurer, log Logger) error {
	const op = errors.Op("service_plugin_init")
	if !cfg.Has(PluginName) {
		return errors.E(errors.Disabled)
	}
	err := cfg.UnmarshalKey(PluginName, &p.cfg.Services)
	if err != nil {
		return errors.E(op, err)
	}

	// init default parameters if not set by the user
	p.cfg.InitDefault()

	// save the logger
	p.logger = log.NamedLogger(PluginName)

	return nil
}

func (p *Plugin) Serve() chan error {
	errCh := make(chan error, 1)

	// start processing
	go func() {
		// lock here, because the Stop command might be invoked during the Serve
		p.mu.Lock()
		defer p.mu.Unlock()

		for k := range p.cfg.Services {
			// create the necessary number of the processes
			procs := make([]*Process, p.cfg.Services[k].ProcessNum)

			for i := range p.cfg.Services[k].ProcessNum {
				// create a processor structure, which will process all the services
				procs[i] = NewServiceProcess(p.cfg.Services[k], k, p.logger)
			}

			// store all the processes idents
			p.processes.Store(k, procs)
		}

		p.processes.Range(func(key, value any) bool {
			procs := value.([]*Process)

			for i := range procs {
				cmdStr := procs[i].service.Command
				err := procs[i].start()
				if err != nil {
					errCh <- err
					return false
				}
				p.logger.Info("service was started", zap.String("name", key.(string)), zap.String("command", cmdStr))
			}

			return true
		})
	}()

	return errCh
}

func (p *Plugin) Weight() uint {
	return 10
}

func (p *Plugin) Reset() error {
	p.processes.Range(func(key, value any) bool {
		procs := value.([]*Process)

		newProcs := make([]*Process, len(procs))

		for i := range procs {
			procs[i].stop()
			p.processes.Delete(key)

			service := &Service{}
			*service = *(procs[i]).service

			newProc := NewServiceProcess(service, key.(string), p.logger)
			err := newProc.start()
			if err != nil {
				p.logger.Error("unable to start the service", zap.String("name", key.(string)))
				return true
			}

			newProcs[i] = newProc
		}

		p.processes.Store(key, newProcs)
		return true
	})

	return nil
}

func (p *Plugin) Workers() []*process.State {
	p.mu.Lock()
	defer p.mu.Unlock()
	states := make([]*process.State, 0, 5)

	p.processes.Range(func(key, value any) bool {
		k := key.(string)
		procs := value.([]*Process)

		for i := range procs {
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

func (p *Plugin) Stop(context.Context) error {
	p.processes.Range(func(key, value any) bool {
		k := key.(string)
		procs := value.([]*Process)

		for i := range procs {
			procs[i].stop()

			p.logger.Info("service was stopped", zap.String("name", k), zap.String("command", procs[i].service.Command))
			p.processes.Delete(key)
		}

		return true
	})

	return nil
}

// Name contains the service name.
func (p *Plugin) Name() string {
	return PluginName
}

func (p *Plugin) RPC() any {
	return &rpc{p: p}
}

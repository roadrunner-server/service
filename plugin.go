package service

import (
	"context"
	"log/slog"
	"sync"

	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/pool/v2/state/process"
)

const PluginName string = "service"

type Plugin struct {
	// mu serializes explicit lifecycle operations. Groups protect configuration.
	mu      sync.Mutex
	stopped bool

	logger *slog.Logger
	cfg    Config

	processes sync.Map // name -> *group
}

type Configurer interface {
	// UnmarshalKey takes a single key and unmarshal it into a Struct.
	UnmarshalKey(name string, out any) error
	// Has checks if a config section exists.
	Has(name string) bool
}

type Logger interface {
	NamedLogger(name string) *slog.Logger
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
	p.mu.Lock()
	defer p.mu.Unlock()

	errCh := make(chan error, 1)
	for name, svc := range p.cfg.Services {
		if _, exists := p.processes.Load(name); exists {
			continue
		}
		g := newGroup(svc, name, p.logger)
		p.processes.Store(name, g)
		if err := g.start(); err != nil {
			stopProcesses(g.pause())
			errCh <- err
			return errCh
		}
	}

	return errCh
}

func (p *Plugin) Weight() uint {
	return 10
}

func (p *Plugin) Reset() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.processes.Range(func(key, value any) bool {
		g := value.(*group)
		if err := g.restart(); err != nil {
			g.stop()
			p.processes.Delete(key)
			p.logger.Error("unable to start the service", "name", key.(string), "error", err)
		}
		return true
	})

	return nil
}

func (p *Plugin) Workers() []*process.State {
	states := make([]*process.State, 0, 5)

	p.processes.Range(func(key, value any) bool {
		k := key.(string)
		procs := value.(*group).snapshot()

		for i := range procs {
			st, err := generalProcessState(procs[i].pid, procs[i].command.String())
			if err != nil {
				p.logger.Error("get process state", "name", k, "command", procs[i].command.String())
				continue
			}
			states = append(states, st)
		}

		return true
	})

	return states
}

func (p *Plugin) Stop(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped = true
	p.processes.Range(func(key, value any) bool {
		g := value.(*group)
		g.stop()
		p.processes.Delete(key)
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

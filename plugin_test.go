package service

import (
	"context"
	stderr "errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/pool/v2/state/process"
	"github.com/stretchr/testify/require"
)

// testConfigurer feeds Init a service section without going through viper.
type testConfigurer struct {
	has      bool
	services map[string]*Service
	err      error
}

func (c *testConfigurer) Has(string) bool { return c.has }

func (c *testConfigurer) UnmarshalKey(_ string, out any) error {
	if c.err != nil {
		return c.err
	}

	dst, ok := out.(*map[string]*Service)
	if !ok {
		return fmt.Errorf("unexpected destination type %T", out)
	}

	*dst = c.services
	return nil
}

type testLogger struct {
	l *slog.Logger
}

func (l *testLogger) NamedLogger(string) *slog.Logger { return l.l }

func TestPluginInitDisabled(t *testing.T) {
	log, _ := newCaptureLogger()
	p := &Plugin{}

	err := p.Init(&testConfigurer{has: false}, &testLogger{l: log})

	require.Error(t, err)
	require.True(t, errors.Is(errors.Disabled, err))
}

func TestPluginInitUnmarshalError(t *testing.T) {
	log, _ := newCaptureLogger()
	p := &Plugin{}

	err := p.Init(&testConfigurer{has: true, err: stderr.New("broken section")}, &testLogger{l: log})

	require.ErrorContains(t, err, "service_plugin_init")
	require.ErrorContains(t, err, "broken section")
}

func TestPluginInitAppliesDefaults(t *testing.T) {
	log, _ := newCaptureLogger()
	p := &Plugin{}

	cfg := &testConfigurer{has: true, services: map[string]*Service{
		"some_service": {Command: "sleep 30"},
	}}
	require.NoError(t, p.Init(cfg, &testLogger{l: log}))

	require.Equal(t, 1, p.cfg.Services["some_service"].ProcessNum)
	require.EqualValues(t, 30, p.cfg.Services["some_service"].RestartSec)
	require.EqualValues(t, 5, p.cfg.Services["some_service"].TimeoutStopSec)
}

func TestPluginGetters(t *testing.T) {
	p := &Plugin{}

	require.Equal(t, PluginName, p.Name())
	require.EqualValues(t, 10, p.Weight())
	require.IsType(t, &rpc{}, p.RPC())
}

func TestPluginServeAndStop(t *testing.T) {
	p, store := newTestPlugin(t, map[string]*Service{
		"some_service": {Command: "sleep 30", ProcessNum: 2},
	})

	errCh := p.Serve()
	requireWorkers(t, p, 2)
	require.Empty(t, errCh)

	for _, st := range p.Workers() {
		require.NotZero(t, st.Pid)
		require.Contains(t, st.Command, "sleep")
		require.NotZero(t, st.MemoryUsage)
	}

	require.NoError(t, p.Stop(context.Background()))

	require.Equal(t, 2, store.count("service was started"))
	require.Equal(t, 2, store.count("service was stopped"))
	require.Empty(t, p.Workers())
}

func TestPluginServeReportsStartError(t *testing.T) {
	p, store := newTestPlugin(t, map[string]*Service{
		"some_service": {Command: filepath.Join(t.TempDir(), "no-such-binary"), ProcessNum: 1},
	})

	errCh := p.Serve()

	select {
	case err := <-errCh:
		require.Error(t, err)
	case <-time.After(time.Second * 10):
		require.Fail(t, "the start error was not pushed to the error channel")
	}

	// the process was stored before it was started, but it has no pid
	require.Empty(t, p.Workers())
	require.Equal(t, 1, store.count("get process state"))
}

func TestPluginResetReplacesProcesses(t *testing.T) {
	p, _ := newTestPlugin(t, map[string]*Service{
		"some_service": {Command: "sleep 30", ProcessNum: 2},
	})

	p.Serve()
	requireWorkers(t, p, 2)
	before := workerPids(p.Workers())

	require.NoError(t, p.Reset())

	after := workerPids(p.Workers())
	require.Len(t, after, 2)
	for _, pid := range after {
		require.NotContains(t, before, pid)
	}
}

func TestPluginResetLogsFailedRestart(t *testing.T) {
	p, store := newTestPlugin(t, map[string]*Service{
		"some_service": {Command: "sleep 30", ProcessNum: 1},
	})

	p.Serve()
	requireWorkers(t, p, 1)

	// the replacement is built from the stored service, which now points at
	// something that cannot be executed
	v, ok := p.processes.Load("some_service")
	require.True(t, ok)
	v.([]*Process)[0].service.Command = filepath.Join(t.TempDir(), "no-such-binary")

	require.NoError(t, p.Reset())

	require.Equal(t, 1, store.count("unable to start the service"))
	require.Empty(t, p.Workers())
}

// newTestPlugin returns a plugin wired to an in-memory logger, stopped at the
// end of the test.
func newTestPlugin(t *testing.T, services map[string]*Service) (*Plugin, *captureStore) {
	t.Helper()

	log, store := newCaptureLogger()
	p := &Plugin{logger: log, cfg: Config{Services: services}}
	t.Cleanup(func() { _ = p.Stop(context.Background()) })

	return p, store
}

// requireWorkers waits until the plugin reports n live processes.
func requireWorkers(t *testing.T, p *Plugin, n int) {
	t.Helper()

	require.Eventually(t, func() bool {
		return len(p.Workers()) == n
	}, time.Second*10, time.Millisecond*20, "the plugin did not report %d processes", n)
}

// workerPids collects the pids the plugin reports.
func workerPids(states []*process.State) []int64 {
	pids := make([]int64, 0, len(states))
	for _, st := range states {
		pids = append(pids, st.Pid)
	}

	return pids
}

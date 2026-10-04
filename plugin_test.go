package service

import (
	"context"
	stderr "errors"
	"fmt"
	"log/slog"
	"os"
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

	require.True(t, errors.Is(errors.Disabled, err))
}

func TestPluginInitUnmarshalError(t *testing.T) {
	log, _ := newCaptureLogger()
	p := &Plugin{}

	err := p.Init(&testConfigurer{has: true, err: stderr.New("broken section")}, &testLogger{l: log})

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
}

func TestPluginServeAndStop(t *testing.T) {
	p, _ := newTestPlugin(t, map[string]*Service{
		"some_service": {Command: "sleep 30", ProcessNum: 2},
	})

	errCh := p.Serve()
	workers := p.Workers()
	require.Len(t, workers, 2)
	require.Empty(t, errCh)

	for _, st := range workers {
		require.NotZero(t, st.Pid)
	}

	require.NoError(t, p.Stop(t.Context()))

	require.Empty(t, p.Workers())
	for _, st := range workers {
		require.Eventually(t, func() bool { return !processAlive(st.Pid) },
			10*time.Second, 20*time.Millisecond)
	}
}

func TestPluginServeReportsStartError(t *testing.T) {
	p, _ := newTestPlugin(t, map[string]*Service{
		"some_service": {Command: filepath.Join(t.TempDir(), "no-such-binary"), ProcessNum: 1},
	})

	errCh := p.Serve()

	select {
	case err := <-errCh:
		require.Error(t, err)
	default:
		t.Fatal("Serve returned before reporting the start error")
	}

	require.Empty(t, p.Workers())
}

func TestPluginResetReplacesProcesses(t *testing.T) {
	p, _ := newTestPlugin(t, map[string]*Service{
		"some_service": {Command: "sleep 30", ProcessNum: 2},
	})

	p.Serve()
	before := workerPids(p.Workers())
	require.Len(t, before, 2)

	require.NoError(t, p.Reset())

	after := workerPids(p.Workers())
	require.Len(t, after, 2)
	for _, pid := range after {
		require.NotContains(t, before, pid)
	}
}

func TestPluginResetLogsFailedRestart(t *testing.T) {
	script := writeScript(t, "reset.sh", "#!/bin/sh\necho ready\nexec sleep 30\n")
	p, store := newTestPlugin(t, map[string]*Service{
		"some_service": {Command: script, ProcessNum: 1},
	})

	p.Serve()
	require.Eventually(t, func() bool { return store.count("ready") == 1 },
		10*time.Second, 20*time.Millisecond)
	require.NoError(t, os.Remove(script))

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

// workerPids collects the pids the plugin reports.
func workerPids(states []*process.State) []int64 {
	pids := make([]int64, 0, len(states))
	for _, st := range states {
		pids = append(pids, st.Pid)
	}

	return pids
}

package helpers

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	mocklogger "tests/mock"

	"github.com/roadrunner-server/config/v6"
	"github.com/roadrunner-server/endure/v2"
	"github.com/stretchr/testify/require"
)

const (
	// defaultConfigVersion is the config schema version used by the test configs.
	defaultConfigVersion = "2024.2.0"
	// serviceStarted is written once per process, right after it is spawned.
	serviceStarted = "service was started"

	// probeTimeout caps how long Start waits for the rpc listener to accept.
	probeTimeout = time.Second * 15
	probeTick    = time.Millisecond * 20
	probeDial    = time.Second

	// logsTimeout caps how long WaitLogs waits for the expected records.
	logsTimeout = time.Second * 30
	logsTick    = time.Millisecond * 20
)

// bootCfg holds the options applied to a container before it is started.
type bootCfg struct {
	inline          string
	graceful        time.Duration
	servicesStarted int
	probeAddr       string
}

// Option customizes the container built by Start and its error-path variants.
type Option func(*bootCfg)

// WithInlineConfig feeds the container YAML from memory; the cfgPath argument is ignored.
func WithInlineConfig(yaml string) Option {
	return func(b *bootCfg) { b.inline = yaml }
}

// WithGracefulTimeout sets the endure graceful shutdown timeout.
func WithGracefulTimeout(d time.Duration) Option {
	return func(b *bootCfg) { b.graceful = d }
}

// WithServicesStarted makes Start return only once n processes have been spawned.
func WithServicesStarted(n int) Option {
	return func(b *bootCfg) { b.servicesStarted = n }
}

// WithTCPProbe makes Start return only once addr accepts a connection, which is
// what the tests talking to the rpc plugin need.
func WithTCPProbe(addr string) Option {
	return func(b *bootCfg) { b.probeAddr = addr }
}

// RR is a running container.
type RR struct {
	// Logs holds the records captured by the in-memory logger.
	Logs *mocklogger.ObservedLogs

	mu   sync.Mutex
	errs []error
}

// Errs returns the errors the container reported on its error channel so far.
func (rr *RR) Errs() []error {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return append([]error(nil), rr.errs...)
}

func (rr *RR) addErr(err error) {
	rr.mu.Lock()
	rr.errs = append(rr.errs, err)
	rr.mu.Unlock()
}

// Count returns the number of captured records whose message contains the snippet.
func (rr *RR) Count(snippet string) int {
	return rr.Logs.FilterMessageSnippet(snippet).Len()
}

// WaitLogs blocks until at least n captured records contain the snippet.
func (rr *RR) WaitLogs(t *testing.T, snippet string, n int) {
	t.Helper()

	require.Eventually(t, func() bool {
		return rr.Count(snippet) >= n
	}, logsTimeout, logsTick, "expected at least %d log records containing %q", n, snippet)
}

// CountExact returns the number of captured records whose message is exactly msg.
func (rr *RR) CountExact(msg string) int {
	return rr.Logs.FilterMessage(msg).Len()
}

// WaitLogsExact blocks until at least n captured records have exactly the message msg.
func (rr *RR) WaitLogsExact(t *testing.T, msg string, n int) {
	t.Helper()

	require.Eventually(t, func() bool {
		return rr.CountExact(msg) >= n
	}, logsTimeout, logsTick, "expected at least %d log records with the message %q", n, msg)
}

// Start registers the plugins, boots the container and waits for the readiness
// options, if any, to be satisfied. Errors arriving on the container channel are
// reported through t.Errorf and stop the container, but they do not abort the test.
//
// The returned stop is idempotent and also registered with t.Cleanup, so tests
// asserting on logs written during shutdown can stop the container mid-test.
func Start(t *testing.T, cfgPath string, plugins []any, opts ...Option) (*RR, func()) {
	t.Helper()

	cont, rr, bc := newContainer(t, cfgPath, plugins, opts)
	require.NoError(t, cont.Init())

	ch, err := cont.Serve()
	require.NoError(t, err)

	stopCont := sync.OnceValue(cont.Stop)
	done := make(chan struct{})
	wg := &sync.WaitGroup{}

	wg.Go(func() {
		for {
			select {
			case res := <-ch:
				if res == nil {
					return
				}
				rr.addErr(res.Error)
				t.Errorf("plugin %s reported an error: %v", res.VertexID, res.Error)
				if errS := stopCont(); errS != nil {
					t.Errorf("container stop: %v", errS)
				}
			case <-done:
				if errS := stopCont(); errS != nil {
					t.Errorf("container stop: %v", errS)
				}
				return
			}
		}
	})

	// The drain goroutine calls t.Errorf, so it has to be joined while the test
	// is still running.
	stop := sync.OnceFunc(func() {
		close(done)
		wg.Wait()
	})
	t.Cleanup(stop)

	if bc.servicesStarted > 0 {
		rr.WaitLogs(t, serviceStarted, bc.servicesStarted)
	}

	if bc.probeAddr != "" {
		require.Eventually(t, func() bool { return dialOK(t.Context(), bc.probeAddr) },
			probeTimeout, probeTick, "rpc listener %s did not become ready", bc.probeAddr)
	}

	return rr, stop
}

// StartExpectInitError registers the plugins and requires Init to fail, returning its error.
func StartExpectInitError(t *testing.T, cfgPath string, plugins []any, opts ...Option) error {
	t.Helper()

	cont, _, _ := newContainer(t, cfgPath, plugins, opts)

	err := cont.Init()
	require.Error(t, err)

	return err
}

// newContainer builds the container and registers the config, the in-memory
// logger and the caller's plugins. The container is not initialized yet.
func newContainer(t *testing.T, cfgPath string, plugins []any, opts []Option) (*endure.Endure, *RR, *bootCfg) {
	t.Helper()

	bc := &bootCfg{}
	for _, o := range opts {
		o(bc)
	}

	cfg := &config.Plugin{Version: defaultConfigVersion}
	if bc.inline != "" {
		cfg.Type = "yaml"
		cfg.ReadInCfg = []byte(bc.inline)
	} else {
		cfg.Path = cfgPath
	}

	var endureOpts []endure.Options
	if bc.graceful != 0 {
		endureOpts = append(endureOpts, endure.GracefulShutdownTimeout(bc.graceful))
	}

	// The service plugin owns no listener, so the log stream is the only place
	// its behavior shows up: every test gets the observed logger.
	l, obs := mocklogger.SlogTestLogger(slog.LevelDebug)
	rr := &RR{Logs: obs}

	cont := endure.New(slog.LevelDebug, endureOpts...)
	require.NoError(t, cont.RegisterAll(append([]any{cfg, l}, plugins...)...))

	return cont, rr, bc
}

// dialOK reports whether addr accepts a tcp connection.
func dialOK(ctx context.Context, addr string) bool {
	d := net.Dialer{Timeout: probeDial}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}

	_ = conn.Close()
	return true
}

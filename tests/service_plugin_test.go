package service

import (
	"log/slog"
	"net"
	"net/rpc"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	mocklogger "tests/mock"

	informerProto "github.com/roadrunner-server/api-go/v6/informer/v1"
	resetterProto "github.com/roadrunner-server/api-go/v6/resetter/v1"
	serviceProto "github.com/roadrunner-server/api-go/v6/service/v1"
	"github.com/roadrunner-server/config/v6"
	"github.com/roadrunner-server/endure/v2"
	goridgeRpc "github.com/roadrunner-server/goridge/v4/pkg/rpc"
	"github.com/roadrunner-server/informer/v6"
	"github.com/roadrunner-server/logger/v6"
	"github.com/roadrunner-server/resetter/v6"
	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/service/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const serviceRPCAddr = "127.0.0.1:6001"

func newRPCClient(t *testing.T, address string) *rpc.Client {
	t.Helper()
	var (
		conn net.Conn
		err  error
	)
	d := net.Dialer{Timeout: 5 * time.Second}
	// simple dial retry loop: the goridge RPC server may not be accepting yet
	// right after Serve, or during the port handoff between sequential tests.
	for range 10 {
		conn, err = d.DialContext(t.Context(), "tcp", address)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, err)
	return rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))
}

func TestServiceInit(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-init.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&service.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second * 10)
	stopCh <- struct{}{}
	wg.Wait()
}

func TestServicePHPCreate(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.2.0",
		Path:    "configs/.rr-service-from-php.yaml",
	}

	l, oLogger := mocklogger.SlogTestLogger(slog.LevelDebug)
	err := cont.RegisterAll(
		l,
		cfg,
		&rpcPlugin.Plugin{},
		&service.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second * 15)
	stopCh <- struct{}{}
	wg.Wait()
	time.Sleep(time.Second)

	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("service was stopped").Len(), 4)
}

func TestServiceTrimOutput(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-newlines.yaml",
	}

	l, oLogger := mocklogger.SlogTestLogger(slog.LevelDebug)
	err := cont.RegisterAll(
		cfg,
		l,
		&service.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second * 5)
	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 1, oLogger.FilterMessageSnippet("stdout write").Len())
}

func TestServiceWorkers(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-workers.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&service.Plugin{},
		&informer.Plugin{},
		&rpcPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second)
	t.Run("workers", workers("service"))
	time.Sleep(time.Second)
	stopCh <- struct{}{}
	wg.Wait()
}

func TestServiceInitStdout(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-init-stdout.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&service.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second * 5)
	stopCh <- struct{}{}
	wg.Wait()
}

func TestServiceEnv(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-env.yaml",
	}

	l, oLogger := mocklogger.SlogTestLogger(slog.LevelDebug)
	err := cont.RegisterAll(
		cfg,
		l,
		&service.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second * 5)
	stopCh <- struct{}{}
	wg.Wait()
	require.Equal(t, 0, oLogger.FilterMessageSnippet("faillll").Len())
}

func TestServiceError(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-error.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&service.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	assert.NoError(t, err)
	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second * 10)
	stopCh <- struct{}{}
	wg.Wait()
}

func TestServiceRestarts(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-restarts.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&service.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second * 10)
	stopCh <- struct{}{}
	wg.Wait()
}

func TestServiceCreate(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-create.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&service.Plugin{},
		&rpcPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second)

	in := &serviceProto.Create{
		Name:            "foo",
		Command:         "php php_test_files/loop.php",
		ProcessNum:      1,
		ExecTimeout:     3,
		RemainAfterExit: false,
		Env:             map[string]string{"foo": "bar"},
		RestartSec:      0,
	}

	out := &serviceProto.Response{}

	t.Run("create", create(in, out))

	time.Sleep(time.Second * 2)

	out = &serviceProto.Response{}
	t.Run("terminate", terminate("127.0.0.1:6001", &serviceProto.Service{Name: "foo"}, out))

	stopCh <- struct{}{}
	wg.Wait()
}

func TestServiceCreateEmptyConfig(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-create-empty.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&service.Plugin{},
		&rpcPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second)

	in := &serviceProto.Create{
		Name:            "foo",
		Command:         "php php_test_files/loop.php",
		ProcessNum:      1,
		ExecTimeout:     3,
		RemainAfterExit: false,
		Env:             map[string]string{"foo": "bar"},
		RestartSec:      0,
	}

	out := &serviceProto.Response{}

	t.Run("create", create(in, out))

	time.Sleep(time.Second * 3)
	l := &serviceProto.List{}
	t.Run("list", list("127.0.0.1:6001", &serviceProto.Service{}, l))

	for i := range len(l.GetServices()) {
		cmd := &serviceProto.Service{
			Name: l.GetServices()[i],
		}

		out = &serviceProto.Response{}

		t.Run("terminate", terminate("127.0.0.1:6001", cmd, out))
	}

	time.Sleep(time.Second * 2)
	stopCh <- struct{}{}
	wg.Wait()
}

func TestServiceRestart(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-create-empty.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&service.Plugin{},
		&rpcPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second)

	in := &serviceProto.Create{
		Name:            "foo",
		Command:         "php php_test_files/loop.php",
		ProcessNum:      1,
		ExecTimeout:     3,
		RemainAfterExit: false,
		Env:             map[string]string{"foo": "bar"},
		RestartSec:      0,
	}

	out := &serviceProto.Response{}

	t.Run("create", create(in, out))

	time.Sleep(time.Second * 3)
	l := &serviceProto.List{}
	t.Run("list", list("127.0.0.1:6001", &serviceProto.Service{}, l))

	for i := range len(l.GetServices()) {
		cmd := &serviceProto.Service{
			Name: l.GetServices()[i],
		}

		out = &serviceProto.Response{}

		t.Run("restart", restart(cmd, out))

		time.Sleep(time.Second * 5)
	}

	time.Sleep(time.Second * 2)
	out = &serviceProto.Response{}
	t.Run("terminate", terminate("127.0.0.1:6001", &serviceProto.Service{Name: "foo"}, out))
	stopCh <- struct{}{}
	wg.Wait()
}

func TestServiceRestartConcurrent(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-create-empty.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&service.Plugin{},
		&rpcPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second)

	in := &serviceProto.Create{
		Name:            "foo",
		Command:         "php php_test_files/loop.php",
		ProcessNum:      1,
		ExecTimeout:     3,
		RemainAfterExit: false,
		Env:             map[string]string{"foo": "bar"},
		RestartSec:      0,
	}

	out := &serviceProto.Response{}

	t.Run("create", create(in, out))

	time.Sleep(time.Second)

	l := &serviceProto.List{}
	t.Run("list", list("127.0.0.1:6001", nil, l))

	for range 100 {
		go func() {
			for i := range len(l.GetServices()) {
				cmd := &serviceProto.Service{
					Name: l.GetServices()[i],
				}

				out1 := &serviceProto.Response{}

				t.Run("restart", restart(cmd, out1))

				time.Sleep(time.Second)
			}
		}()

		go func() {
			for i := range len(l.GetServices()) {
				cmd := &serviceProto.Service{
					Name: l.GetServices()[i],
				}

				out2 := &serviceProto.Response{}

				t.Run("restart", restart(cmd, out2))

				time.Sleep(time.Second)
			}
		}()
	}

	time.Sleep(time.Second * 10)
	out = &serviceProto.Response{}
	t.Run("terminate", terminate("127.0.0.1:6001", &serviceProto.Service{Name: "foo"}, out))
	stopCh <- struct{}{}
	wg.Wait()
}

func TestServiceListConcurrent(t *testing.T) {
	cont := endure.New(slog.LevelDebug, endure.GracefulShutdownTimeout(time.Second*5))

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-create-empty.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&service.Plugin{},
		&rpcPlugin.Plugin{},
	)
	assert.NoError(t, err)

	require.NoError(t, cont.Init())

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second)

	in := &serviceProto.Create{
		Name:            "foo",
		Command:         "php php_test_files/loop.php",
		ProcessNum:      1,
		ExecTimeout:     3,
		RemainAfterExit: false,
		Env:             map[string]string{"foo": "bar"},
		RestartSec:      0,
	}

	out := &serviceProto.Response{}

	t.Run("create", create(in, out))

	time.Sleep(time.Second)

	l := &serviceProto.List{}
	t.Run("list", list("127.0.0.1:6001", nil, l))

	for range 100 {
		go func() {
			for i := range len(l.GetServices()) {
				cmd := &serviceProto.Service{
					Name: l.GetServices()[i],
				}

				out1 := &serviceProto.Response{}

				t.Run("restart", restart(cmd, out1))
				ll := &serviceProto.List{}
				t.Run("list", list("127.0.0.1:6001", nil, ll))
				require.Len(t, ll.GetServices(), 1)

				time.Sleep(time.Millisecond * 100)
			}
		}()

		go func() {
			for i := range len(l.GetServices()) {
				cmd := &serviceProto.Service{
					Name: l.GetServices()[i],
				}

				out2 := &serviceProto.Response{}
				t.Run("restart", restart(cmd, out2))
				ll := &serviceProto.List{}
				t.Run("list", list("127.0.0.1:6001", nil, ll))
				require.Len(t, ll.GetServices(), 1)

				time.Sleep(time.Millisecond * 100)
			}
		}()
	}

	time.Sleep(time.Second * 15)
	out = &serviceProto.Response{}
	t.Run("terminate", terminate("127.0.0.1:6001", &serviceProto.Service{Name: "foo"}, out))
	stopCh <- struct{}{}
	wg.Wait()
}

func TestServiceStatus(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-create-empty.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&service.Plugin{},
		&rpcPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second)

	in := &serviceProto.Create{
		Name:            "foo",
		Command:         "php php_test_files/loop.php",
		ProcessNum:      1,
		ExecTimeout:     10,
		RemainAfterExit: false,
		Env:             map[string]string{"foo": "bar"},
		RestartSec:      0,
	}

	out := &serviceProto.Response{}

	t.Run("create", create(in, out))

	time.Sleep(time.Second)

	l := &serviceProto.List{}
	t.Run("list", list("127.0.0.1:6001", nil, l))
	require.Len(t, l.GetServices(), 1)

	inStat := &serviceProto.Service{
		Name: l.GetServices()[0],
	}

	outStat := &serviceProto.Statuses{}
	t.Run("stats", status(inStat, outStat))
	require.NotEmpty(t, outStat.Status[0].GetCommand())
	require.NotZero(t, outStat.Status[0].GetMemoryUsage())
	require.NotZero(t, outStat.Status[0].GetPid())

	out = &serviceProto.Response{}
	t.Run("terminate", terminate("127.0.0.1:6001", &serviceProto.Service{Name: l.GetServices()[0]}, out))

	time.Sleep(time.Second * 2)
	stopCh <- struct{}{}
	wg.Wait()
}

func TestServiceInitRemain(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-init-remain.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&service.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second * 10)
	stopCh <- struct{}{}
	wg.Wait()
}

func TestServiceReset(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-reset.yaml",
	}

	l, oLogger := mocklogger.SlogTestLogger(slog.LevelDebug)
	err := cont.RegisterAll(
		cfg,
		&rpcPlugin.Plugin{},
		&resetter.Plugin{},
		l,
		&service.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second * 2)

	client := newRPCClient(t, serviceRPCAddr)
	resp := &resetterProto.Response{}
	require.NoError(t, client.Call("resetter.Reset", &resetterProto.ResetRequest{Plugin: "service"}, resp))
	require.True(t, resp.GetOk())
	_ = client.Close()

	time.Sleep(time.Second * 5)
	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 20, oLogger.FilterMessageSnippet("The number is: 0").Len())
	require.Equal(t, 20, oLogger.FilterMessageSnippet("Hello 0").Len())
}

func TestServiceReset2(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-reload-22.yaml",
	}

	l, oLogger := mocklogger.SlogTestLogger(slog.LevelDebug)
	err := cont.RegisterAll(
		cfg,
		l,
		&rpcPlugin.Plugin{},
		&resetter.Plugin{},
		&service.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second * 2)

	file, err := os.Create("foo.txt")
	require.NoError(t, err)

	go func() {
		d := net.Dialer{Timeout: 5 * time.Second}
		conn, dialErr := d.DialContext(t.Context(), "tcp", "127.0.0.1:6112")
		// assert (not require) inside goroutine: assert routes through t.Errorf,
		// which is documented safe for concurrent use; require.FailNow would only
		// terminate this goroutine and silently let the test pass.
		if !assert.NoError(t, dialErr) {
			return
		}
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))
		defer func() { _ = client.Close() }()
		resp := &resetterProto.Response{}
		callErr := client.Call("resetter.Reset", &resetterProto.ResetRequest{Plugin: "service"}, resp)
		assert.NoError(t, callErr)
		if callErr == nil {
			assert.True(t, resp.GetOk())
		}
	}()

	time.Sleep(time.Second * 10)
	stopCh <- struct{}{}
	wg.Wait()

	assert.LessOrEqual(t, oLogger.FilterMessageSnippet("The number is: 0").Len(), 30)
	assert.LessOrEqual(t, oLogger.FilterMessageSnippet("Hello 0").Len(), 30)

	t.Cleanup(func() {
		_ = file.Close()
		_ = os.Remove("foo.txt")
	})
}

func TestServiceReset3(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-reload-2.yaml",
	}

	l, oLogger := mocklogger.SlogTestLogger(slog.LevelDebug)
	err := cont.RegisterAll(
		cfg,
		l,
		&rpcPlugin.Plugin{},
		&resetter.Plugin{},
		&service.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second * 2)

	file, err := os.Create("foo.txt")
	require.NoError(t, err)

	time.Sleep(time.Second * 4)
	stopCh <- struct{}{}
	wg.Wait()

	assert.Equal(t, 20, oLogger.FilterMessageSnippet("The number is: 0").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("Hello 0").Len())

	t.Cleanup(func() {
		_ = file.Close()
		_ = os.Remove("foo.txt")
	})
}

func TestServiceReset4(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-reload.yaml",
	}

	l, oLogger := mocklogger.SlogTestLogger(slog.LevelDebug)
	err := cont.RegisterAll(
		cfg,
		l,
		&rpcPlugin.Plugin{},
		&resetter.Plugin{},
		&service.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	assert.NoError(t, err)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second * 2)

	file, err := os.Create("foo2.txt")
	require.NoError(t, err)

	go func() {
		l1 := &serviceProto.List{}
		t.Run("list", list("127.0.0.1:6111", nil, l1))
		require.Len(t, l1.GetServices(), 2)
	}()

	go func() {
		l2 := &serviceProto.List{}
		t.Run("list", list("127.0.0.1:6111", nil, l2))
		require.Len(t, l2.GetServices(), 2)
		for i := range len(l2.GetServices()) {
			cmd := &serviceProto.Service{
				Name: l2.GetServices()[i],
			}

			out := &serviceProto.Response{}

			t.Run("terminate", terminate("127.0.0.1:6111", cmd, out))
		}
	}()

	time.Sleep(time.Second * 10)
	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 20, oLogger.FilterMessageSnippet("service was started").Len())
	t.Cleanup(func() {
		_ = file.Close()
		_ = os.Remove("foo2.txt")
	})
}

func create(in *serviceProto.Create, out *serviceProto.Response) func(t *testing.T) {
	return func(t *testing.T) {
		client := newRPCClient(t, serviceRPCAddr)
		defer func() { _ = client.Close() }()
		require.NoError(t, client.Call("service.CreateService", in, out))
	}
}

func terminate(address string, in *serviceProto.Service, out *serviceProto.Response) func(t *testing.T) {
	return func(t *testing.T) {
		client := newRPCClient(t, address)
		defer func() { _ = client.Close() }()
		require.NoError(t, client.Call("service.Terminate", in, out))
	}
}

func restart(in *serviceProto.Service, out *serviceProto.Response) func(t *testing.T) {
	return func(t *testing.T) {
		client := newRPCClient(t, serviceRPCAddr)
		defer func() { _ = client.Close() }()
		require.NoError(t, client.Call("service.Restart", in, out))
	}
}

func status(in *serviceProto.Service, out *serviceProto.Statuses) func(t *testing.T) {
	return func(t *testing.T) {
		client := newRPCClient(t, serviceRPCAddr)
		defer func() { _ = client.Close() }()
		require.NoError(t, client.Call("service.GetStatuses", in, out))
	}
}

func list(address string, in *serviceProto.Service, out *serviceProto.List) func(t *testing.T) {
	return func(t *testing.T) {
		if in == nil {
			in = &serviceProto.Service{}
		}
		client := newRPCClient(t, address)
		defer func() { _ = client.Close() }()
		require.NoError(t, client.Call("service.ListServices", in, out))
	}
}

func workers(service string) func(t *testing.T) {
	return func(t *testing.T) {
		client := newRPCClient(t, serviceRPCAddr)
		defer func() { _ = client.Close() }()
		resp := &informerProto.WorkersList{}
		require.NoError(t, client.Call("informer.GetWorkers", &informerProto.GetWorkersRequest{Plugin: service}, resp))
		require.Len(t, resp.GetWorkers(), 20)
	}
}

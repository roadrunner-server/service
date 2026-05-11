package service

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	mocklogger "tests/mock"

	"connectrpc.com/connect"
	serviceProto "github.com/roadrunner-server/api-go/v6/service/v1"
	"github.com/roadrunner-server/api-go/v6/service/v1/serviceV1connect"
	"github.com/roadrunner-server/config/v6"
	"github.com/roadrunner-server/endure/v2"
	goridgeRpc "github.com/roadrunner-server/goridge/v4/pkg/rpc"
	"github.com/roadrunner-server/informer/v6"
	"github.com/roadrunner-server/logger/v6"
	"github.com/roadrunner-server/pool/v2/state/process"
	"github.com/roadrunner-server/resetter/v6"
	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/service/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/proto"
)

const serviceRPCAddr = "127.0.0.1:6001"

func newServiceClient(address string) serviceV1connect.ServiceManagerClient {
	httpc := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return new(net.Dialer).DialContext(ctx, network, addr)
			},
		},
	}
	return serviceV1connect.NewServiceManagerClient(httpc, "http://"+address)
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
	t.Skip("blocked on informer plugin Connect-RPC migration: informer.Workers is unreachable until informer ships (string, http.Handler)")
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
	t.Skip("blocked on resetter plugin Connect-RPC migration: resetter.Reset is unreachable until resetter ships (string, http.Handler)")
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

	conn, err := net.Dial("tcp", "127.0.0.1:6001")
	require.NoError(t, err)
	client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

	var ok bool
	err = client.Call("resetter.Reset", "service", &ok)
	require.NoError(t, err)
	require.True(t, ok)

	time.Sleep(time.Second * 5)
	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 20, oLogger.FilterMessageSnippet("The number is: 0").Len())
	require.Equal(t, 20, oLogger.FilterMessageSnippet("Hello 0").Len())
}

func TestServiceReset2(t *testing.T) {
	t.Skip("blocked on resetter plugin Connect-RPC migration: resetter.Reset is unreachable until resetter ships (string, http.Handler)")
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
		conn, err := net.Dial("tcp", "127.0.0.1:6112") //nolint:noctx // skipped test; goridge wire unreachable post-Connect migration
		require.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		var ok bool
		err = client.Call("resetter.Reset", "service", &ok)
		require.NoError(t, err)
		require.True(t, ok)
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
		resp, err := newServiceClient(serviceRPCAddr).CreateService(t.Context(), connect.NewRequest(in))
		require.NoError(t, err)
		proto.Merge(out, resp.Msg)
	}
}

func terminate(address string, in *serviceProto.Service, out *serviceProto.Response) func(t *testing.T) {
	return func(t *testing.T) {
		resp, err := newServiceClient(address).Terminate(t.Context(), connect.NewRequest(in))
		require.NoError(t, err)
		proto.Merge(out, resp.Msg)
	}
}

func restart(in *serviceProto.Service, out *serviceProto.Response) func(t *testing.T) {
	return func(t *testing.T) {
		resp, err := newServiceClient(serviceRPCAddr).Restart(t.Context(), connect.NewRequest(in))
		require.NoError(t, err)
		proto.Merge(out, resp.Msg)
	}
}

func status(in *serviceProto.Service, out *serviceProto.Statuses) func(t *testing.T) {
	return func(t *testing.T) {
		resp, err := newServiceClient(serviceRPCAddr).GetStatuses(t.Context(), connect.NewRequest(in))
		require.NoError(t, err)
		proto.Merge(out, resp.Msg)
	}
}

func list(address string, in *serviceProto.Service, out *serviceProto.List) func(t *testing.T) {
	return func(t *testing.T) {
		resp, err := newServiceClient(address).ListServices(t.Context(), connect.NewRequest(in))
		require.NoError(t, err)
		proto.Merge(out, resp.Msg)
	}
}

func workers(service string) func(t *testing.T) {
	return func(t *testing.T) {
		conn, err := net.Dial("tcp", "127.0.0.1:6001") //nolint:noctx // skipped test; goridge wire unreachable post-Connect migration
		require.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))
		// WorkerList contains a list of workers.
		lst := struct {
			// Workers is a list of workers.
			Workers []process.State `json:"workers"`
		}{}

		err = client.Call("informer.Workers", service, &lst)
		require.NoError(t, err)
		require.Len(t, lst.Workers, 20)
	}
}

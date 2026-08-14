package tests

import (
	"sync"
	"testing"
	"time"

	"tests/helpers"

	serviceV1 "github.com/roadrunner-server/api-go/v6/service/v1"
	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/service/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceConcurrentRestart(t *testing.T) {
	const (
		addr     = "127.0.0.1:6311"
		routines = 200
		rounds   = 2
	)

	rr, stop := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
		[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(addr))

	client := helpers.RPC(t, addr)
	helpers.Create(t, client, &serviceV1.Create{
		Name:       "foo",
		Command:    "php php_test_files/loop.php",
		ProcessNum: 1,
	})

	wg := &sync.WaitGroup{}
	for range routines {
		wg.Go(func() {
			for range rounds {
				// assert, not require: a failed require would only end this goroutine
				assert.NoError(t, helpers.RestartErr(client, "foo"))
			}
		})
	}
	wg.Wait()

	// the group survives every restart and still has a live process
	require.Equal(t, []string{"foo"}, helpers.List(t, client))
	require.Len(t, helpers.Statuses(t, client, "foo"), 1)

	helpers.Terminate(t, client, "foo")

	stop()

	require.Empty(t, rr.Errs())
}

func TestServiceConcurrentRestartAndList(t *testing.T) {
	const (
		addr     = "127.0.0.1:6311"
		routines = 200
		rounds   = 2
	)

	rr, stop := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
		[]any{&service.Plugin{}, &rpcPlugin.Plugin{}},
		helpers.WithTCPProbe(addr), helpers.WithGracefulTimeout(time.Second*5))

	client := helpers.RPC(t, addr)
	helpers.Create(t, client, &serviceV1.Create{
		Name:       "foo",
		Command:    "php php_test_files/loop.php",
		ProcessNum: 1,
	})

	wg := &sync.WaitGroup{}
	for range routines {
		wg.Go(func() {
			for range rounds {
				assert.NoError(t, helpers.RestartErr(client, "foo"))

				// the service is never observable as missing while it is replaced
				names, err := helpers.ListErr(client)
				if assert.NoError(t, err) {
					assert.Equal(t, []string{"foo"}, names)
				}
			}
		})
	}
	wg.Wait()

	require.Equal(t, []string{"foo"}, helpers.List(t, client))

	helpers.Terminate(t, client, "foo")

	stop()

	require.Empty(t, rr.Errs())
}

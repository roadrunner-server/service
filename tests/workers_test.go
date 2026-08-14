package tests

import (
	"testing"

	"tests/helpers"

	"github.com/roadrunner-server/informer/v6"
	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/service/v6"
	"github.com/stretchr/testify/require"
)

func TestServiceInformerWorkers(t *testing.T) {
	const addr = "127.0.0.1:6315"

	rr, stop := helpers.Start(t, "configs/.rr-service-workers.yaml",
		[]any{&service.Plugin{}, &informer.Plugin{}, &rpcPlugin.Plugin{}},
		helpers.WithServicesStarted(20), helpers.WithTCPProbe(addr))

	states := helpers.Workers(t, helpers.RPC(t, addr), "service")
	require.Len(t, states, 20)

	for _, st := range states {
		require.NotZero(t, st.Pid)
		require.NotZero(t, st.MemoryUsage)
		require.Contains(t, st.Command, "php")
	}

	stop()

	require.Empty(t, rr.Errs())
}

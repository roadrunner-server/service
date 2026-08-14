package tests

import (
	"testing"

	"tests/helpers"

	"github.com/roadrunner-server/resetter/v6"
	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/service/v6"
	"github.com/stretchr/testify/require"
)

func TestServiceResetterRestartsProcesses(t *testing.T) {
	const addr = "127.0.0.1:6314"

	rr, stop := helpers.Start(t, "configs/.rr-service-reset.yaml",
		[]any{&service.Plugin{}, &rpcPlugin.Plugin{}, &resetter.Plugin{}},
		helpers.WithServicesStarted(20), helpers.WithTCPProbe(addr))

	// each of the 10+10 processes writes its first line once per start
	rr.WaitLogs(t, "The number is: 0", 10)
	rr.WaitLogs(t, "Hello 0", 10)

	helpers.Reset(t, helpers.RPC(t, addr), "service")

	rr.WaitLogs(t, "The number is: 0", 20)
	rr.WaitLogs(t, "Hello 0", 20)

	stop()

	// remain_after_exit is false, so the reset is the only thing that can start
	// a second generation of processes
	require.Equal(t, 20, rr.Count("The number is: 0"))
	require.Equal(t, 20, rr.Count("Hello 0"))
	require.Equal(t, 20, rr.Count("service was stopped"))
	require.Empty(t, rr.Errs())
}

func TestServiceExecTimeoutRestart(t *testing.T) {
	rr, stop := helpers.Start(t, "configs/.rr-service-exec-timeout.yaml", []any{&service.Plugin{}},
		helpers.WithServicesStarted(20))

	// exec_timeout kills every process and remain_after_exit brings it back
	// restart_sec later, so the first line of each command is written again
	rr.WaitLogs(t, "The number is: 0", 20)
	rr.WaitLogs(t, "Hello 0", 20)

	stop()

	// every kill surfaces as a wait error before the process is started again
	require.NotZero(t, rr.CountExact("wait"))
	require.Empty(t, rr.Errs())
}

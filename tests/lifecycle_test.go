package tests

import (
	"testing"
	"time"

	"tests/helpers"

	"github.com/roadrunner-server/service/v6"
	"github.com/stretchr/testify/require"
)

func TestServiceInit(t *testing.T) {
	rr, stop := helpers.Start(t, "configs/.rr-service-init.yaml", []any{&service.Plugin{}},
		helpers.WithServicesStarted(2))

	// both commands really execute: each writes its first line on startup
	rr.WaitLogs(t, "The number is: 0", 1)
	rr.WaitLogs(t, "Hello 0", 1)

	stop()

	require.Equal(t, 2, rr.Count("service was stopped"))
	require.Empty(t, rr.Errs())
}

func TestServiceStdout(t *testing.T) {
	rr, stop := helpers.Start(t, "configs/.rr-service-init-stdout.yaml", []any{&service.Plugin{}},
		helpers.WithServicesStarted(1))

	// the child writes to stdout, which the plugin redirects into the log, and
	// the env entries from the config reach it
	rr.WaitLogs(t, "stdout write FOO=BAR FOO2=BAZ", 1)

	stop()

	require.Equal(t, 1, rr.Count("service was stopped"))
	require.Empty(t, rr.Errs())
}

func TestServiceEnv(t *testing.T) {
	rr, stop := helpers.Start(t, "configs/.rr-service-env.yaml", []any{&service.Plugin{}},
		helpers.WithServicesStarted(1))

	// the script writes the marker only when both env entries arrive uppercased
	rr.WaitLogs(t, "env ok", 1)

	stop()

	require.Zero(t, rr.Count("faillll"))
	require.Empty(t, rr.Errs())
}

func TestServiceTrimOutput(t *testing.T) {
	rr, stop := helpers.Start(t, "configs/.rr-service-newlines.yaml", []any{&service.Plugin{}},
		helpers.WithServicesStarted(1))

	rr.WaitLogs(t, "stdout write", 1)

	stop()

	require.Equal(t, 1, rr.Count("stdout write"))
	// the child writes "stdout write \n\t" and the record keeps none of the
	// trailing whitespace, so the raw payload never shows up
	require.Zero(t, rr.Count("stdout write "))
	require.Empty(t, rr.Errs())
}

func TestServiceCommandNotFound(t *testing.T) {
	rr, stop := helpers.Start(t, "configs/.rr-service-error.yaml", []any{&service.Plugin{}},
		helpers.WithServicesStarted(1))

	// php cannot open the script, reports it and exits non-zero
	rr.WaitLogs(t, "Could not open input file", 1)
	// the non-zero exit surfaces as a wait error
	rr.WaitLogsExact(t, "wait", 1)

	// remain_after_exit is false, so the process is not brought back
	require.Never(t, func() bool { return rr.Count("Could not open input file") > 1 },
		time.Second*2, time.Millisecond*100)

	stop()

	require.Empty(t, rr.Errs())
}

func TestServiceExitRestart(t *testing.T) {
	rr, stop := helpers.Start(t, "configs/.rr-service-restarts.yaml", []any{&service.Plugin{}},
		helpers.WithServicesStarted(1))

	// exec_timeout kills the process, remain_after_exit brings it back after
	// restart_sec, and the fresh process writes its first line again
	rr.WaitLogs(t, "Hello 0", 2)

	stop()

	require.NotZero(t, rr.CountExact("wait"))
	require.Empty(t, rr.Errs())
}

func TestServiceBrokenConfig(t *testing.T) {
	err := helpers.StartExpectInitError(t, "", []any{&service.Plugin{}}, helpers.WithInlineConfig(`
version: '3'

service:
  some_service_1:
    command: "php php_test_files/loop.php"
    exec_timeout: not-a-duration
`))

	require.ErrorContains(t, err, "service_plugin_init")
}

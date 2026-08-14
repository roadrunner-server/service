package tests

import (
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"tests/helpers"

	serviceV1 "github.com/roadrunner-server/api-go/v6/service/v1"
	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/service/v6"
	"github.com/stretchr/testify/require"
)

func TestServiceRPCCreate(t *testing.T) {
	const addr = "127.0.0.1:6312"

	rr, stop := helpers.Start(t, "configs/.rr-service-create.yaml",
		[]any{&service.Plugin{}, &rpcPlugin.Plugin{}},
		helpers.WithServicesStarted(2), helpers.WithTCPProbe(addr))

	client := helpers.RPC(t, addr)
	helpers.Create(t, client, &serviceV1.Create{
		Name:        "foo",
		Command:     "php php_test_files/loop.php",
		ProcessNum:  1,
		ExecTimeout: 10,
		Env:         map[string]string{"foo": "bar"},
	})

	require.ElementsMatch(t, []string{"some_service_1", "some_service_2", "foo"}, helpers.List(t, client))

	helpers.Terminate(t, client, "foo")
	require.ElementsMatch(t, []string{"some_service_1", "some_service_2"}, helpers.List(t, client))

	stop()

	require.Empty(t, rr.Errs())
}

func TestServiceRPCCreateOnEmptyConfig(t *testing.T) {
	const addr = "127.0.0.1:6311"

	rr, stop := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
		[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(addr))

	client := helpers.RPC(t, addr)
	require.Empty(t, helpers.List(t, client))

	helpers.Create(t, client, &serviceV1.Create{
		Name:        "foo",
		Command:     "php php_test_files/loop.php",
		ProcessNum:  1,
		ExecTimeout: 10,
	})

	require.Equal(t, []string{"foo"}, helpers.List(t, client))
	rr.WaitLogs(t, "The number is: 0", 1)

	helpers.Terminate(t, client, "foo")
	require.Empty(t, helpers.List(t, client))

	stop()

	require.Empty(t, rr.Errs())
}

func TestServiceRPCRestart(t *testing.T) {
	const addr = "127.0.0.1:6311"

	rr, stop := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
		[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(addr))

	client := helpers.RPC(t, addr)
	helpers.Create(t, client, &serviceV1.Create{
		Name:        "foo",
		Command:     "php php_test_files/loop.php",
		ProcessNum:  2,
		ExecTimeout: 10,
	})

	before := statusPids(t, helpers.Statuses(t, client, "foo"))
	require.Len(t, before, 2)

	helpers.Restart(t, client, "foo")

	after := statusPids(t, helpers.Statuses(t, client, "foo"))
	require.Len(t, after, 2)
	for _, pid := range after {
		require.NotContains(t, before, pid)
	}

	helpers.Terminate(t, client, "foo")

	stop()

	require.Empty(t, rr.Errs())
}

func TestServiceRPCStatuses(t *testing.T) {
	const addr = "127.0.0.1:6311"

	rr, stop := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
		[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(addr))

	client := helpers.RPC(t, addr)
	helpers.Create(t, client, &serviceV1.Create{
		Name:        "foo",
		Command:     "php php_test_files/loop.php",
		ProcessNum:  2,
		ExecTimeout: 10,
	})

	statuses := helpers.Statuses(t, client, "foo")
	require.Len(t, statuses, 2)

	for _, st := range statuses {
		require.Nil(t, st.GetStatus())
		require.NotZero(t, st.GetPid())
		require.NotZero(t, st.GetMemoryUsage())
		require.Contains(t, st.GetCommand(), "loop.php")
	}

	helpers.Terminate(t, client, "foo")

	stop()

	require.Empty(t, rr.Errs())
}

func TestServiceRPCListAndTerminate(t *testing.T) {
	const addr = "127.0.0.1:6316"

	rr, stop := helpers.Start(t, "configs/.rr-service-list-terminate.yaml",
		[]any{&service.Plugin{}, &rpcPlugin.Plugin{}},
		helpers.WithServicesStarted(20), helpers.WithTCPProbe(addr))

	client := helpers.RPC(t, addr)
	names := helpers.List(t, client)
	require.ElementsMatch(t, []string{"some_service_1", "some_service_2"}, names)

	for _, name := range names {
		helpers.Terminate(t, client, name)
	}

	require.Empty(t, helpers.List(t, client))
	require.Equal(t, 20, rr.Count("service was started"))

	stop()

	require.Empty(t, rr.Errs())
}

func TestServiceRPCCreateFromPHP(t *testing.T) {
	requirePHPRPCClient(t)

	const addr = "127.0.0.1:6313"

	rr, stop := helpers.Start(t, "configs/.rr-service-from-php.yaml",
		[]any{&service.Plugin{}, &rpcPlugin.Plugin{}},
		helpers.WithServicesStarted(1), helpers.WithTCPProbe(addr))

	client := helpers.RPC(t, addr)

	// the php script creates a group of three processes over rpc
	require.Eventually(t, func() bool {
		names, err := helpers.ListErr(client)
		return err == nil && slices.Contains(names, "listen-jobs")
	}, time.Second*20, time.Millisecond*50, "the php side did not create its service")

	require.Len(t, helpers.Statuses(t, client, "listen-jobs"), 3)

	stop()

	// the configured service plus the three processes the php side created
	require.Equal(t, 4, rr.Count("service was stopped"))
	require.Empty(t, rr.Errs())
}

// statusPids collects the pids reported for a service group.
func statusPids(t *testing.T, statuses []*serviceV1.Status) []int32 {
	t.Helper()

	pids := make([]int32, 0, len(statuses))
	for _, st := range statuses {
		require.NotZero(t, st.GetPid())
		pids = append(pids, st.GetPid())
	}

	return pids
}

// requirePHPRPCClient skips unless the php side can open an rpc connection: the
// composer dependencies have to be installed and php needs the sockets extension.
func requirePHPRPCClient(t *testing.T) {
	t.Helper()

	if _, err := os.Stat("php_test_files/vendor/autoload.php"); err != nil {
		t.Skip("php_test_files/vendor is not installed")
	}

	if err := exec.CommandContext(t.Context(), "php", "-r", `exit(extension_loaded("sockets") ? 0 : 1);`).Run(); err != nil {
		t.Skip("php is built without the sockets extension")
	}
}

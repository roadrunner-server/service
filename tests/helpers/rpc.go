package helpers

import (
	"net"
	"net/rpc"
	"testing"

	serviceV1 "github.com/roadrunner-server/api-go/v6/service/v1"
	goridgeRpc "github.com/roadrunner-server/goridge/v4/pkg/rpc"
	"github.com/roadrunner-server/pool/v2/state/process"
	"github.com/stretchr/testify/require"
)

// RPC dials the goridge endpoint at addr and returns a client closed at the end
// of the test. net/rpc clients are safe for concurrent use, so one client per
// test is enough even for the concurrency cases.
func RPC(t *testing.T, addr string) *rpc.Client {
	t.Helper()

	var conn net.Conn
	require.Eventually(t, func() bool {
		d := net.Dialer{Timeout: probeDial}
		c, err := d.DialContext(t.Context(), "tcp", addr)
		if err != nil {
			return false
		}

		conn = c
		return true
	}, probeTimeout, probeTick, "rpc endpoint %s did not accept a connection", addr)

	client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))
	t.Cleanup(func() { _ = client.Close() })

	return client
}

// Create starts a new service group.
func Create(t *testing.T, c *rpc.Client, in *serviceV1.Create) {
	t.Helper()

	out := &serviceV1.Response{}
	require.NoError(t, c.Call("service.Create", in, out))
	require.True(t, out.GetOk())
}

// Terminate stops the named service group and drops it from the plugin.
func Terminate(t *testing.T, c *rpc.Client, name string) {
	t.Helper()

	out := &serviceV1.Response{}
	require.NoError(t, c.Call("service.Terminate", &serviceV1.Service{Name: name}, out))
	require.True(t, out.GetOk())
}

// Restart replaces every process of the named service group.
func Restart(t *testing.T, c *rpc.Client, name string) {
	t.Helper()
	require.NoError(t, RestartErr(c, name))
}

// RestartErr is Restart without a *testing.T, for calls made from goroutines.
func RestartErr(c *rpc.Client, name string) error {
	out := &serviceV1.Response{}
	return c.Call("service.Restart", &serviceV1.Service{Name: name}, out)
}

// Statuses returns the per-process state of the named service group.
func Statuses(t *testing.T, c *rpc.Client, name string) []*serviceV1.Status {
	t.Helper()

	out := &serviceV1.Statuses{}
	require.NoError(t, c.Call("service.Statuses", &serviceV1.Service{Name: name}, out))

	return out.GetStatus()
}

// List returns the names of the service groups the plugin knows about.
func List(t *testing.T, c *rpc.Client) []string {
	t.Helper()

	names, err := ListErr(c)
	require.NoError(t, err)

	return names
}

// ListErr is List without a *testing.T, for calls made from goroutines.
func ListErr(c *rpc.Client) ([]string, error) {
	out := &serviceV1.List{}
	if err := c.Call("service.List", &serviceV1.Service{}, out); err != nil {
		return nil, err
	}

	return out.GetServices(), nil
}

// Reset asks the resetter plugin to reset the named plugin.
func Reset(t *testing.T, c *rpc.Client, plugin string) {
	t.Helper()

	var ok bool
	require.NoError(t, c.Call("resetter.Reset", plugin, &ok))
	require.True(t, ok)
}

// Workers returns the process states the informer plugin reports for the plugin.
func Workers(t *testing.T, c *rpc.Client, plugin string) []process.State {
	t.Helper()

	out := struct {
		Workers []process.State `json:"workers"`
	}{}
	require.NoError(t, c.Call("informer.Workers", plugin, &out))

	return out.Workers
}

package service

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeneralProcessState(t *testing.T) {
	st, err := generalProcessState(int64(os.Getpid()), "go test")
	require.NoError(t, err)

	require.Equal(t, int64(os.Getpid()), st.Pid)
	require.Equal(t, "go test", st.Command)
	require.EqualValues(t, 1, st.Status)
	require.NotZero(t, st.MemoryUsage)
}

func TestGeneralProcessStateInvalidPid(t *testing.T) {
	st, err := generalProcessState(-1, "go test")

	require.Error(t, err)
	require.Nil(t, st)
	require.ErrorContains(t, err, "process_state")
}

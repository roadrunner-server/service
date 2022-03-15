package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetEnv(t *testing.T) {
	e := make(Env, 5)
	e["foo"] = "bar"
	e["bar"] = "baz"

	p := &Process{}
	out := p.setEnv(e)
	val := out[len(out)-1]
	val2 := out[len(out)-2]

	require.Equal(t, "bar=baz", val)
	require.Equal(t, "foo=bar", val2)
}

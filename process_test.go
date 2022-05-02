package service

import (
	"testing"
)

func TestSetEnv(t *testing.T) {
	e := make(Env, 5)
	e["foo"] = "bar"
	e["bar"] = "baz"

	p := &Process{}
	out := p.setEnv(e)
	val := out[len(out)-1]
	val2 := out[len(out)-2]

	if val != "bar=baz" && val != "foo=bar" {
		t.Fail()
	}

	if val2 != "bar=baz" && val2 != "foo=bar" {
		t.Fail()
	}
}

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigInitDefault(t *testing.T) {
	cfg := &Config{Services: map[string]*Service{
		"unset":    {},
		"negative": {ProcessNum: -3},
		"explicit": {ProcessNum: 4, RestartSec: 7, TimeoutStopSec: 9},
	}}

	cfg.InitDefault()

	tests := []struct {
		name           string
		processNum     int
		restartSec     uint64
		timeoutStopSec uint64
	}{
		{name: "unset", processNum: 1, restartSec: 30, timeoutStopSec: 5},
		{name: "negative", processNum: 1, restartSec: 30, timeoutStopSec: 5},
		{name: "explicit", processNum: 4, restartSec: 7, timeoutStopSec: 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := cfg.Services[tt.name]
			require.Equal(t, tt.processNum, svc.ProcessNum)
			require.Equal(t, tt.restartSec, svc.RestartSec)
			require.Equal(t, tt.timeoutStopSec, svc.TimeoutStopSec)
		})
	}
}

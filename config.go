package service

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"time"
)

// Env variables type alias
type Env map[string]string

// Service represents particular service configuration
type Service struct {
	Command         string        `mapstructure:"command"`
	UseServiceName  bool          `mapstructure:"service_name_in_log"`
	ProcessNum      int           `mapstructure:"process_num"`
	ExecTimeout     time.Duration `mapstructure:"exec_timeout"`
	RemainAfterExit bool          `mapstructure:"remain_after_exit"`
	RestartSec      uint64        `mapstructure:"restart_sec"`
	TimeoutStopSec  uint64        `mapstructure:"timeout_stop_sec"`
	Env             Env           `mapstructure:"env"`
	User            string        `mapstructure:"user"`
}

func (s *Service) clone() Service {
	next := *s
	next.Env = maps.Clone(s.Env)
	next.RestartSec = cmp.Or(s.RestartSec, 30)
	next.TimeoutStopSec = cmp.Or(s.TimeoutStopSec, 5)
	return next
}

func validateRuntimeValues(count, execution int64, restart, stop uint64) error {
	const maxSeconds = math.MaxInt64 / int64(time.Second)
	if count < 1 || count > math.MaxInt {
		return fmt.Errorf("process_num must fit int and have at least 1 process")
	}
	if execution < 0 || execution > maxSeconds {
		return fmt.Errorf("exec_timeout must be between 0 and %d seconds", maxSeconds)
	}
	if restart > uint64(maxSeconds) {
		return fmt.Errorf("restart_sec must not exceed %d seconds", maxSeconds)
	}
	if stop > uint64(maxSeconds) {
		return fmt.Errorf("timeout_stop_sec must not exceed %d seconds", maxSeconds)
	}
	return nil
}

// Config for the services
type Config struct {
	Services map[string]*Service `mapstructure:"service"`
}

func (c *Config) InitDefault() {
	for _, v := range c.Services {
		if v.ProcessNum <= 0 {
			v.ProcessNum = 1
		}
		if v.RestartSec == 0 {
			v.RestartSec = 30
		}
		// default 5 seconds
		if v.TimeoutStopSec == 0 {
			v.TimeoutStopSec = 5
		}
	}
}

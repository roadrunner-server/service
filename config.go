package service

import (
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

package service

import (
	"errors"
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
	Env             Env           `mapstructure:"env"`
}

// Config for the services
type Config struct {
	Services map[string]*Service `mapstructure:"service"`
}

func (c *Config) InitDefault() error {
	if len(c.Services) > 0 {
		for k, v := range c.Services {
			val := c.Services[k]
			c.Services[k] = val

			if v.ExecTimeout == 0 {
				return errors.New("exec_timeout should be more 0")
			}

			if v.ProcessNum == 0 {
				val := c.Services[k]
				val.ProcessNum = 1
				c.Services[k] = val
			}
			if v.RestartSec == 0 {
				val := c.Services[k]
				val.RestartSec = 30
				c.Services[k] = val
			}
		}
	}

	return nil
}

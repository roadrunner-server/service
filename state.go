package service

import (
	"github.com/roadrunner-server/errors"
	rrProcess "github.com/roadrunner-server/sdk/v2/state/process"
	"github.com/shirou/gopsutil/process"
)

func generalProcessState(pid int64, command string) (*rrProcess.State, error) {
	const op = errors.Op("process_state")
	p, _ := process.NewProcess(int32(pid))
	i, err := p.MemoryInfo()
	if err != nil {
		return nil, errors.E(op, err)
	}
	percent, err := p.CPUPercent()
	if err != nil {
		return nil, err
	}

	return &rrProcess.State{
		CPUPercent_:  percent,
		Pid_:         pid,
		MemoryUsage_: i.RSS,
		Command_:     command,
	}, nil
}

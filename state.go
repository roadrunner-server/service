package service

import (
	rrProcess "github.com/roadrunner-server/api/v2/state/process"
	"github.com/roadrunner-server/errors"
	"github.com/shirou/gopsutil/process"
)

func generalProcessState(pid int, command string) (rrProcess.State, error) {
	const op = errors.Op("process_state")
	p, _ := process.NewProcess(int32(pid))
	i, err := p.MemoryInfo()
	if err != nil {
		return rrProcess.State{}, errors.E(op, err)
	}
	percent, err := p.CPUPercent()
	if err != nil {
		return rrProcess.State{}, err
	}

	return rrProcess.State{
		CPUPercent:  percent,
		Pid:         pid,
		MemoryUsage: i.RSS,
		Command:     command,
	}, nil
}

package service

import (
	"github.com/roadrunner-server/errors"
	rrProcess "github.com/roadrunner-server/pool/state/process"
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
		CPUPercent:  percent,
		Pid:         pid,
		Status:      1,
		MemoryUsage: i.RSS,
		Command:     command,
	}, nil
}

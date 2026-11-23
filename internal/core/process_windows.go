package core

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var createJob = kernel32.NewProc("CreateJobObjectW")
var setJob = kernel32.NewProc("SetInformationJobObject")
var assignJob = kernel32.NewProc("AssignProcessToJobObject")

type basicJobLimits struct {
	ProcessTime, JobTime         int64
	Flags                        uint32
	MinWorkingSet, MaxWorkingSet uintptr
	ActiveProcesses              uint32
	Affinity                     uintptr
	Priority, Scheduling         uint32
}
type jobLimits struct {
	Basic                                                      basicJobLimits
	IOCounters                                                 [6]uint64
	ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
}

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
} // CREATE_NO_WINDOW
func containProcess(cmd *exec.Cmd) (func(), error) {
	handle, _, err := createJob.Call(0, 0)
	if handle == 0 {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}
	closeJob := func() { syscall.CloseHandle(syscall.Handle(handle)) }
	limits := jobLimits{}
	limits.Basic.Flags = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	ok, _, err := setJob.Call(handle, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		closeJob()
		return nil, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	process, err := syscall.OpenProcess(0x0100|0x0001, false, uint32(cmd.Process.Pid))
	if err != nil {
		closeJob()
		return nil, err
	}
	defer syscall.CloseHandle(process)
	ok, _, err = assignJob.Call(handle, uintptr(process))
	if ok == 0 {
		closeJob()
		return nil, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	return closeJob, nil
}

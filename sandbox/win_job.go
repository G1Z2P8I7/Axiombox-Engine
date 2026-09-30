// Package sandbox implements native Windows Job Object isolation for untrusted processes.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procCreateJobObjectW        = modkernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject = modkernel32.NewProc("SetInformationJobObject")
	procQueryInformationJobObject = modkernel32.NewProc("QueryInformationJobObject")
	procAssignProcessToJobObject = modkernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject      = modkernel32.NewProc("TerminateJobObject")
)

// Win32 Job Object Information Classes
const (
	JobObjectBasicAccountingInformation = 1
	JobObjectBasicLimitInformation      = 2
	JobObjectBasicUIRestrictions        = 4
	JobObjectSecurityLimitInformation   = 5
	JobObjectEndOfJobTimeInformation    = 6
	JobObjectAssociateCompletionPortInformation = 7
	JobObjectBasicAndIoAccountingInformation = 8
	JobObjectExtendedLimitInformation   = 9
	JobObjectCpuRateControlInformation  = 15
)

// Limit Flags for JOBOBJECT_BASIC_LIMIT_INFORMATION
const (
	JOB_OBJECT_LIMIT_WORKINGSET                 = 0x00000001
	JOB_OBJECT_LIMIT_PROCESS_TIME               = 0x00000002
	JOB_OBJECT_LIMIT_JOB_TIME                   = 0x00000004
	JOB_OBJECT_LIMIT_ACTIVE_PROCESSES           = 0x00000008
	JOB_OBJECT_LIMIT_AFFINITY                   = 0x00000010
	JOB_OBJECT_LIMIT_PRIORITY_CLASS             = 0x00000020
	JOB_OBJECT_LIMIT_PRESERVE_JOB_TIME          = 0x00000040
	JOB_OBJECT_LIMIT_SCHEDULING_CLASS           = 0x00000080
	JOB_OBJECT_LIMIT_PROCESS_MEMORY             = 0x00000100
	JOB_OBJECT_LIMIT_JOB_MEMORY                 = 0x00000200
	JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION = 0x00000400
	JOB_OBJECT_LIMIT_BREAKAWAY_OK               = 0x00000800
	JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK        = 0x00001000
	JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE          = 0x00002000
	JOB_OBJECT_LIMIT_SUBSET_AFFINITY            = 0x00004000
)

// UI Restrictions Flags
const (
	JOB_OBJECT_UILIMIT_HANDLES          = 0x00000001
	JOB_OBJECT_UILIMIT_READCLIPBOARD    = 0x00000002
	JOB_OBJECT_UILIMIT_WRITECLIPBOARD   = 0x00000004
	JOB_OBJECT_UILIMIT_SYSTEMPARAMETERS = 0x00000008
	JOB_OBJECT_UILIMIT_DISPLAYSETTINGS  = 0x00000010
	JOB_OBJECT_UILIMIT_GLOBALATOMS      = 0x00000020
	JOB_OBJECT_UILIMIT_DESKTOP          = 0x00000040
	JOB_OBJECT_UILIMIT_EXITWINDOWS      = 0x00000080
)

// CPU Rate Control Flags
const (
	JOB_OBJECT_CPU_RATE_CONTROL_ENABLE   = 0x00000001
	JOB_OBJECT_CPU_RATE_CONTROL_WEIGHT_BASED = 0x00000002
	JOB_OBJECT_CPU_RATE_CONTROL_HARD_CAP = 0x00000004
	JOB_OBJECT_CPU_RATE_CONTROL_NOTIFY   = 0x00000008
	JOB_OBJECT_CPU_RATE_CONTROL_MIN_MAX_RATE = 0x00000010
)

// Exit codes representing sandbox enforcement reasons
const (
	ExitCodeSuccess               = 0
	ExitCodeTimeLimitExceeded     = 0xC0000054 // STATUS_TIMEOUT equivalent
	ExitCodeMemoryLimitExceeded   = 0xC0000017 // STATUS_NO_MEMORY
	ExitCodeProcessLimitExceeded  = 0xC0000005 // Security/process breach
	ExitCodeSecurityViolation     = 0xC0000022 // STATUS_ACCESS_DENIED
)

// Win32 Structures matching 64-bit layout
type JOBOBJECT_BASIC_LIMIT_INFORMATION struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	padding1                uint32 // 4-byte padding on 64-bit
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	padding2                uint32 // 4-byte padding on 64-bit
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type IO_COUNTERS struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type JOBOBJECT_EXTENDED_LIMIT_INFORMATION struct {
	BasicLimitInformation JOBOBJECT_BASIC_LIMIT_INFORMATION
	IoInfo                IO_COUNTERS
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type JOBOBJECT_BASIC_UI_RESTRICTIONS struct {
	UIRestrictionsClass uint32
}

type JOBOBJECT_CPU_RATE_CONTROL_INFORMATION struct {
	ControlFlags uint32
	CpuRate      uint32 // In 1/100th of 1% (e.g. 10000 = 100% of 1 core)
}

type JOBOBJECT_BASIC_ACCOUNTING_INFORMATION struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// Config holds containment settings for an untrusted sandbox execution.
type Config struct {
	// MemoryLimitBytes specifies maximum commit limit (default 64MB).
	MemoryLimitBytes uint64
	// TimeLimit specifies maximum execution deadline (default 2.0s).
	TimeLimit time.Duration
	// MaxActiveProcesses sets maximum simultaneous process count (default 1 to kill fork-bombs).
	MaxActiveProcesses uint32
	// CpuRatePercentage sets maximum CPU percentage cap (e.g. 100 = 1 full core, default 100).
	CpuRatePercentage uint32
	// WorkingDir sets working directory for the sandboxed process.
	WorkingDir string
}

// DefaultConfig returns hardened production defaults: 64MB RAM, 2.0s timeout, 1 active process.
func DefaultConfig() Config {
	return Config{
		MemoryLimitBytes:   64 * 1024 * 1024, // 64 MB
		TimeLimit:          2 * time.Second,  // 2000 ms
		MaxActiveProcesses: 1,                // Strictly 1 process - prevents fork-bombs
		CpuRatePercentage:  100,              // 100% of 1 single core
		WorkingDir:         "",
	}
}

// Job represents an active Windows Job Object isolation harness.
type Job struct {
	handle      windows.Handle
	config      Config
	mu          sync.Mutex
	isClosed    bool
	timedOut    bool
	memExceeded bool
}

// NewJob creates and initializes a hardened Windows Job Object with kernel limits.
func NewJob(cfg Config) (*Job, error) {
	if cfg.MemoryLimitBytes == 0 {
		cfg.MemoryLimitBytes = 64 * 1024 * 1024
	}
	if cfg.TimeLimit == 0 {
		cfg.TimeLimit = 2 * time.Second
	}
	if cfg.MaxActiveProcesses == 0 {
		cfg.MaxActiveProcesses = 1
	}

	// 1. Create Job Object via kernel32.dll
	r1, _, err := procCreateJobObjectW.Call(0, 0)
	if r1 == 0 {
		return nil, fmt.Errorf("CreateJobObjectW failed: %w", err)
	}
	jobHandle := windows.Handle(r1)

	job := &Job{
		handle: jobHandle,
		config: cfg,
	}

	// 2. Configure Extended Limit Information (Memory, Active Processes, Kill-on-Close)
	extLimits := JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: JOB_OBJECT_LIMIT_PROCESS_MEMORY |
				JOB_OBJECT_LIMIT_JOB_MEMORY |
				JOB_OBJECT_LIMIT_ACTIVE_PROCESSES |
				JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
				JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION,
			ActiveProcessLimit: cfg.MaxActiveProcesses,
		},
		ProcessMemoryLimit: uintptr(cfg.MemoryLimitBytes),
		JobMemoryLimit:     uintptr(cfg.MemoryLimitBytes),
	}

	r2, _, err := procSetInformationJobObject.Call(
		uintptr(jobHandle),
		uintptr(JobObjectExtendedLimitInformation),
		uintptr(unsafe.Pointer(&extLimits)),
		uintptr(unsafe.Sizeof(extLimits)),
	)
	if r2 == 0 {
		job.Close()
		return nil, fmt.Errorf("SetInformationJobObject(ExtendedLimit) failed: %w", err)
	}

	// 3. Configure UI Restrictions (Block Windows desktop, clipboard, display settings, handles)
	uiLimits := JOBOBJECT_BASIC_UI_RESTRICTIONS{
		UIRestrictionsClass: JOB_OBJECT_UILIMIT_HANDLES |
			JOB_OBJECT_UILIMIT_READCLIPBOARD |
			JOB_OBJECT_UILIMIT_WRITECLIPBOARD |
			JOB_OBJECT_UILIMIT_SYSTEMPARAMETERS |
			JOB_OBJECT_UILIMIT_DISPLAYSETTINGS |
			JOB_OBJECT_UILIMIT_GLOBALATOMS |
			JOB_OBJECT_UILIMIT_DESKTOP |
			JOB_OBJECT_UILIMIT_EXITWINDOWS,
	}

	r3, _, err := procSetInformationJobObject.Call(
		uintptr(jobHandle),
		uintptr(JobObjectBasicUIRestrictions),
		uintptr(unsafe.Pointer(&uiLimits)),
		uintptr(unsafe.Sizeof(uiLimits)),
	)
	if r3 == 0 {
		job.Close()
		return nil, fmt.Errorf("SetInformationJobObject(BasicUI) failed: %w", err)
	}

	// 4. Configure CPU Rate Control (Hard cap rate cycles)
	if cfg.CpuRatePercentage > 0 {
		cpuRateVal := cfg.CpuRatePercentage * 100 // 100% -> 10,000 units
		if cpuRateVal > 10000 {
			cpuRateVal = 10000
		}
		cpuLimits := JOBOBJECT_CPU_RATE_CONTROL_INFORMATION{
			ControlFlags: JOB_OBJECT_CPU_RATE_CONTROL_ENABLE | JOB_OBJECT_CPU_RATE_CONTROL_HARD_CAP,
			CpuRate:      cpuRateVal,
		}

		r4, _, _ := procSetInformationJobObject.Call(
			uintptr(jobHandle),
			uintptr(JobObjectCpuRateControlInformation),
			uintptr(unsafe.Pointer(&cpuLimits)),
			uintptr(unsafe.Sizeof(cpuLimits)),
		)
		_ = r4 // Logged if needed
	}

	return job, nil
}

// AssignProcess assigns an OS process handle to the Job Object.
func (j *Job) AssignProcess(hProcess windows.Handle) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.isClosed {
		return fmt.Errorf("job is already closed")
	}

	r1, _, err := procAssignProcessToJobObject.Call(uintptr(j.handle), uintptr(hProcess))
	if r1 == 0 {
		return fmt.Errorf("AssignProcessToJobObject failed: %w", err)
	}
	return nil
}

// QueryAccounting retrieves microsecond execution times and process statistics from the kernel.
func (j *Job) QueryAccounting() (JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	var acct JOBOBJECT_BASIC_ACCOUNTING_INFORMATION
	var retLen uint32

	r1, _, err := procQueryInformationJobObject.Call(
		uintptr(j.handle),
		uintptr(JobObjectBasicAccountingInformation),
		uintptr(unsafe.Pointer(&acct)),
		uintptr(unsafe.Sizeof(acct)),
		uintptr(unsafe.Pointer(&retLen)),
	)
	if r1 == 0 {
		return acct, fmt.Errorf("QueryInformationJobObject(Accounting) failed: %w", err)
	}
	return acct, nil
}

// QueryExtendedInfo retrieves peak memory utilization from the kernel.
func (j *Job) QueryExtendedInfo() (JOBOBJECT_EXTENDED_LIMIT_INFORMATION, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	var ext JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	var retLen uint32

	r1, _, err := procQueryInformationJobObject.Call(
		uintptr(j.handle),
		uintptr(JobObjectExtendedLimitInformation),
		uintptr(unsafe.Pointer(&ext)),
		uintptr(unsafe.Sizeof(ext)),
		uintptr(unsafe.Pointer(&retLen)),
	)
	if r1 == 0 {
		return ext, fmt.Errorf("QueryInformationJobObject(Extended) failed: %w", err)
	}
	return ext, nil
}

// Terminate kills all processes in the job object immediately.
func (j *Job) Terminate(exitCode uint32) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.isClosed {
		return nil
	}

	r1, _, err := procTerminateJobObject.Call(uintptr(j.handle), uintptr(exitCode))
	if r1 == 0 {
		return fmt.Errorf("TerminateJobObject failed: %w", err)
	}
	return nil
}

// Close releases the Win32 Job Object handle.
// Because JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE was set, any remaining process is instantly destroyed.
func (j *Job) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.isClosed {
		return nil
	}
	j.isClosed = true
	return windows.CloseHandle(j.handle)
}

// Handle returns the underlying Windows Handle.
func (j *Job) Handle() windows.Handle {
	return j.handle
}

// ExecutionResult captures complete metrics and outputs of a sandboxed run.
type ExecutionResult struct {
	Stdout           string        `json:"stdout"`
	Stderr           string        `json:"stderr"`
	ExitCode         uint32        `json:"exit_code"`
	Status           string        `json:"status"` // COMPLETED, TIME_LIMIT_EXCEEDED, MEMORY_LIMIT_EXCEEDED, PROCESS_LIMIT_EXCEEDED, RUNTIME_ERROR
	ExecutionTime    time.Duration `json:"execution_time"`
	CpuKernelTimeUs  int64         `json:"cpu_kernel_time_us"`
	CpuUserTimeUs    int64         `json:"cpu_user_time_us"`
	PeakMemoryBytes  uint64        `json:"peak_memory_bytes"`
	PeakMemoryMB     float64       `json:"peak_memory_mb"`
	TotalProcesses   uint32        `json:"total_processes"`
	ColdStartLatency time.Duration `json:"cold_start_latency"`
}

// ExecuteProcess launches a command inside the Job Object with suspended initialization,
// stdin/stdout pipes, and real-time watchdog enforcement.
func (j *Job) ExecuteProcess(commandLine string, stdinContent string) (*ExecutionResult, error) {
	startInit := time.Now()

	// Create anonymous pipes for stdout and stderr
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	defer stdoutRead.Close()
	defer stdoutWrite.Close()

	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}
	defer stderrRead.Close()
	defer stderrWrite.Close()

	// Create pipe for stdin if input provided
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	defer stdinRead.Close()
	defer stdinWrite.Close()

	if len(stdinContent) > 0 {
		go func() {
			_, _ = stdinWrite.WriteString(stdinContent)
			_ = stdinWrite.Close()
		}()
	} else {
		_ = stdinWrite.Close()
	}

	// Prepare Win32 StartupInfo with redirected handles
	si := &windows.StartupInfo{
		Cb:         uint32(unsafe.Sizeof(windows.StartupInfo{})),
		Flags:      windows.STARTF_USESTDHANDLES,
		StdInput:   windows.Handle(stdinRead.Fd()),
		StdOutput:  windows.Handle(stdoutWrite.Fd()),
		StdErr:     windows.Handle(stderrWrite.Fd()),
	}

	pi := &windows.ProcessInformation{}

	cmdUTF16, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return nil, fmt.Errorf("invalid command line: %w", err)
	}

	var dirUTF16 *uint16
	if j.config.WorkingDir != "" {
		absDir, err := filepath.Abs(j.config.WorkingDir)
		if err == nil {
			dirUTF16, _ = windows.UTF16PtrFromString(absDir)
		}
	}

	// CRITICAL SECURITY PRIMITIVE:
	// Spawn the process with CREATE_SUSPENDED so it NEVER runs code before entering the Job Object.
	creationFlags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW | windows.CREATE_BREAKAWAY_FROM_JOB)

	err = windows.CreateProcess(
		nil,
		cmdUTF16,
		nil,
		nil,
		true, // Inherit pipe handles
		creationFlags,
		nil,
		dirUTF16,
		si,
		pi,
	)
	if err != nil {
		// If CREATE_BREAKAWAY_FROM_JOB is denied by parent job, retry without breakaway
		creationFlags = windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW
		err = windows.CreateProcess(nil, cmdUTF16, nil, nil, true, creationFlags, nil, dirUTF16, si, pi)
		if err != nil {
			return nil, fmt.Errorf("CreateProcess failed: %w", err)
		}
	}
	defer windows.CloseHandle(pi.Thread)
	defer windows.CloseHandle(pi.Process)

	// 1. Bind process to Job Object BEFORE unpausing execution
	if err := j.AssignProcess(pi.Process); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return nil, fmt.Errorf("failed assigning process to Job Object: %w", err)
	}

	// Launch-to-execution latency (Cold start)
	coldStartLatency := time.Since(startInit)

	// 2. Start asynchronous pipe readers
	var stdoutBuf, stderrBuf []byte
	var readWg sync.WaitGroup
	readWg.Add(2)

	go func() {
		defer readWg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := stdoutRead.Read(buf)
			if n > 0 {
				stdoutBuf = append(stdoutBuf, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
	}()

	go func() {
		defer readWg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := stderrRead.Read(buf)
			if n > 0 {
				stderrBuf = append(stderrBuf, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
	}()

	// 3. Resume the suspended main thread
	startTime := time.Now()
	_, _ = windows.ResumeThread(pi.Thread)

	// 4. Watchdog timer enforcement
	doneChan := make(chan error, 1)
	go func() {
		event, waitErr := windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		if waitErr != nil {
			doneChan <- waitErr
			return
		}
		if event == windows.WAIT_OBJECT_0 {
			doneChan <- nil
		} else {
			doneChan <- fmt.Errorf("wait failed with event 0x%X", event)
		}
	}()

	var timedOut bool
	select {
	case <-time.After(j.config.TimeLimit):
		timedOut = true
		j.timedOut = true
		// Hard kill entire job object instantly
		_ = j.Terminate(ExitCodeTimeLimitExceeded)
		<-doneChan
	case <-doneChan:
		// Process exited normally or by kernel limit
	}

	execDuration := time.Since(startTime)

	// Close write pipes so readers finish
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	readWg.Wait()

	// 5. Collect Kernel Accounting and Extended Memory Stats
	acct, _ := j.QueryAccounting()
	ext, _ := j.QueryExtendedInfo()

	var exitCode uint32
	_ = windows.GetExitCodeProcess(pi.Process, &exitCode)

	// Determine final execution status
	status := "COMPLETED"
	peakBytes := uint64(ext.PeakJobMemoryUsed)
	if peakBytes == 0 {
		peakBytes = uint64(ext.PeakProcessMemoryUsed)
	}

	if timedOut {
		status = "TIME_LIMIT_EXCEEDED"
		exitCode = ExitCodeTimeLimitExceeded
	} else if peakBytes >= j.config.MemoryLimitBytes*95/100 || exitCode == ExitCodeMemoryLimitExceeded {
		status = "MEMORY_LIMIT_EXCEEDED"
	} else if acct.TotalProcesses > j.config.MaxActiveProcesses && exitCode != 0 {
		status = "PROCESS_LIMIT_EXCEEDED"
	} else if exitCode != 0 {
		status = "RUNTIME_ERROR"
	}

	// 100ns units to microseconds
	cpuUserUs := acct.TotalUserTime / 10
	cpuKernelUs := acct.TotalKernelTime / 10

	return &ExecutionResult{
		Stdout:           string(stdoutBuf),
		Stderr:           string(stderrBuf),
		ExitCode:         exitCode,
		Status:           status,
		ExecutionTime:    execDuration,
		CpuKernelTimeUs:  cpuKernelUs,
		CpuUserTimeUs:    cpuUserUs,
		PeakMemoryBytes:  peakBytes,
		PeakMemoryMB:     float64(peakBytes) / (1024 * 1024),
		TotalProcesses:   acct.TotalProcesses,
		ColdStartLatency: coldStartLatency,
	}, nil
}

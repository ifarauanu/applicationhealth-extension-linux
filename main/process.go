package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Azure/applicationhealth-extension-linux/internal/handlerenv"
	"github.com/Azure/applicationhealth-extension-linux/internal/telemetry"
)

// Package-level function variables to allow mocking in tests
var (
	findExistingProcesses  = findExistingProcessesImpl
	getLogFileLastWriteTime = getLogFileLastWriteTimeImpl
	getHandlerEnvironment  = handlerenv.GetHandlerEnviroment
	killProcesses          = killProcessesImpl
)

// findExistingProcessesImpl scans /proc to find all other running instances of the
// Application Health Extension binary (excluding the current process).
// Uses /proc/<pid>/exe (kernel-controlled symlink to the actual binary) for process
// identification, which cannot be spoofed unlike /proc/<pid>/cmdline.
// Returns a slice of PIDs of existing processes (empty if none found).
func findExistingProcessesImpl() ([]int, error) {
	myPid := os.Getpid()
	var pids []int

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("failed to read /proc: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == myPid {
			continue
		}

		// Use /proc/<pid>/exe symlink to identify the binary — this is set by
		// the kernel and cannot be modified by the process itself.
		exePath, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if err != nil {
			continue
		}

		procName := filepath.Base(exePath)
		if procName != AppHealthBinaryNameAmd64 && procName != AppHealthBinaryNameArm64 {
			continue
		}

		// Verify the process is running with "enable" argument via cmdline
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		parts := strings.Split(string(cmdline), "\x00")
		if len(parts) >= 2 && parts[1] == "enable" {
			pids = append(pids, pid)
		}
	}

	return pids, nil
}

// getHandlerLogDir returns the handler log directory from the LOG_DIR
// environment variable (exported by the shim), falling back to DefaultHandlerLogDir.
func getHandlerLogDir() string {
	if dir := os.Getenv("LOG_DIR"); dir != "" {
		return dir
	}
	return DefaultHandlerLogDir
}

// getHandlerLogFile returns the handler log file name from the LOG_FILE
// environment variable (exported by the shim), falling back to DefaultHandlerLogFile.
func getHandlerLogFile() string {
	if file := os.Getenv("LOG_FILE"); file != "" {
		return file
	}
	return DefaultHandlerLogFile
}

// getLogFileLastWriteTimeImpl returns the last write time of the handler log file.
// Uses getHandlerLogDir() and getHandlerLogFile() to locate the file.
// This is used to determine if an existing AHE process is still responsive
// (writing heartbeat logs).
func getLogFileLastWriteTimeImpl() (time.Time, error) {
	logFilePath := filepath.Join(getHandlerLogDir(), getHandlerLogFile())
	info, err := os.Stat(logFilePath)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to stat handler log file %s: %w", logFilePath, err)
	}
	return info.ModTime(), nil
}

// isLogFileFresh checks whether the log file was updated within the stale
// threshold (AppHealthLogFileStaleThresholdInMinutes). Returns true if fresh,
// along with the last modification time and any error from reading the file.
func isLogFileFresh() (bool, time.Time, error) {
	lastWriteTime, err := getLogFileLastWriteTime()
	if err != nil {
		return false, time.Time{}, err
	}

	threshold := time.Duration(AppHealthLogFileStaleThresholdInMinutes) * time.Minute
	return time.Since(lastWriteTime) < threshold, lastWriteTime, nil
}

// killProcessesImpl sends SIGTERM to all specified processes and waits for each to exit.
// Logs a warning for any process that cannot be killed but continues with the rest.
func killProcessesImpl(lg *slog.Logger, pids []int) {
	for _, pid := range pids {
		if err := killProcess(pid); err != nil {
			logAndSend(lg, telemetry.WarningEvent, telemetry.AppHealthTask,
				fmt.Sprintf("Failed to terminate existing process %d: %v", pid, err),
				"pid", pid, "error", err)
		}
	}
}

// killProcess sends SIGTERM to the specified process and waits (bounded) for it
// to exit before returning. This prevents a race where two AHE instances run
// simultaneously during takeover.
func killProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process %d: %w", pid, err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM to process %d: %w", pid, err)
	}

	telemetry.SendEvent(telemetry.InfoEvent, telemetry.AppHealthTask,
		fmt.Sprintf("Sent SIGTERM to existing AHE process with PID %d, waiting for exit", pid))

	// Wait up to 5 seconds for the process to exit
	for i := 0; i < 10; i++ {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			// Process is gone
			telemetry.SendEvent(telemetry.InfoEvent, telemetry.AppHealthTask,
				fmt.Sprintf("Existing AHE process with PID %d has exited", pid))
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	// SIGTERM did not work — escalate to SIGKILL
	telemetry.SendEvent(telemetry.WarningEvent, telemetry.AppHealthTask,
		fmt.Sprintf("Existing AHE process with PID %d did not exit within 5 seconds after SIGTERM, sending SIGKILL", pid))
	if err := process.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("failed to send SIGKILL to process %d: %w", pid, err)
	}

	// Wait up to 2 seconds for the process to die after SIGKILL
	for i := 0; i < 4; i++ {
		time.Sleep(500 * time.Millisecond)
		if err := process.Signal(syscall.Signal(0)); err != nil {
			telemetry.SendEvent(telemetry.InfoEvent, telemetry.AppHealthTask,
				fmt.Sprintf("Existing AHE process with PID %d has exited after SIGKILL", pid))
			return nil
		}
	}

	return fmt.Errorf("process %d did not exit after SIGKILL within 2 seconds", pid)
}

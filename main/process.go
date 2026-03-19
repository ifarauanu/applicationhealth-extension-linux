package main

import (
	"fmt"
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
	findExistingProcess   = findExistingProcessImpl
	getLogFileLastModTime = getLogFileLastModTimeImpl
	getHandlerEnvironment = handlerenv.GetHandlerEnviroment
)

// findExistingProcessImpl scans /proc to find another running instance of the
// Application Health Extension binary (excluding the current process).
// Returns the PID of the existing process, or 0 if none found.
func findExistingProcessImpl() (int, error) {
	myPid := os.Getpid()

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, fmt.Errorf("failed to read /proc: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == myPid {
			continue
		}

		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}

		// /proc/<pid>/cmdline contains null-separated arguments
		cmdStr := string(cmdline)
		parts := strings.Split(cmdStr, "\x00")
		if len(parts) < 2 {
			continue
		}

		procName := filepath.Base(parts[0])
		// Check if the process is an AHE binary running with "enable" argument
		if (procName == AppHealthBinaryNameAmd64 || procName == AppHealthBinaryNameArm64) && parts[1] == "enable" {
			return pid, nil
		}
	}

	return 0, nil
}

// getLogFileLastModTimeImpl returns the modification time of the handler log file
// (handler.log) in the log folder. This is used to determine if an existing AHE
// process is still responsive (writing heartbeat logs).
// Only checks handler.log specifically to avoid false positives from other
// processes (logrotate, monitoring agents) touching files in the same folder.
func getLogFileLastModTimeImpl(logFolder string) (time.Time, error) {
	logFilePath := filepath.Join(logFolder, "handler.log")
	info, err := os.Stat(logFilePath)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to stat handler log file %s: %w", logFilePath, err)
	}
	return info.ModTime(), nil
}

// isLogFileFresh checks whether the log file was updated within the stale
// threshold (AppHealthLogFileStaleThresholdInMinutes). Returns true if fresh,
// along with the last modification time.
func isLogFileFresh(logFolder string) (bool, time.Time) {
	lastModTime, err := getLogFileLastModTime(logFolder)
	if err != nil {
		return false, time.Time{}
	}

	threshold := time.Duration(AppHealthLogFileStaleThresholdInMinutes) * time.Minute
	return time.Since(lastModTime) < threshold, lastModTime
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
	return nil
}

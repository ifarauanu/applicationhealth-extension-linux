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
	osExit                = os.Exit
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

// getLogFileLastModTimeImpl returns the most recent modification time among all
// log files in the handler's log folder. This is used to determine if an
// existing AHE process is still responsive (writing heartbeat logs).
func getLogFileLastModTimeImpl(logFolder string) (time.Time, error) {
	entries, err := os.ReadDir(logFolder)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to read log folder %s: %w", logFolder, err)
	}

	var mostRecent time.Time
	found := false

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().After(mostRecent) {
			mostRecent = info.ModTime()
			found = true
		}
	}

	if !found {
		return time.Time{}, fmt.Errorf("no log files found in %s", logFolder)
	}

	return mostRecent, nil
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

// killProcess sends SIGTERM to the specified process to allow graceful shutdown.
func killProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process %d: %w", pid, err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM to process %d: %w", pid, err)
	}

	telemetry.SendEvent(telemetry.InfoEvent, telemetry.AppHealthTask,
		fmt.Sprintf("Sent SIGTERM to existing AHE process with PID %d", pid))
	return nil
}

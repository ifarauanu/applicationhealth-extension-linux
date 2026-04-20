package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spawnDetachedProcess starts a process via bash double-fork so it's not a direct
// child of the test process. This avoids zombie issues with Signal(0) checks.
// Returns the PID of the spawned process.
func spawnDetachedProcess(t *testing.T, shellCmd string) int {
	t.Helper()
	// Use bash to double-fork: the inner process writes its PID to a temp file
	pidFile := t.TempDir() + "/pid"
	cmd := exec.Command("bash", "-c",
		"("+shellCmd+" & echo $! > "+pidFile+")")
	require.NoError(t, cmd.Run(), "failed to spawn detached process")

	// Read the PID
	pidBytes, err := os.ReadFile(pidFile)
	require.NoError(t, err, "failed to read PID file")
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	require.NoError(t, err, "failed to parse PID")
	return pid
}

func Test_killProcess_SIGTERM(t *testing.T) {
	pid := spawnDetachedProcess(t, "sleep 60")

	err := killProcess(pid)
	assert.NoError(t, err, "killProcess should succeed for a process that handles SIGTERM")
}

func Test_killProcess_SIGKILL_escalation(t *testing.T) {
	// Spawn a process that ignores SIGTERM
	pid := spawnDetachedProcess(t, "trap '' TERM; sleep 60")

	startTime := time.Now()
	err := killProcess(pid)
	elapsed := time.Since(startTime)

	assert.NoError(t, err, "killProcess should succeed after escalating to SIGKILL")
	assert.True(t, elapsed >= 5*time.Second, "should have waited for SIGTERM timeout before SIGKILL")
}

func Test_killProcess_NonExistentPID(t *testing.T) {
	err := killProcess(9999999)
	assert.Error(t, err, "killProcess should return error for non-existent PID")
}

func Test_killProcess_AlreadyExited(t *testing.T) {
	cmd := exec.Command("true")
	require.NoError(t, cmd.Start(), "failed to start test process")
	pid := cmd.Process.Pid
	cmd.Wait()

	err := killProcess(pid)
	assert.Error(t, err, "killProcess should return error for already-exited process")
}

func Test_getHandlerLogDir(t *testing.T) {
	assert.Equal(t, DefaultHandlerLogDir, getHandlerLogDir())
	assert.Equal(t, "/var/log/azure/applicationhealth-extension", getHandlerLogDir())
}

func Test_getHandlerLogFile(t *testing.T) {
	assert.Equal(t, DefaultHandlerLogFile, getHandlerLogFile())
	assert.Equal(t, "handler.log", getHandlerLogFile())
}

func Test_getLogFileLastWriteTime(t *testing.T) {
	t.Run("ReturnsModTimeForExistingFile", func(t *testing.T) {
		origGetLogFileLastWriteTime := getLogFileLastWriteTime
		defer func() { getLogFileLastWriteTime = origGetLogFileLastWriteTime }()

		expectedTime := time.Now().Add(-2 * time.Minute)
		getLogFileLastWriteTime = func() (time.Time, error) {
			return expectedTime, nil
		}

		modTime, err := getLogFileLastWriteTime()
		assert.NoError(t, err)
		assert.Equal(t, expectedTime, modTime)
	})

	t.Run("ReturnsErrorForMissingFile", func(t *testing.T) {
		origGetLogFileLastWriteTime := getLogFileLastWriteTime
		defer func() { getLogFileLastWriteTime = origGetLogFileLastWriteTime }()

		getLogFileLastWriteTime = func() (time.Time, error) {
			return time.Time{}, fmt.Errorf("failed to stat handler log file: no such file or directory")
		}

		_, err := getLogFileLastWriteTime()
		assert.Error(t, err)
	})
}

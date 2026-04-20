package main

import (
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
	t.Run("ReturnsEnvVarWhenSet", func(t *testing.T) {
		t.Setenv("LOG_DIR", "/custom/log/dir")
		assert.Equal(t, "/custom/log/dir", getHandlerLogDir())
	})

	t.Run("ReturnsDefaultWhenEnvVarNotSet", func(t *testing.T) {
		t.Setenv("LOG_DIR", "")
		assert.Equal(t, DefaultHandlerLogDir, getHandlerLogDir())
	})
}

func Test_getHandlerLogFile(t *testing.T) {
	t.Run("ReturnsEnvVarWhenSet", func(t *testing.T) {
		t.Setenv("LOG_FILE", "custom.log")
		assert.Equal(t, "custom.log", getHandlerLogFile())
	})

	t.Run("ReturnsDefaultWhenEnvVarNotSet", func(t *testing.T) {
		t.Setenv("LOG_FILE", "")
		assert.Equal(t, DefaultHandlerLogFile, getHandlerLogFile())
	})
}

func Test_getLogFileLastWriteTime(t *testing.T) {
	t.Run("ReturnsModTimeForExistingFile", func(t *testing.T) {
		tmpDir := t.TempDir()
		logFile := tmpDir + "/" + DefaultHandlerLogFile
		require.NoError(t, os.WriteFile(logFile, []byte("test"), 0644))

		t.Setenv("LOG_DIR", tmpDir)
		t.Setenv("LOG_FILE", DefaultHandlerLogFile)

		modTime, err := getLogFileLastWriteTime()
		assert.NoError(t, err)
		assert.False(t, modTime.IsZero())
		assert.WithinDuration(t, time.Now(), modTime, 5*time.Second)
	})

	t.Run("ReturnsErrorForMissingFile", func(t *testing.T) {
		t.Setenv("LOG_DIR", "/nonexistent/path")
		t.Setenv("LOG_FILE", "handler.log")

		_, err := getLogFileLastWriteTime()
		assert.Error(t, err)
	})
}

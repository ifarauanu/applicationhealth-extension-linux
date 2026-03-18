package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Azure/applicationhealth-extension-linux/internal/handlerenv"
	"github.com/Azure/applicationhealth-extension-linux/internal/seqno"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// mockHandlerEnv creates a test HandlerEnvironment with a temp log folder
func mockHandlerEnv(t *testing.T) (*handlerenv.HandlerEnvironment, string) {
	t.Helper()
	logFolder := t.TempDir()
	hEnv := &handlerenv.HandlerEnvironment{}
	hEnv.LogFolder = logFolder
	return hEnv, logFolder
}

// saveAndRestoreIdempotencyFuncs saves the current injectable functions and restores them after the test
func saveAndRestoreIdempotencyFuncs(t *testing.T) {
	t.Helper()
	origProcessDetector := processDetectorFunc
	origHeartbeatChecker := heartbeatCheckerFunc
	origExitProcess := exitProcessFunc
	origGetHandlerEnv := getHandlerEnvFunc
	t.Cleanup(func() {
		processDetectorFunc = origProcessDetector
		heartbeatCheckerFunc = origHeartbeatChecker
		exitProcessFunc = origExitProcess
		getHandlerEnvFunc = origGetHandlerEnv
	})
}

func Test_checkIdempotency_SameSeqHealthyProcess_ShouldExit(t *testing.T) {
	// Test case 1: Same seq number + healthy process → should exit
	saveAndRestoreIdempotencyFuncs(t)
	lg := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	hEnv, logFolder := mockHandlerEnv(t)

	// Write a fresh heartbeat file
	heartbeatPath := filepath.Join(logFolder, AppHealthHeartbeatFileName)
	err := os.WriteFile(heartbeatPath, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0644)
	require.NoError(t, err)

	getHandlerEnvFunc = func() (*handlerenv.HandlerEnvironment, error) {
		return hEnv, nil
	}
	heartbeatCheckerFunc = GetHeartbeatFileLastWriteTime
	processDetectorFunc = func(lg *slog.Logger) (bool, int) {
		return true, 1234 // another process is running
	}

	shouldExit := checkIdempotency(lg, 5)
	assert.True(t, shouldExit, "Should exit when same seq number and healthy process exists")
}

func Test_checkIdempotency_SameSeqUnhealthyProcess_ShouldNotExit(t *testing.T) {
	// Test case 2: Same seq number + unhealthy process → should take over
	saveAndRestoreIdempotencyFuncs(t)
	lg := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	hEnv, logFolder := mockHandlerEnv(t)

	// Write a stale heartbeat file (7 minutes old)
	heartbeatPath := filepath.Join(logFolder, AppHealthHeartbeatFileName)
	staleTime := time.Now().Add(-7 * time.Minute)
	err := os.WriteFile(heartbeatPath, []byte(staleTime.UTC().Format(time.RFC3339Nano)), 0644)
	require.NoError(t, err)
	os.Chtimes(heartbeatPath, staleTime, staleTime)

	getHandlerEnvFunc = func() (*handlerenv.HandlerEnvironment, error) {
		return hEnv, nil
	}
	heartbeatCheckerFunc = GetHeartbeatFileLastWriteTime
	processDetectorFunc = func(lg *slog.Logger) (bool, int) {
		return true, 1234 // another process is running but unhealthy
	}

	shouldExit := checkIdempotency(lg, 5)
	assert.False(t, shouldExit, "Should not exit when same seq number and unhealthy process exists")
}

func Test_checkIdempotency_SameSeqNoProcess_ShouldNotExit(t *testing.T) {
	// Test case 7: No existing process → should proceed normally
	saveAndRestoreIdempotencyFuncs(t)
	lg := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	hEnv, logFolder := mockHandlerEnv(t)

	// Write a fresh heartbeat file but no process running
	heartbeatPath := filepath.Join(logFolder, AppHealthHeartbeatFileName)
	err := os.WriteFile(heartbeatPath, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0644)
	require.NoError(t, err)

	getHandlerEnvFunc = func() (*handlerenv.HandlerEnvironment, error) {
		return hEnv, nil
	}
	heartbeatCheckerFunc = GetHeartbeatFileLastWriteTime
	processDetectorFunc = func(lg *slog.Logger) (bool, int) {
		return false, 0 // no process running
	}

	shouldExit := checkIdempotency(lg, 5)
	assert.False(t, shouldExit, "Should not exit when no existing process")
}

func Test_checkIdempotency_NoHeartbeatFile_ShouldNotExit(t *testing.T) {
	// No heartbeat file exists → stale, should not exit even if process detected
	saveAndRestoreIdempotencyFuncs(t)
	lg := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	hEnv, _ := mockHandlerEnv(t)

	// No heartbeat file written
	getHandlerEnvFunc = func() (*handlerenv.HandlerEnvironment, error) {
		return hEnv, nil
	}
	heartbeatCheckerFunc = GetHeartbeatFileLastWriteTime
	processDetectorFunc = func(lg *slog.Logger) (bool, int) {
		return true, 1234
	}

	shouldExit := checkIdempotency(lg, 5)
	assert.False(t, shouldExit, "Should not exit when heartbeat file does not exist (stale)")
}

func Test_checkIdempotency_HandlerEnvError_ShouldNotExit(t *testing.T) {
	// Handler environment error → should proceed normally
	saveAndRestoreIdempotencyFuncs(t)
	lg := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	getHandlerEnvFunc = func() (*handlerenv.HandlerEnvironment, error) {
		return nil, assert.AnError
	}

	shouldExit := checkIdempotency(lg, 5)
	assert.False(t, shouldExit, "Should not exit when handler env is unavailable")
}

func Test_enablePre_SameSeqHealthyProcess_ExitsWithZero(t *testing.T) {
	// Test case 1 end-to-end: enablePre should call exitProcessFunc(0)
	saveAndRestoreIdempotencyFuncs(t)

	lg := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctrl := gomock.NewController(t)
	mockSeqNumManager := seqno.NewMockSequenceNumberManager(ctrl)

	hEnv, logFolder := mockHandlerEnv(t)

	// Write a fresh heartbeat file
	heartbeatPath := filepath.Join(logFolder, AppHealthHeartbeatFileName)
	err := os.WriteFile(heartbeatPath, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0644)
	require.NoError(t, err)

	// Same seq number
	mockSeqNumManager.EXPECT().GetCurrentSequenceNumber(gomock.Any(), gomock.Any(), gomock.Any()).Return(uint(5), nil)
	seqnoManager = mockSeqNumManager

	getHandlerEnvFunc = func() (*handlerenv.HandlerEnvironment, error) {
		return hEnv, nil
	}
	heartbeatCheckerFunc = GetHeartbeatFileLastWriteTime
	processDetectorFunc = func(lg *slog.Logger) (bool, int) {
		return true, 1234
	}

	exitCalled := false
	exitCode := -1
	exitProcessFunc = func(code int) {
		exitCalled = true
		exitCode = code
	}

	err = enablePre(lg, 5)
	assert.NoError(t, err)
	assert.True(t, exitCalled, "exitProcessFunc should have been called")
	assert.Equal(t, 0, exitCode, "Should exit with code 0 for idempotency")
}

func Test_enablePre_SameSeqUnhealthyProcess_Continues(t *testing.T) {
	// Test case 2: Same seq + unhealthy → takes over, SetSequenceNumber called
	saveAndRestoreIdempotencyFuncs(t)

	lg := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctrl := gomock.NewController(t)
	mockSeqNumManager := seqno.NewMockSequenceNumberManager(ctrl)

	hEnv, logFolder := mockHandlerEnv(t)

	// Write a stale heartbeat file
	heartbeatPath := filepath.Join(logFolder, AppHealthHeartbeatFileName)
	staleTime := time.Now().Add(-7 * time.Minute)
	err := os.WriteFile(heartbeatPath, []byte(staleTime.UTC().Format(time.RFC3339Nano)), 0644)
	require.NoError(t, err)
	os.Chtimes(heartbeatPath, staleTime, staleTime)

	mockSeqNumManager.EXPECT().GetCurrentSequenceNumber(gomock.Any(), gomock.Any(), gomock.Any()).Return(uint(5), nil)
	mockSeqNumManager.EXPECT().SetSequenceNumber(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	seqnoManager = mockSeqNumManager

	getHandlerEnvFunc = func() (*handlerenv.HandlerEnvironment, error) {
		return hEnv, nil
	}
	heartbeatCheckerFunc = GetHeartbeatFileLastWriteTime
	processDetectorFunc = func(lg *slog.Logger) (bool, int) {
		return true, 1234 // unhealthy process
	}

	exitCalled := false
	exitProcessFunc = func(code int) {
		exitCalled = true
	}

	err = enablePre(lg, 5)
	assert.NoError(t, err, "Should succeed and take over")
	assert.False(t, exitCalled, "exitProcessFunc should not have been called")
}

func Test_enablePre_HigherSeqNum_ShouldFail(t *testing.T) {
	// Test case 5: New process has lower seq than existing → exits with error
	saveAndRestoreIdempotencyFuncs(t)

	lg := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctrl := gomock.NewController(t)
	mockSeqNumManager := seqno.NewMockSequenceNumberManager(ctrl)

	mockSeqNumManager.EXPECT().GetCurrentSequenceNumber(gomock.Any(), gomock.Any(), gomock.Any()).Return(uint(10), nil)
	seqnoManager = mockSeqNumManager

	err := enablePre(lg, 5)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "most recent sequence number 10 is greater than the requested sequence number 5")
}

func Test_enablePre_LowerSeqNum_ShouldPass(t *testing.T) {
	// Test case 3: New process has higher seq → proceeds normally
	saveAndRestoreIdempotencyFuncs(t)

	lg := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctrl := gomock.NewController(t)
	mockSeqNumManager := seqno.NewMockSequenceNumberManager(ctrl)

	mockSeqNumManager.EXPECT().GetCurrentSequenceNumber(gomock.Any(), gomock.Any(), gomock.Any()).Return(uint(5), nil)
	mockSeqNumManager.EXPECT().SetSequenceNumber(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	seqnoManager = mockSeqNumManager

	err := enablePre(lg, 10)
	assert.NoError(t, err)
}

func Test_checkAndHandleStaleSequenceNumber_Stale_ShouldReturnTrue(t *testing.T) {
	// When current mrSeq is higher than our seq, should kill VMWatch and return true
	lg := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctrl := gomock.NewController(t)
	mockSeqNumManager := seqno.NewMockSequenceNumberManager(ctrl)

	origSeqNoManager := seqnoManager
	defer func() { seqnoManager = origSeqNoManager }()

	mockSeqNumManager.EXPECT().GetCurrentSequenceNumber(gomock.Any(), gomock.Any(), gomock.Any()).Return(uint(10), nil)
	seqnoManager = mockSeqNumManager

	// vmWatchCommand is nil, so killVMWatch should handle it gracefully
	result := checkAndHandleStaleSequenceNumber(lg, 5)
	assert.True(t, result, "Should return true when sequence number is stale")
}

func Test_checkAndHandleStaleSequenceNumber_Current_ShouldReturnFalse(t *testing.T) {
	// When our seq is current, should return false
	lg := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctrl := gomock.NewController(t)
	mockSeqNumManager := seqno.NewMockSequenceNumberManager(ctrl)

	origSeqNoManager := seqnoManager
	defer func() { seqnoManager = origSeqNoManager }()

	mockSeqNumManager.EXPECT().GetCurrentSequenceNumber(gomock.Any(), gomock.Any(), gomock.Any()).Return(uint(5), nil)
	seqnoManager = mockSeqNumManager

	result := checkAndHandleStaleSequenceNumber(lg, 5)
	assert.False(t, result, "Should return false when sequence number is current")
}

func Test_checkAndHandleStaleSequenceNumber_Higher_ShouldReturnFalse(t *testing.T) {
	// When our seq is higher, should return false
	lg := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctrl := gomock.NewController(t)
	mockSeqNumManager := seqno.NewMockSequenceNumberManager(ctrl)

	origSeqNoManager := seqnoManager
	defer func() { seqnoManager = origSeqNoManager }()

	mockSeqNumManager.EXPECT().GetCurrentSequenceNumber(gomock.Any(), gomock.Any(), gomock.Any()).Return(uint(5), nil)
	seqnoManager = mockSeqNumManager

	result := checkAndHandleStaleSequenceNumber(lg, 10)
	assert.False(t, result, "Should return false when our sequence number is higher")
}

func Test_GetHeartbeatFileLastWriteTime_FileExists(t *testing.T) {
	logFolder := t.TempDir()
	heartbeatPath := filepath.Join(logFolder, AppHealthHeartbeatFileName)

	// Write heartbeat file
	err := os.WriteFile(heartbeatPath, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0644)
	require.NoError(t, err)

	writeTime, err := GetHeartbeatFileLastWriteTime(logFolder)
	assert.NoError(t, err)
	assert.WithinDuration(t, time.Now(), writeTime, 2*time.Second)
}

func Test_GetHeartbeatFileLastWriteTime_FileNotExists(t *testing.T) {
	logFolder := t.TempDir()

	_, err := GetHeartbeatFileLastWriteTime(logFolder)
	assert.Error(t, err)
}

func Test_writeHeartbeatFile(t *testing.T) {
	logFolder := t.TempDir()
	origPath := heartbeatFilePath
	defer func() { heartbeatFilePath = origPath }()

	heartbeatFilePath = filepath.Join(logFolder, AppHealthHeartbeatFileName)
	writeHeartbeatFile()

	// Verify file was created
	info, err := os.Stat(heartbeatFilePath)
	require.NoError(t, err)
	assert.False(t, info.IsDir())

	// Verify content is a timestamp
	content, err := os.ReadFile(heartbeatFilePath)
	require.NoError(t, err)
	_, err = time.Parse(time.RFC3339Nano, string(content))
	assert.NoError(t, err, "Heartbeat file should contain a valid RFC3339Nano timestamp")
}

func Test_writeHeartbeatFile_EmptyPath(t *testing.T) {
	origPath := heartbeatFilePath
	defer func() { heartbeatFilePath = origPath }()

	heartbeatFilePath = ""
	// Should not panic
	writeHeartbeatFile()
}

func Test_LogHeartBeat_WritesHeartbeatFile(t *testing.T) {
	logFolder := t.TempDir()
	origPath := heartbeatFilePath
	origTime := timeOfLastAppHealthLog
	defer func() {
		heartbeatFilePath = origPath
		timeOfLastAppHealthLog = origTime
	}()

	heartbeatFilePath = filepath.Join(logFolder, AppHealthHeartbeatFileName)
	// Reset timer to force heartbeat write
	timeOfLastAppHealthLog = time.Time{}

	LogHeartBeat()

	// Verify heartbeat file exists
	_, err := os.Stat(heartbeatFilePath)
	assert.NoError(t, err, "Heartbeat file should be created after LogHeartBeat")
}

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var debugLogMu sync.Mutex

const maxDebugLogBytes = 512 * 1024 // 512 KB rolling limit

// DebugLogPath returns %LOCALAPPDATA%\AroniumUtility\logs\debug.log (or OS equivalent).
func DebugLogPath() string {
	return filepath.Join(DefaultAppDataDir(), "logs", "debug.log")
}

// ServiceLogPath returns %LOCALAPPDATA%\AroniumUtility\logs\service.log (or OS equivalent).
func ServiceLogPath() string {
	return filepath.Join(DefaultAppDataDir(), "logs", "service.log")
}

// AppendDebugLog writes a structured timestamped diagnostic line to debug.log with rotation and PII redaction.
func AppendDebugLog(source, command, status string, durationMs int64, details string) {
	debugLogMu.Lock()
	defer debugLogMu.Unlock()

	logPath := DebugLogPath()
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)

	if info, err := os.Stat(logPath); err == nil && info.Size() > maxDebugLogBytes {
		if raw, rErr := os.ReadFile(logPath); rErr == nil && len(raw) > maxDebugLogBytes/2 {
			tail := raw[len(raw)-(maxDebugLogBytes/2):]
			_ = os.WriteFile(logPath, tail, 0644)
		}
	}

	ts := time.Now().Format("2006-01-02 15:04:05.000")
	cleanDetails := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(details, "\r", " "), "\n", " | "))
	cleanDetails = RedactPII(cleanDetails)
	if len(cleanDetails) > 900 {
		cleanDetails = cleanDetails[:900] + "... [truncado]"
	}
	line := fmt.Sprintf("[%s] [%s] [%s] (%dms) cmd=%q | %s\n",
		ts, source, status, durationMs, RedactPII(command), cleanDetails)

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}

// ReadDebugLogs reads the tail of debug.log up to maxBytes.
func ReadDebugLogs(maxBytes int) (string, error) {
	if maxBytes <= 0 {
		maxBytes = 65536
	}
	raw, err := os.ReadFile(DebugLogPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "(Sin registros en debug.log todavía)", nil
		}
		return "", err
	}
	if len(raw) > maxBytes {
		raw = raw[len(raw)-maxBytes:]
	}
	return RedactPII(string(raw)), nil
}

// ReadServiceLogs reads the tail of service.log up to maxBytes.
func ReadServiceLogs(maxBytes int) (string, error) {
	if maxBytes <= 0 {
		maxBytes = 32768
	}
	raw, err := os.ReadFile(ServiceLogPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "(Sin registros en service.log todavía)", nil
		}
		return "", err
	}
	if len(raw) > maxBytes {
		raw = raw[len(raw)-maxBytes:]
	}
	return RedactPII(string(raw)), nil
}

// ClearDebugLogs truncates debug.log and records the reset marker.
func ClearDebugLogs() error {
	debugLogMu.Lock()
	defer debugLogMu.Unlock()

	logPath := DebugLogPath()
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	return os.WriteFile(logPath, []byte(fmt.Sprintf("[%s] [LOGGER] [RESET] Historial de debug limpiado por el usuario.\n", ts)), 0644)
}

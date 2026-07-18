package runtime

import (
	"bytes"
	"os/exec"
	"runtime"
)

// ExecRun executes a shell command and returns stdout, stderr, and exit code.
// Format: exec.run("command")
func ExecRun(cmdStr string) interface{} {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-Command", cmdStr)
	} else {
		cmd = exec.Command("sh", "-c", cmdStr)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return map[string]interface{}{
		"stdout":   stdout.String(),
		"stderr":   stderr.String(),
		"exitCode": float64(exitCode),
	}
}

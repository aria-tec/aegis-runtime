package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
)

type ProcessRunner struct {
	baseWorkDir string
}

func NewProcessRunner(baseWorkDir string) *ProcessRunner {
	if baseWorkDir == "" {
		baseWorkDir = "scratch"
	}
	return &ProcessRunner{baseWorkDir: baseWorkDir}
}

func (p *ProcessRunner) Execute(ctx context.Context, req domain.ToolExecutionRequest) (*domain.ToolExecutionResult, error) {
	timeout := time.Duration(req.TimeoutSecs) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Prepare isolated work directory
	targetDir := filepath.Join(p.baseWorkDir, req.WorkflowID)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sandbox dir: %w", err)
	}

	cmd := exec.CommandContext(execCtx, req.Command, req.Args...)
	cmd.Dir = targetDir

	// SECURITY: Scrub host environment, strictly pass only whitelisted envs
	cleanEnv := []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin",
		"TMPDIR=" + targetDir,
	}
	for k, v := range req.Env {
		cleanEnv = append(cleanEnv, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = cleanEnv

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if req.Stdin != "" {
		cmd.Stdin = strings.NewReader(req.Stdin)
	}

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return &domain.ToolExecutionResult{
		ToolName: req.ToolName,
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
		Error:    errMsg,
	}, nil
}

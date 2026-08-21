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

// DefaultMaxOutputBytes limits stdout/stderr capture to 1MB to prevent OOM DOS attacks.
const DefaultMaxOutputBytes = 1 * 1024 * 1024

// boundedBuffer captures process output up to a maximum limit and discards excess data safely.
type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newBoundedBuffer(limit int) *boundedBuffer {
	if limit <= 0 {
		limit = DefaultMaxOutputBytes
	}
	return &boundedBuffer{limit: limit}
}

func (b *boundedBuffer) Write(p []byte) (n int, err error) {
	if b.buf.Len() >= b.limit {
		b.truncated = true
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *boundedBuffer) String() string {
	s := b.buf.String()
	if b.truncated {
		s += "\n[TRUNCATED: Output exceeded safety buffer limit]"
	}
	return s
}

type ProcessRunner struct {
	baseWorkDir    string
	maxOutputBytes int
}

func NewProcessRunner(baseWorkDir string) *ProcessRunner {
	if baseWorkDir == "" {
		baseWorkDir = "scratch"
	}
	return &ProcessRunner{
		baseWorkDir:    baseWorkDir,
		maxOutputBytes: DefaultMaxOutputBytes,
	}
}

func (p *ProcessRunner) SetMaxOutputBytes(limit int) {
	if limit > 0 {
		p.maxOutputBytes = limit
	}
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

	// Output bomb protection: Bounded buffers prevent OOM attacks from runaway stdout/stderr
	stdout := newBoundedBuffer(p.maxOutputBytes)
	stderr := newBoundedBuffer(p.maxOutputBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

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

package sandbox_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/sandbox"
)

func TestProcessRunner_Execute_BasicEcho(t *testing.T) {
	tempDir := t.TempDir()
	runner := sandbox.NewProcessRunner(tempDir)
	ctx := context.Background()

	req := domain.ToolExecutionRequest{
		WorkflowID:  "wf-test-echo",
		StepNumber:  1,
		ToolName:    "echo_tool",
		Command:     "echo",
		Args:        []string{"hello", "world"},
		TimeoutSecs: 5,
	}

	result, err := runner.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ToolName != "echo_tool" {
		t.Fatalf("expected tool name echo_tool, got %s", result.ToolName)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", result.ExitCode, result.Stderr)
	}
	if strings.TrimSpace(result.Stdout) != "hello world" {
		t.Fatalf("expected stdout 'hello world', got '%s'", result.Stdout)
	}
	if result.Error != "" {
		t.Fatalf("expected no error message, got '%s'", result.Error)
	}
	if result.Duration <= 0 {
		t.Fatalf("expected duration > 0, got %v", result.Duration)
	}

	// Verify working directory was created
	expectedWfDir := filepath.Join(tempDir, "wf-test-echo")
	if info, err := os.Stat(expectedWfDir); err != nil || !info.IsDir() {
		t.Fatalf("expected workflow directory %s to exist as directory", expectedWfDir)
	}
}

func TestProcessRunner_ExecuteWithEnvScrubbing(t *testing.T) {
	os.Setenv("SECRET_HOST_KEY", "SUPER_SECRET_VALUE_DO_NOT_LEAK")
	os.Setenv("HOST_API_KEY", "SECRET_API_TOKEN_XYZ")
	defer func() {
		os.Unsetenv("SECRET_HOST_KEY")
		os.Unsetenv("HOST_API_KEY")
	}()

	tempDir := t.TempDir()
	runner := sandbox.NewProcessRunner(tempDir)
	ctx := context.Background()

	req := domain.ToolExecutionRequest{
		WorkflowID:  "wf-test-sandbox",
		StepNumber:  1,
		ToolName:    "print_env",
		Command:     "env",
		TimeoutSecs: 5,
		Env: map[string]string{
			"ALLOWED_VAR": "SAFE_123",
			"CUSTOM_OPT":  "ENABLED",
		},
	}

	result, err := runner.Execute(ctx, req)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", result.ExitCode, result.Stderr)
	}

	// SECURITY: Host secrets must NOT be present
	if strings.Contains(result.Stdout, "SUPER_SECRET_VALUE_DO_NOT_LEAK") || strings.Contains(result.Stdout, "SECRET_HOST_KEY") {
		t.Fatalf("SECURITY VIOLATION: SECRET_HOST_KEY leaked into tool execution environment! Output: %s", result.Stdout)
	}
	if strings.Contains(result.Stdout, "SECRET_API_TOKEN_XYZ") || strings.Contains(result.Stdout, "HOST_API_KEY") {
		t.Fatalf("SECURITY VIOLATION: HOST_API_KEY leaked into tool execution environment! Output: %s", result.Stdout)
	}

	// Whitelisted env vars must be present
	if !strings.Contains(result.Stdout, "ALLOWED_VAR=SAFE_123") {
		t.Fatalf("expected ALLOWED_VAR=SAFE_123 in stdout, got: %s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "CUSTOM_OPT=ENABLED") {
		t.Fatalf("expected CUSTOM_OPT=ENABLED in stdout, got: %s", result.Stdout)
	}

	// TargetDir and PATH should be present
	targetDir := filepath.Join(tempDir, "wf-test-sandbox")
	if !strings.Contains(result.Stdout, "TMPDIR="+targetDir) {
		t.Fatalf("expected TMPDIR=%s in stdout, got: %s", targetDir, result.Stdout)
	}
	if !strings.Contains(result.Stdout, "PATH=/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin") {
		t.Fatalf("expected sanitized PATH in stdout, got: %s", result.Stdout)
	}
}

func TestProcessRunner_OutputBombProtection(t *testing.T) {
	tempDir := t.TempDir()
	runner := sandbox.NewProcessRunner(tempDir)
	// Set small limit (1KB) for fast deterministic test
	runner.SetMaxOutputBytes(1024)
	ctx := context.Background()

	// Tool generates 50KB of data
	req := domain.ToolExecutionRequest{
		WorkflowID:  "wf-test-output-bomb",
		StepNumber:  1,
		ToolName:    "flood_tool",
		Command:     "sh",
		Args:        []string{"-c", "for i in $(seq 1 1000); do echo 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'; done"},
		TimeoutSecs: 5,
	}

	result, err := runner.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}

	// Verify stdout is capped and contains truncation warning
	if len(result.Stdout) > 2048 {
		t.Fatalf("SECURITY VIOLATION: output exceeded safety buffer! Buffer length: %d", len(result.Stdout))
	}
	if !strings.Contains(result.Stdout, "[TRUNCATED: Output exceeded safety buffer limit]") {
		t.Fatalf("expected truncation notice in output, got: %s", result.Stdout)
	}
}

func TestProcessRunner_Stdin(t *testing.T) {
	tempDir := t.TempDir()
	runner := sandbox.NewProcessRunner(tempDir)
	ctx := context.Background()

	input := "hello stdin streaming test data\nsecond line"
	req := domain.ToolExecutionRequest{
		WorkflowID:  "wf-test-stdin",
		StepNumber:  1,
		ToolName:    "cat_tool",
		Command:     "cat",
		Stdin:       input,
		TimeoutSecs: 5,
	}

	result, err := runner.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.Stdout != input {
		t.Fatalf("expected stdout '%s', got '%s'", input, result.Stdout)
	}
}

func TestProcessRunner_TimeoutEnforcement(t *testing.T) {
	tempDir := t.TempDir()
	runner := sandbox.NewProcessRunner(tempDir)
	ctx := context.Background()

	start := time.Now()
	req := domain.ToolExecutionRequest{
		WorkflowID:  "wf-test-timeout",
		StepNumber:  1,
		ToolName:    "sleep_tool",
		Command:     "sleep",
		Args:        []string{"3"},
		TimeoutSecs: 1,
	}

	result, err := runner.Execute(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if elapsed > 2500*time.Millisecond {
		t.Fatalf("expected execution to time out near 1s, took %v", elapsed)
	}
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit code on timeout, got %d", result.ExitCode)
	}
	if result.Error == "" {
		t.Fatalf("expected error message on timeout, got empty string")
	}
}

func TestProcessRunner_NonZeroExitCode(t *testing.T) {
	tempDir := t.TempDir()
	runner := sandbox.NewProcessRunner(tempDir)
	ctx := context.Background()

	req := domain.ToolExecutionRequest{
		WorkflowID:  "wf-test-exitcode",
		StepNumber:  1,
		ToolName:    "sh_tool",
		Command:     "sh",
		Args:        []string{"-c", "echo 'failed output' >&2; exit 42"},
		TimeoutSecs: 5,
	}

	result, err := runner.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if result.ExitCode != 42 {
		t.Fatalf("expected exit code 42, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "failed output") {
		t.Fatalf("expected stderr to contain 'failed output', got '%s'", result.Stderr)
	}
	if result.Error == "" {
		t.Fatalf("expected non-empty Error string for non-zero exit code")
	}
}

func TestProcessRunner_InvalidCommand(t *testing.T) {
	tempDir := t.TempDir()
	runner := sandbox.NewProcessRunner(tempDir)
	ctx := context.Background()

	req := domain.ToolExecutionRequest{
		WorkflowID:  "wf-test-invalid-cmd",
		StepNumber:  1,
		ToolName:    "invalid_tool",
		Command:     "non_existent_binary_xyz_123",
		TimeoutSecs: 5,
	}

	result, err := runner.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if result.ExitCode != -1 {
		t.Fatalf("expected exit code -1 for missing binary, got %d", result.ExitCode)
	}
	if result.Error == "" {
		t.Fatalf("expected non-empty Error string for missing binary")
	}
}

func TestProcessRunner_DefaultWorkDir(t *testing.T) {
	runner := sandbox.NewProcessRunner("")
	ctx := context.Background()

	req := domain.ToolExecutionRequest{
		WorkflowID:  "wf-test-default-dir",
		StepNumber:  1,
		ToolName:    "pwd_tool",
		Command:     "pwd",
		TimeoutSecs: 5,
	}

	defer os.RemoveAll("scratch")

	result, err := runner.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}

	if !strings.Contains(result.Stdout, "scratch") {
		t.Fatalf("expected working directory to contain 'scratch', got: %s", result.Stdout)
	}
}

func TestDockerRunner(t *testing.T) {
	dockerRunnerDefault := sandbox.NewDockerRunner("")
	dockerRunnerCustom := sandbox.NewDockerRunner("alpine:3.19")

	if dockerRunnerDefault == nil || dockerRunnerCustom == nil {
		t.Fatalf("expected non-nil DockerRunner instances")
	}

	ctx := context.Background()
	req := domain.ToolExecutionRequest{
		WorkflowID:  "wf-test-docker",
		StepNumber:  1,
		ToolName:    "echo_tool",
		Command:     "echo",
		Args:        []string{"docker", "fallback", "test"},
		TimeoutSecs: 5,
	}
	defer os.RemoveAll("scratch")

	result, err := dockerRunnerDefault.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error executing via DockerRunner: %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if strings.TrimSpace(result.Stdout) != "docker fallback test" {
		t.Fatalf("expected stdout 'docker fallback test', got '%s'", result.Stdout)
	}
}

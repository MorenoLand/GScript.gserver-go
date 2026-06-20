package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type gs2CompileResult struct {
	bytecode    []byte
	errText     string
	warningText string
}

func (s *Server) compileGS2ForFeedback(scriptType, scriptName, script string) gs2CompileResult {
	if s == nil || s.settings == nil {
		return gs2CompileResult{}
	}
	src, ok := clientsideGS2(script)
	if !ok {
		return gs2CompileResult{}
	}
	compilerPath := strings.TrimSpace(s.settings.Get("gs2compiler"))
	if compilerPath == "" {
		return gs2CompileResult{warningText: "gs2compiler is not configured; saved without compile feedback"}
	}

	tmpRoot := filepath.Join(".", ".gs2tmp")
	if s.config != nil {
		tmpRoot = s.config.ResolvePath(".gs2tmp")
	}
	if err := os.MkdirAll(tmpRoot, 0700); err != nil {
		return gs2CompileResult{errText: err.Error()}
	}
	tmpDir, err := os.MkdirTemp(tmpRoot, "compile-*")
	if err != nil {
		return gs2CompileResult{errText: err.Error()}
	}
	defer os.RemoveAll(tmpDir)

	baseName := safeCompilerFileName(scriptName)
	inputPath := filepath.Join(tmpDir, baseName+".gs2")
	outputPath := filepath.Join(tmpDir, baseName+".gs2bc")
	if err := os.WriteFile(inputPath, []byte(src), 0600); err != nil {
		return gs2CompileResult{errText: err.Error()}
	}

	args := splitCompilerArgs(s.settings.Get("gs2compilerargs"))
	args = append(args, inputPath, outputPath)
	cmd := exec.Command(compilerPath, args...)
	cmd.Env = append(os.Environ(), "GS2_SCRIPT_TYPE="+scriptType, "GS2_SCRIPT_NAME="+scriptName)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	if err := runCompilerWithTimeout(cmd, 10*time.Second); err != nil {
		msg := strings.TrimSpace(combined.String())
		if msg == "" {
			msg = err.Error()
		}
		return gs2CompileResult{errText: msg}
	}

	bytecode, err := os.ReadFile(outputPath)
	if err != nil {
		msg := strings.TrimSpace(combined.String())
		if msg != "" {
			return gs2CompileResult{errText: msg}
		}
		return gs2CompileResult{errText: fmt.Sprintf("compiler did not write bytecode: %v", err)}
	}
	return gs2CompileResult{bytecode: bytecode}
}

func safeCompilerFileName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "script"
	}
	return b.String()
}

func clientsideGS2(script string) (string, bool) {
	const marker = "//#CLIENTSIDE"
	idx := strings.Index(strings.ToUpper(script), marker)
	if idx < 0 {
		return "", false
	}
	return strings.TrimSpace(script[idx+len(marker):]), true
}

func splitCompilerArgs(args string) []string {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func runCompilerWithTimeout(cmd *exec.Cmd, timeout time.Duration) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		_ = cmd.Process.Kill()
		<-done
		return fmt.Errorf("compiler timed out")
	}
}

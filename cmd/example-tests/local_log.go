package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const localOTEBaseDir = "OCP_CI/OTE/local"

func localArtifactsLogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return filepath.Join(home, localOTEBaseDir, "artifacts", "logs"), nil
}

func setupLocalRunTestLogging() (func(), error) {
	logDir, err := localArtifactsLogDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory %s: %w", logDir, err)
	}

	logPath := filepath.Join(logDir, "build-log.txt")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file %s: %w", logPath, err)
	}

	origStdout := os.Stdout
	origStderr := os.Stderr

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		logFile.Close()
		return nil, err
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		logFile.Close()
		return nil, err
	}

	os.Stdout = stdoutW
	os.Stderr = stderrW

	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		_, _ = io.Copy(io.MultiWriter(origStdout, logFile), stdoutR)
	}()
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(io.MultiWriter(origStderr, logFile), stderrR)
	}()

	return func() {
		_ = stdoutW.Close()
		_ = stderrW.Close()
		<-stdoutDone
		<-stderrDone
		_ = logFile.Close()
		os.Stdout = origStdout
		os.Stderr = origStderr
	}, nil
}

func wrapRunTestWithLocalLogging(cmd *cobra.Command) *cobra.Command {
	origRunE := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		cleanup, err := setupLocalRunTestLogging()
		if err != nil {
			return err
		}
		defer cleanup()
		return origRunE(c, args)
	}
	return cmd
}

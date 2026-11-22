//go:build !windows

package core

import "os/exec"

func configureProcess(cmd *exec.Cmd)               {}
func containProcess(cmd *exec.Cmd) (func(), error) { return func() {}, nil }

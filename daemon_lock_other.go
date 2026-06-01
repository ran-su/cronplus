//go:build !darwin && !linux

package main

import "fmt"

type daemonLock struct{}

func acquireDaemonLock(path string, port int) (*daemonLock, error) {
	return nil, fmt.Errorf("single-daemon lock is not supported on this platform")
}

func (l *daemonLock) Release() {}

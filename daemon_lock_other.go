//go:build !darwin && !linux

package main

import "fmt"

type daemonLock struct{}

func acquireDaemonLock(path string, port int) (*daemonLock, error) {
	return nil, fmt.Errorf("single-daemon lock is not supported on this platform")
}

func daemonLockHeld(path string) (bool, error) {
	return false, nil
}

func (l *daemonLock) Release() {}

package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func readDaemonLockPort(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok || strings.TrimSpace(key) != "port" {
			continue
		}
		port, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || port <= 0 {
			return 0, fmt.Errorf("invalid daemon lock port: %q", value)
		}
		return port, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("daemon lock has no port")
}

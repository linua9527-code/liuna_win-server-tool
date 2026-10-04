//go:build !windows

package main

import (
	"bufio"
	"errors"
	"io"
)

func configureConsole() {}
func readSecret(_ *bufio.Reader, _ io.Writer, _ string) (string, error) {
	return "", errors.New("此程序仅支持 Windows")
}
func clearRDPCredentials() (int, error) {
	return 0, errors.New("此程序仅支持 Windows")
}

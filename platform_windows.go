//go:build windows

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	getConsoleMode = kernel32.NewProc("GetConsoleMode")
	setConsoleMode = kernel32.NewProc("SetConsoleMode")
	advapi32       = syscall.NewLazyDLL("advapi32.dll")
	credEnumerate  = advapi32.NewProc("CredEnumerateW")
	credDelete     = advapi32.NewProc("CredDeleteW")
	credFree       = advapi32.NewProc("CredFree")
)

func readSecret(reader *bufio.Reader, output io.Writer, prompt string) (string, error) {
	fmt.Fprint(output, prompt)
	var mode uint32
	handle := os.Stdin.Fd()
	if ok, _, err := getConsoleMode.Call(handle, uintptr(unsafe.Pointer(&mode))); ok == 0 {
		return "", fmt.Errorf("密码需要在交互式控制台输入：%w", err)
	}
	if ok, _, err := setConsoleMode.Call(handle, uintptr(mode&^0x0004)); ok == 0 {
		return "", fmt.Errorf("关闭密码回显：%w", err)
	}
	defer func() { setConsoleMode.Call(handle, uintptr(mode)); fmt.Fprintln(output) }()
	value, err := reader.ReadString('\n')
	return strings.TrimRight(value, "\r\n"), err
}

type credential struct {
	Flags          uint32
	Type           uint32
	TargetName     *uint16
	Comment        *uint16
	LastWritten    syscall.Filetime
	BlobSize       uint32
	Blob           *byte
	Persist        uint32
	AttributeCount uint32
	Attributes     unsafe.Pointer
	TargetAlias    *uint16
	UserName       *uint16
}

/** @brief 只删除当前用户凭据库中的 TERMSRV 凭据，保留其他业务凭据。 */
func clearRDPCredentials() (int, error) {
	filter, err := syscall.UTF16PtrFromString("TERMSRV/*")
	if err != nil {
		return 0, err
	}
	var count uint32
	var entries **credential
	ok, _, callErr := credEnumerate.Call(uintptr(unsafe.Pointer(filter)), 0, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&entries)))
	if ok == 0 {
		if errors.Is(callErr, syscall.Errno(1168)) {
			return 0, nil
		}
		return 0, fmt.Errorf("读取远程凭据：%w", callErr)
	}
	defer credFree.Call(uintptr(unsafe.Pointer(entries)))
	if count > 100000 {
		return 0, errors.New("凭据数量异常")
	}
	deleted := 0
	var failures []error
	for _, item := range unsafe.Slice(entries, int(count)) {
		if ok, _, err := credDelete.Call(uintptr(unsafe.Pointer(item.TargetName)), uintptr(item.Type), 0); ok == 0 {
			failures = append(failures, fmt.Errorf("删除远程凭据：%w", err))
		} else {
			deleted++
		}
	}
	return deleted, errors.Join(failures...)
}

func configureConsole() {
	kernel32.NewProc("SetConsoleOutputCP").Call(65001)
	kernel32.NewProc("SetConsoleCP").Call(65001)
}

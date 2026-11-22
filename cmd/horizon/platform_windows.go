package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

func openBrowser(url string) error {
	shell := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
	verb, _ := syscall.UTF16PtrFromString("open")
	target, err := syscall.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	result, _, _ := shell.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(target)), 0, 0, 1)
	if result <= 32 {
		return fmt.Errorf("ShellExecuteW returned %d", result)
	}
	return nil
}
func showError(message string) {
	caption, _ := syscall.UTF16PtrFromString("Horizon Server Manager")
	body, _ := syscall.UTF16PtrFromString(message)
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(caption)), 0x10)
}
func lockInstance(dir string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(dir, "host.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	overlap := &syscall.Overlapped{}
	lock := syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	ok, _, _ := lock.Call(file.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(overlap)))
	if ok == 0 {
		file.Close()
		return nil, errors.New("Horizon is already running. Use the existing browser tab and Settings → Shut down local host before relaunching")
	}
	return func() {
		syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx").Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(overlap)))
		file.Close()
	}, nil
}

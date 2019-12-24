// +build windows

package main

import "os/signal"
import "syscall"

func SetupSignalHandlers() {
	signal.Ignore(syscall.SIGINT)
}

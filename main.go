package main

import (
	"fmt"
	"github.com/nbedos/citop/tui"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	signal.Ignore(syscall.SIGINT)
	// FIXME Do not ignore SIGTSTP/SIGCONT
	signal.Ignore(syscall.SIGTSTP)

	if err := tui.RunWidgetApp(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

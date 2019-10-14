package main

import (
	"fmt"
	"github.com/nbedos/citop/tui"
	"os"
)

func main() {
	if err := tui.RunWidgetApp(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

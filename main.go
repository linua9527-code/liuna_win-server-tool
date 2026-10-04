package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
)

const defaultConfigName = "config.json"

func main() {
	configureConsole()
	if runtime.GOOS != "windows" {
		fmt.Fprintln(os.Stderr, "此程序仅支持 Windows")
		os.Exit(1)
	}
	app := newApplication(os.Stdin, os.Stdout)
	if err := app.dispatch(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

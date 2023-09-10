package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"

	actions "github.com/sethvargo/go-githubactions"
	"golang.org/x/sys/unix"
)

const projectName = "Go Size Tracker"

func usage() {
	fmt.Fprintf(os.Stderr, `
A Github Action that lets you know how your code changes affect binary size

	go-size-tracker [flags]

%s accepts the following flags:

`[1:], projectName)
	flag.PrintDefaults()
	fmt.Fprint(os.Stderr, `

For more information, see https://github.com/capnspacehook/go-size-tracker.
`[1:])
}

func main() {
	os.Exit(mainRetCode())
}

func mainRetCode() int {
	var (
		debugLogs    bool
		printVersion bool
	)

	flag.Usage = usage
	flag.BoolVar(&debugLogs, "debug", false, "enable debug logging")
	flag.BoolVar(&printVersion, "version", false, "print version and build information and exit")
	flag.Parse()

	info, ok := debug.ReadBuildInfo()
	if !ok {
		fmt.Fprintln(os.Stderr, "build information not found")
		return 1
	}

	if printVersion {
		printVersionInfo(info)
		return 0
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, unix.SIGTERM)
	defer cancel()

	action := actions.New()
	if err := mainErr(ctx, action); err != nil {
		var exitCode *errJustExit
		if errors.As(err, &exitCode) {
			return int(*exitCode)
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type errJustExit int

func (e errJustExit) Error() string { return fmt.Sprintf("exit: %d", e) }

// this disables a linter warning because nil is unconditionally returned
// here, remove this when adding your own code that can return errors
//
//nolint:unparam
func mainErr(ctx context.Context, action *actions.Action) error {
	// START MAIN LOGIC HERE

	<-ctx.Done()
	action.Infof("shutting down")

	// STOP MAIN LOGIC HERE

	return nil
}

package main

import (
	"context"
	"encoding/gob"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"time"

	"github.com/google/shlex"
	actions "github.com/sethvargo/go-githubactions"
	"golang.org/x/sys/unix"
)

const (
	projectName   = "Go Size Tracker"
	iso8601Layout = "2006-01-02T15:04:05-0700"
)

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
	var printVersion bool

	flag.Usage = usage
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
		action.Errorf("%v", err)
		return 1
	}
	return 0
}

type errJustExit int

func (e errJustExit) Error() string { return fmt.Sprintf("exit: %d", e) }

func mainErr(ctx context.Context, action *actions.Action) error {
	buildCmd := action.GetInput("build-command")
	if buildCmd == "" {
		return errors.New("required input 'build-command' is unset")
	}
	buildArgs, err := shlex.Split(buildCmd)
	if err != nil {
		return fmt.Errorf("parsing build command: %w", err)
	}
	if len(buildArgs) == 0 {
		return errors.New("parsed build command is empty")
	}

	ghCtx, err := action.Context()
	if err != nil {
		return fmt.Errorf("getting github context: %w", err)
	}
	if ghCtx.RefType == "tag" {
		action.Infof("triggered by a tag, exiting")
		return nil
	}

	var addRecord bool
	var compare bool
	switch ghCtx.EventName {
	case "push":
		addRecord = true
	case "pull_request":
		compare = true
	default:
		action.Infof("triggered by event name %s, exiting", ghCtx.EventName)
		return nil
	}

	err = runSilentCmd(ctx, action, buildArgs[0], buildArgs[1:]...)
	if err != nil {
		return fmt.Errorf("running build command: %w", err)
	}

	fi, err := os.Stat("out")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("expected output file 'out' does not exist")
		}
		return fmt.Errorf("error reading output file: %w", err)
	}
	if !fi.Mode().Type().IsRegular() {
		return fmt.Errorf("output file is a %s file not a regular file", fi.Mode().Type())
	}

	err = runSilentCmd(ctx, action, "git", "fetch", "origin", "+refs/notes/go-size-tracker:refs/notes/go-size-tracker")
	if err != nil {
		return fmt.Errorf("fetching git notes: %w", err)
	}

	if addRecord {
	} else if compare {
	}

	return nil
}

func runCmd(ctx context.Context, action *actions.Action, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	action.Debugf("running command: %s", cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("running command %s:\n%s\n%w", cmd, string(out), err)
	}
	return out, nil
}

func runSilentCmd(ctx context.Context, action *actions.Action, name string, args ...string) error {
	_, err := runCmd(ctx, action, name, args...)
	return err
}

type sizeRecord struct {
	commit string
	date   time.Time
	size   uint32
}

func addSizeRecord(ctx context.Context, action *actions.Action, ghCtx *actions.GitHubContext, size int64) error {
	date, err := runCmd(ctx, action, "git", "log", "--format=format:%cI", "n=1")
	if err != nil {
		return fmt.Errorf("getting commit time: %w", err)
	}
	commitDate, err := time.Parse(iso8601Layout, string(date))
	if err != nil {
		return fmt.Errorf("parsing commit time: %w", err)
	}

	recordFile, err := os.CreateTemp("", "*")
	if err != nil {
		return fmt.Errorf("creating record file: %w", err)
	}
	defer recordFile.Close()

	record := sizeRecord{
		commit: ghCtx.SHA,
		date:   commitDate,
		size:   uint32(size),
	}
	enc := gob.NewEncoder(recordFile)
	if err := enc.Encode(&record); err != nil {
		return fmt.Errorf("encoding size record: %w", err)
	}
	if err := recordFile.Close(); err != nil {
		return fmt.Errorf("closing record file: %w", err)
	}

	recordBlob, err := runCmd(ctx, action, "git", "hash-object", "-w", recordFile.Name())
	if err != nil {
		return fmt.Errorf("creating git blob of record file: %w", err)
	}
	err = runSilentCmd(ctx, action, "git", "notes", "ref=refs/notes/go-size-tracker", "add", "-C", string(recordBlob), "-f")
	if err != nil {
		return fmt.Errorf("creating git note of size record: %w", err)
	}
	err = runSilentCmd(ctx, action, "git", "push", "origin", "refs/notes/go-size-tracker")
	if err != nil {
		return fmt.Errorf("pushing git note: %w", err)
	}

	return nil
}

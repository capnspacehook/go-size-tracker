package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/google/go-github/v78/github"
	"github.com/google/shlex"
	actions "github.com/sethvargo/go-githubactions"
	"github.com/wcharczuk/go-chart/v2"
	"github.com/wcharczuk/go-chart/v2/drawing"
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

	ghToken := os.Getenv("GITHUB_TOKEN")
	if ghToken == "" {
		return errors.New("environmental variable GITHUB_TOKEN is unset")
	}

	ghCtx, err := action.Context()
	if err != nil {
		return fmt.Errorf("getting github context: %w", err)
	}
	if ghCtx.RefType == "tag" {
		action.Infof("triggered by a tag, exiting")
		return nil
	}

	ghCli := github.NewClient(nil).WithAuthToken(ghToken)

	addRecord := true
	switch ghCtx.EventName {
	case "push":
		owner, repo := ghCtx.Repo()
		prs, _, err := ghCli.PullRequests.List(context.Background(), owner, repo, &github.PullRequestListOptions{
			State: "open",
		})
		if err != nil {
			return fmt.Errorf("getting pull requests: %w", err)
		}

		for _, pr := range prs {
			if pr.Head == nil {
				continue
			}
			if pr.Head.GetRef() == ghCtx.HeadRef {
				addRecord = false
				break
			}
		}
	case "pull_request":
		addRecord = false
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

	err = runSilentCmd(ctx, action, "git", "config", "--global", "--add", "safe.directory", "/github/workspace")
	if err != nil {
		return fmt.Errorf("setting git safe directory: %w", err)
	}

	err = runSilentCmd(ctx, action, "git", "fetch", "origin", "+refs/notes/go-size-tracker:refs/notes/go-size-tracker")
	if err != nil {
		return fmt.Errorf("fetching git notes: %w", err)
	}

	record, err := createRecord(ctx, action, ghCtx, fi.Size())
	if err != nil {
		return fmt.Errorf("creating size record: %w", err)
	}

	if addRecord {
		err := addSize(ctx, action, record)
		if err != nil {
			return fmt.Errorf("adding size record: %w", err)
		}
		return nil
	}

	err = compareSizes(ctx, action, record)
	if err != nil {
		return fmt.Errorf("comparing size records: %w", err)
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

func createRecord(ctx context.Context, action *actions.Action, ghCtx *actions.GitHubContext, size int64) (*sizeRecord, error) {
	date, err := runCmd(ctx, action, "git", "log", "--format=format:%cI", "n=1")
	if err != nil {
		return nil, fmt.Errorf("getting commit time: %w", err)
	}
	commitDate, err := time.Parse(iso8601Layout, string(date))
	if err != nil {
		return nil, fmt.Errorf("parsing commit time: %w", err)
	}

	return &sizeRecord{
		Commit: ghCtx.SHA,
		Date:   commitDate,
		Size:   uint32(size),
	}, nil
}

type sizeRecord struct {
	Commit string
	Date   time.Time
	Size   uint32
}

func addSize(ctx context.Context, action *actions.Action, record *sizeRecord) error {
	enc, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encoding size record: %w", err)
	}

	err = runSilentCmd(ctx, action, "git", "notes", "ref=refs/notes/go-size-tracker", "add", "-m", string(enc), "-f")
	if err != nil {
		return fmt.Errorf("creating git note of size record: %w", err)
	}
	err = runSilentCmd(ctx, action, "git", "push", "origin", "refs/notes/go-size-tracker")
	if err != nil {
		return fmt.Errorf("pushing git note: %w", err)
	}

	return nil
}

func compareSizes(ctx context.Context, action *actions.Action, record *sizeRecord) error {
	notes, err := runCmd(ctx, action, "git", "notes", "ref=refs/notes/go-size-tracker", "list")
	if err != nil {
		return fmt.Errorf("listing git notes: %w", err)
	}
	newlines := bytes.Count(notes, []byte("\n"))
	if newlines == 0 {
		action.Infof("no size records to compare against")
		return nil
	}

	records := make([]sizeRecord, newlines)
	r := bufio.NewScanner(bytes.NewReader(notes))
	for i := 0; r.Scan(); i++ {
		line := r.Text()
		_, commit, ok := strings.Cut(line, " ")
		if !ok {
			return fmt.Errorf("malformed git notes output line: %q", line)
		}
		note, err := runCmd(ctx, action, "git", "notes", "ref=refs/notes/go-size-tracker", "show", commit)
		if err != nil {
			return fmt.Errorf("getting git note of commit: %w", err)
		}

		err = json.Unmarshal(trimNewline(note), &records[i])
		if err != nil {
			return fmt.Errorf("decoding size record: %w", err)
		}
	}

	action.Infof("binary size: %s (%d bytes)", humanize.Bytes(uint64(record.Size)), record.Size)
	action.Infof("previous binary size: %s (%d bytes)", humanize.Bytes(uint64(records[0].Size)), records[0].Size)

	times := make([]time.Time, 0, len(records)+1)
	sizes := make([]float64, 0, len(records)+1)
	for _, record := range records {
		times = append(times, record.Date)
		sizes = append(sizes, float64(record.Size))
	}
	times = append(times, record.Date)
	sizes = append(sizes, float64(record.Size))

	sizeSeries := chart.TimeSeries{
		Name:    "Binary Sizes",
		XValues: times,
		YValues: sizes,
	}
	smaSeries := chart.SMASeries{
		Name: "Binary Sizes - SMA",
		Style: chart.Style{
			StrokeColor:     drawing.ColorRed,
			StrokeDashArray: []float64{5.0, 5.0},
		},
		InnerSeries: sizeSeries,
	}

	graph := chart.Chart{
		XAxis: chart.XAxis{
			Name:           "Commit Time",
			ValueFormatter: chart.TimeDateValueFormatter,
			TickPosition:   chart.TickPositionBetweenTicks,
		},
		YAxis: chart.YAxis{
			Name: "Binary Sizes",
			ValueFormatter: func(v any) string {
				mb := uint64(v.(float64))

				return fmt.Sprintf("%s (%d B)", humanize.IBytes(mb), mb)
			},
		},
		Series: []chart.Series{
			sizeSeries,
			smaSeries,
		},
	}

	graphFile, err := os.Create("graph.png")
	if err != nil {
		panic(fmt.Errorf("creating graph file: %w", err))
	}
	defer graphFile.Close()
	err = graph.Render(chart.PNG, graphFile)
	if err != nil {
		panic(fmt.Errorf("rendering graph: %w", err))
	}

	return nil
}

func trimNewline(b []byte) []byte {
	if len(b) != 0 && b[len(b)-1] == '\n' {
		return b[:len(b)-1]
	}
	return b
}

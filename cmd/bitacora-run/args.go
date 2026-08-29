package main

import (
	"fmt"
	"time"

	"github.com/bitacora-dev/bitacora/internal/job"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// parseArgs parses:
//
//	bitacora-run --job NAME [--trigger cron|systemd-timer|manual]
//	             [--timeout DURATION] [--socket PATH] [--spool-dir DIR] -- CMD [ARGS...]
//
// Everything after "--" is passed through to CMD untouched, flags and all —
// bitacora-run must never interpret the wrapped command's own arguments.
func parseArgs(argv []string) (Options, error) {
	opts := Options{
		GracePeriod: DefaultGracePeriod,
		SocketPath:  job.DefaultSocketPath,
		SpoolDir:    DefaultSpoolDir,
		HostIDPath:  schema.DefaultHostIDPath,
	}

	i := 0
	sawSeparator := false
	for i < len(argv) {
		arg := argv[i]
		if arg == "--" {
			i++
			sawSeparator = true
			break
		}

		var err error
		i, err = parseFlag(argv, i, &opts)
		if err != nil {
			return Options{}, err
		}
	}

	if opts.JobName == "" {
		return Options{}, fmt.Errorf("--job is required")
	}
	if !sawSeparator || i >= len(argv) {
		return Options{}, fmt.Errorf("missing command: expected `-- CMD [ARGS...]`")
	}

	opts.Command = argv[i]
	opts.Args = argv[i+1:]
	return opts, nil
}

func parseFlag(argv []string, i int, opts *Options) (next int, err error) {
	arg := argv[i]

	value := func() (string, error) {
		if i+1 >= len(argv) {
			return "", fmt.Errorf("%s requires a value", arg)
		}
		return argv[i+1], nil
	}

	switch arg {
	case "--job":
		v, err := value()
		if err != nil {
			return 0, err
		}
		opts.JobName = v
	case "--trigger":
		v, err := value()
		if err != nil {
			return 0, err
		}
		opts.Trigger = v
	case "--timeout":
		v, err := value()
		if err != nil {
			return 0, err
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			return 0, fmt.Errorf("--timeout: %w", err)
		}
		opts.Timeout = d
	case "--socket":
		v, err := value()
		if err != nil {
			return 0, err
		}
		opts.SocketPath = v
	case "--spool-dir":
		v, err := value()
		if err != nil {
			return 0, err
		}
		opts.SpoolDir = v
	default:
		return 0, fmt.Errorf("unknown flag %q", arg)
	}
	return i + 2, nil
}

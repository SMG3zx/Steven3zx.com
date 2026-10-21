// Command janus-agent is the Janus Agent: it runs on an Owner's machine, joins
// the Janus tailnet, and serves the Deployments the control plane hands it. It
// is a composition root like cmd/janus: the only place that knows which
// adapters the Agent uses.
//
//	janus-agent enroll -server https://janus.example -folder <dir> [-name <name>] <code>
//	janus-agent run
//	janus-agent pause
//	janus-agent resume
//	janus-agent status
//
// Every command takes -state <dir>, where the Agent keeps its settings, its
// tailnet node and its Deployments. It defaults to janus-agent under the user's
// configuration directory.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

const usage = `usage: janus-agent <command> [flags]

commands:
  enroll -server <url> -folder <dir> [-name <name>] <code>
         join Janus with an Enrollment Code from the web UI
  run    serve Deployments until stopped
  pause  stop serving until resume (the running Agent follows within a second)
  resume serve again
  status show whether the Agent is paused and what it serves

every command takes -state <dir>`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "janus-agent:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	command, args := args[0], args[1:]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(out)
	state := flags.String("state", defaultStateDir(), "directory for the Agent's settings, node and Deployments")
	switch command {
	case "enroll":
		server := flags.String("server", "", "the Janus control plane URL")
		folder := flags.String("folder", "", "the one host folder Deployments may use")
		name := flags.String("name", hostname(), "a name for this Agent")
		if err := flags.Parse(args); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return errors.New("enroll needs exactly one Enrollment Code")
		}
		return enroll(ctx, out, *state, *server, *folder, *name, flags.Arg(0))
	case "run":
		if err := flags.Parse(args); err != nil {
			return err
		}
		return serve(ctx, *state)
	case "pause", "resume":
		if err := flags.Parse(args); err != nil {
			return err
		}
		return setPaused(ctx, out, *state, command == "pause")
	case "status":
		if err := flags.Parse(args); err != nil {
			return err
		}
		return status(ctx, out, *state)
	}
	return fmt.Errorf("unknown command %q\n%s", command, usage)
}

func defaultStateDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "janus-agent"
	}
	return filepath.Join(dir, "janus-agent")
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "agent"
	}
	return name
}

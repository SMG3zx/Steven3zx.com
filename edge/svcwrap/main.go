// Command svcwrap runs one program as a Windows service and restarts it when it
// exits. Traefik has no service mode on Windows, so install.ps1 registers
// svcwrap with the Traefik command line after the -- separator.
//
//	svcwrap -name steven3zx-traefik -dir C:\steven3zx\edge -env traefik.env -log traefik.log -- traefik.exe --configFile=...
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
)

func main() {
	name := flag.String("name", "steven3zx-traefik", "Windows service name")
	dir := flag.String("dir", "", "working directory of the program (default: its own folder)")
	envFile := flag.String("env", "", "KEY=VALUE file added to the program's environment")
	logPath := flag.String("log", "", "file that receives the program's output and this wrapper's log")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("usage: svcwrap [flags] -- program [args...]")
	}

	var out *os.File = os.Stderr
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		out = f
		log.SetOutput(f)
	}

	r := &runner{
		exe:          args[0],
		args:         args[1:],
		dir:          *dir,
		out:          out,
		respawnDelay: defaultRespawnDelay,
	}
	if r.dir == "" {
		r.dir = filepath.Dir(r.exe)
	}
	if *envFile != "" {
		env, err := loadEnvFile(*envFile)
		if err != nil {
			log.Fatal(err)
		}
		r.env = env
	}

	if ran, err := runIfService(*name, r.run); ran {
		if err != nil {
			log.Fatal(err)
		}
		return
	}

	// Started from a console: run until Ctrl+C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := r.run(ctx); err != nil {
		log.Fatal(err)
	}
}

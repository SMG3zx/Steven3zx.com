package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// serviceNames are the parts of Janus that `janus serve` can run, in start
// order: the audit service, the services that keep records, the fleet service,
// and the gateway that answers HTTP.
var serviceNames = []string{"audit", "identity", "projects", "builds", "runs", "fleet", "gateway"}

// serveUsage is printed for a `janus serve` that names no known service.
var serveUsage = `usage: janus [serve all | serve <service>...]

serve all        runs every service and the gateway in this process, with an
                 embedded NATS server unless JANUS_NATS_URL names one. This is
                 also what plain "janus" does.
serve <service>  runs the named services against the NATS server in
                 JANUS_NATS_URL, one process each or several together.

services: ` + strings.Join(serviceNames, ", ")

// parseServe turns the arguments after `serve` into the services to run. "all"
// is every service. A name given twice is run once.
func parseServe(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, errors.New(serveUsage)
	}
	if slices.Contains(args, "all") {
		if len(args) > 1 {
			return nil, fmt.Errorf("serve all runs every service; do not name others with it\n\n%s", serveUsage)
		}
		return slices.Clone(serviceNames), nil
	}
	wanted := map[string]bool{}
	for _, name := range args {
		if !slices.Contains(serviceNames, name) {
			return nil, fmt.Errorf("unknown service %q\n\n%s", name, serveUsage)
		}
		wanted[name] = true
	}
	var services []string
	for _, name := range serviceNames {
		if wanted[name] {
			services = append(services, name)
		}
	}
	return services, nil
}

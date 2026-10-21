package dockerbuild

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"janus/internal/app"
)

// Each check command runs the Language's checks in its builder image and exits
// 0 itself, printing the status of the checks as its last line of stdout. So a
// failing check is a result, and a container that could not run is an error.
const (
	rustCheckCommand = "cp -a /src/. /workspace/ && cd /workspace && cargo test --locked --offline >&2 2>&1; echo $?"
	goCheckCommand   = "cp -a /src/. /workspace/ && cd /workspace && (GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go vet ./... && GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test ./...) >&2 2>&1; echo $?"
)

// rustTestsRan matches the summary cargo prints for a test binary that ran at
// least one passing test.
var rustTestsRan = regexp.MustCompile(`test result: ok\. [1-9][0-9]* passed`)

// checkSpec is how one Language is checked: its command and how its output
// shows that no test ran.
type checkSpec struct {
	command string
	noTests func(log string) bool
}

var checkSpecs = map[string]checkSpec{
	"rust": {command: rustCheckCommand, noTests: func(log string) bool { return !rustTestsRan.MatchString(log) }},
	"go":   {command: goCheckCommand, noTests: func(log string) bool { return !strings.Contains("\n"+log, "\nok ") }},
}

var _ app.Checker = (*Docker)(nil)

// Check runs the check and test commands of the workspace in the Language's
// builder image, with the same isolation as a build.
func (d *Docker) Check(ctx context.Context, language string, request app.CheckRequest) (app.CheckResult, error) {
	key := strings.ToLower(language)
	a, ok := d.adapters[key]
	spec, hasChecks := checkSpecs[key]
	if !ok || !hasChecks {
		return app.CheckResult{}, errors.New("no WASM checks for " + language)
	}
	if !validImage.MatchString(a.image) {
		return app.CheckResult{}, errors.New("configure a trusted, pre-provisioned WASM builder image")
	}
	ran := d.execute(ctx, Run{Image: a.image, Workspace: request.Workspace, Command: spec.command, MaxLog: MaxLog, MaxOutput: 64 << 10})
	if ran.Err != nil {
		return app.CheckResult{Log: ran.Stderr}, ran.Err
	}
	status, err := strconv.Atoi(strings.TrimSpace(string(ran.Stdout)))
	if err != nil {
		return app.CheckResult{Log: ran.Stderr}, errors.New("the checks did not report their status")
	}
	passed := status == 0
	return app.CheckResult{Passed: passed, NoTests: passed && spec.noTests(string(ran.Stderr)), Log: ran.Stderr}, nil
}

// Command cgiapp is a small Deployment for the Agent live test, built with
// GOOS=wasip1 GOARCH=wasm. It answers CGI requests: "/" greets, "/read" and
// "/write" use a file in the host folder, "/escape" tries to leave it, and
// "/spin" never ends.
package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
)

func main() {
	query, _ := url.ParseQuery(os.Getenv("QUERY_STRING"))
	switch os.Getenv("PATH_INFO") {
	case "/":
		respond(200, "hello from %s %s", os.Getenv("REQUEST_METHOD"), os.Getenv("QUERY_STRING"))
	case "/read":
		data, err := os.ReadFile(query.Get("name"))
		if err != nil {
			respond(404, "cannot read: %v", err)
			return
		}
		respond(200, "%s", data)
	case "/write":
		body, _ := io.ReadAll(os.Stdin)
		if err := os.WriteFile(query.Get("name"), body, 0o644); err != nil {
			respond(500, "cannot write: %v", err)
			return
		}
		respond(201, "wrote %d bytes", len(body))
	case "/escape":
		for _, name := range []string{"../outside.txt", "/../outside.txt", query.Get("absolute")} {
			if data, err := os.ReadFile(name); err == nil {
				respond(200, "escaped through %q: %s", name, data)
				return
			}
		}
		respond(403, "every path outside the folder was refused")
	case "/spin":
		for n := 0; ; n++ {
			if n < 0 {
				respond(200, "unreachable")
			}
		}
	default:
		respond(404, "no such page")
	}
}

func respond(status int, format string, args ...any) {
	fmt.Printf("Status: %d\r\nContent-Type: text/plain\r\n\r\n", status)
	fmt.Printf(format, args...)
}

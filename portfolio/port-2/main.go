// Sources
// Serving Static Files:https://lets-go.alexedwards.net/sample/02.09-serving-static-files.html

package main

import (
	"log"
	"net/http"
)

func home(w http.ResponseWriter, req *http.Request) {
	println("Serving the home -> index.html")
}

func main() {
	mux := http.NewServeMux()

	// Create a file server which serves out of the "./assets/static" directory.
	// Note that the path given to the http.Dir function is relative to the project
	// directory root.
	fileServer := http.FileServer(http.Dir("./assets/static"))

	// Use the mux.Handle() function to register the file server as the handler for
	// all URL paths that start with "/static/". For matching paths, we strip the
	// "/static" prefix before the request reaches the file server.
	mux.Handle("GET /static/", http.StripPrefix("/static", fileServer))

	// Handle serving the root page
	// GET  Prefix: Restricts the route exclusively to HTTP GET (and implicitly HEAD) requests.
	mux.HandleFunc("GET /{$}", home)

	println("Serving on localhost:8090")
	err := http.ListenAndServe(":8090", mux)
	log.Fatal(err)
}

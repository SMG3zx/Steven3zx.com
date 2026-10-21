package main

// The manifest_windows_*.syso files give the Windows build an application
// manifest. tsnet reads the Windows version through an API that reports an old
// version to a program without one, and then panics.
//
//go:generate go run github.com/tc-hib/go-winres@v0.3.3 simply --arch amd64,arm64 --manifest cli --product-name Janus --file-description "Janus control plane" --out manifest

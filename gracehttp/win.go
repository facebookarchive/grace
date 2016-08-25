// +build windows

package gracehttp

import (
	"net/http"
)

// Serve is a fallback wrapper around the standard Serve method
// on Windows. It is used instead of function gracehttp.Serve as the latter
// uses *nix specific code.
func Serve(servers ...*http.Server) error {
	return serveFallback(servers)
}

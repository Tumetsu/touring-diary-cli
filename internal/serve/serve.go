// Package serve is a tiny local HTTP server for previewing a built site.
// file:// URLs break fetch() of trip.json, so previews need HTTP.
package serve

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
)

// Handler serves dir with caching disabled so rebuilt files show up on reload.
func Handler(dir string) http.Handler {
	fsrv := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fsrv.ServeHTTP(w, r)
	})
}

// Run serves dir on addr until the listener fails. It prints the URL to out
// once listening.
func Run(dir, addr string, out io.Writer) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "serving %s at http://%s/ (Ctrl+C to stop)\n", dir, ln.Addr())
	return http.Serve(ln, Handler(dir))
}

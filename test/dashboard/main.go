// Small test server that starts the gossm dashboard on a given port
// for Playwright testing.
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/bovinemagnet/gossm/internal/config"
	"github.com/bovinemagnet/gossm/internal/session"
	"github.com/bovinemagnet/gossm/internal/web"
)

func main() {
	port := "8877"
	if len(os.Args) > 1 {
		port = os.Args[1]
	}

	sm := session.New(nil, nil)
	defer sm.Close()

	cfg := &config.Config{
		DashboardPort: 8877,
		LogLevel:      "warn",
		PIDDir:        os.TempDir(),
	}

	srv := web.NewServer(sm, cfg, time.Now(), nil)
	defer srv.Stop()

	addr := fmt.Sprintf(":%s", port)
	fmt.Printf("Test dashboard running on http://localhost%s\n", addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

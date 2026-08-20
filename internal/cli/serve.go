package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bernardosimoes/pdm/internal/boot"
	"github.com/bernardosimoes/pdm/internal/server"
)

// runServe starts the persistent localhost HTTP daemon. The engine state is
// built once here instead of once per pdm invocation — the whole point of the
// daemon (see internal/server).
func runServe(opts options, stdout, stderr io.Writer) int {
	listen := strings.TrimSpace(opts.listen)
	if listen == "" {
		listen = strings.TrimSpace(os.Getenv("PDM_LISTEN"))
	}
	if listen == "" {
		listen = "127.0.0.1:8787"
	}

	truthRaw := os.Getenv("PDM_TRUTH_API")
	if opts.truthAPISet {
		truthRaw = opts.truthAPI
	}
	truthAPI, err := boot.ValidateTruthAPI(truthRaw)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	srv, err := server.New(server.Config{
		CacheDir: opts.cacheDir,
		TruthAPI: truthAPI,
		Version:  Version,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: NDJSON streams legitimately run up to the engine's
		// 150s budget; each request is bounded by its own context instead.
	}

	// The supervisor (web/server.js) stops the daemon with SIGTERM; finish
	// in-flight streams before exiting.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	fmt.Fprintf(stdout, "pdm serve listening on http://%s\n", listen)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}
	return 0
}

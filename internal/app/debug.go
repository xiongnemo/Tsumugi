package app

import (
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"
	"sync"
)

var pprofOnce sync.Once

func startPprofIfEnabled() {
	addr := strings.TrimSpace(os.Getenv("TSUMUGI_PPROF_ADDR"))
	if addr == "" {
		return
	}
	pprofOnce.Do(func() {
		go func() {
			fmt.Fprintf(os.Stderr, "tsumugi: pprof listening on %s\n", addr)
			if err := http.ListenAndServe(addr, nil); err != nil {
				fmt.Fprintf(os.Stderr, "tsumugi: pprof stopped: %v\n", err)
			}
		}()
	})
}

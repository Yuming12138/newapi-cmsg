// Build on the development host, then run in the New API container so that
// public-data collection uses the real provider, network and existing Redis.
// Never print connection strings, inspect users, or bypass API authentication.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
)

type probeTransport struct {
	base      http.RoundTripper
	noNetwork bool
	requests  atomic.Int32
}

func (transport *probeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.requests.Add(1)
	if transport.noNetwork {
		return nil, errors.New("probe forbids upstream access; no fresh persisted snapshot was restored")
	}
	return transport.base.RoundTrip(request)
}

func main() {
	noNetwork := flag.Bool("no-network", false, "require a fresh persisted snapshot without contacting the upstream")
	flag.Parse()
	if err := common.InitRedisClient(); err != nil {
		fmt.Fprintln(os.Stderr, "probe could not initialize the existing Redis connection")
		os.Exit(1)
	}
	if !common.RedisEnabled || common.RDB == nil {
		fmt.Fprintln(os.Stderr, "probe requires the existing application Redis configuration")
		os.Exit(1)
	}
	defer common.RDB.Close()
	transport := &probeTransport{base: http.DefaultTransport, noNetwork: *noNetwork}
	http.DefaultTransport = transport
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	overview, err := service.GetCodexRadarOverview(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe status=error upstream_requests=%d error=%q\n", transport.requests.Load(), err.Error())
		os.Exit(1)
	}
	families := make(map[string]int)
	for _, metric := range overview.Metrics {
		families[metric.Family]++
	}
	fmt.Printf("probe status=ok elapsed_ms=%d points=%d families=%v schema=%s benchmark=%s fetched=%s stale=%t upstream_requests=%d\n",
		time.Since(start).Milliseconds(), len(overview.Metrics), families, overview.SchemaVersion, overview.Benchmark, overview.FetchedAt, overview.Stale, transport.requests.Load())
}

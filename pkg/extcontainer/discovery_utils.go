package extcontainer

import (
	"context"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	extension_kit "github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/exthttp"
	"github.com/steadybit/extension-kit/extutil"
	"net/http"
	"sync"
	"time"
)

type fetchDataFn[T any] func(ctx context.Context) (T, error)

func schedule[T any](ctx context.Context, interval time.Duration, fetchData fetchDataFn[T]) fetchDataFn[T] {
	results := &scheduledResult[T]{}

	go func() {
		results.store(fetchData(ctx))

		for {
			select {
			case <-time.After(interval):
				results.store(fetchData(ctx))
			case <-ctx.Done():
				return
			}
		}
	}()

	return func(_ context.Context) (T, error) {
		return results.get()
	}
}

func discoveryHandler(discovery fetchDataFn[[]discovery_kit_api.Target]) func(w http.ResponseWriter, r *http.Request, body []byte) {
	return func(w http.ResponseWriter, r *http.Request, body []byte) {
		targets, err := discovery(r.Context())
		if err != nil {
			exthttp.WriteError(w, extension_kit.ToError("Discovery failed.", err))
			return
		}
		exthttp.WriteBody(w, discovery_kit_api.DiscoveryData{Targets: extutil.Ptr(targets)})
	}
}

type scheduledResult[T any] struct {
	mu   sync.RWMutex
	data T
	err  error
}

func (r *scheduledResult[T]) store(data T, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.data = data
	r.err = err
}

func (r *scheduledResult[T]) get() (data T, err error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.data, r.err
}

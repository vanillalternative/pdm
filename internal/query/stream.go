package query

import (
	"encoding/json"
	"io"
	"sync"

	"github.com/bernardosimoes/pdm/internal/mapview"
)

// emitter writes NDJSON events: one compact JSON object per line, mutex-guarded
// because layer events are emitted from concurrent evaluation goroutines.
type emitter struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func newEmitter(w io.Writer) *emitter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &emitter{enc: enc}
}

func (e *emitter) emit(v any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.enc.Encode(v) // Encode appends the newline
}

// StreamNDJSON runs a query in NDJSON streaming mode over w: the engine emits
// meta and per-layer events as they complete, the locator map is generated
// concurrently (buildMap may be nil when run emits its own map event), and the
// complete result is the terminal event. On failure the terminal event is an
// error event and the error is also returned. This function is the single
// implementation of the streaming contract shared by the CLI and `pdm serve`:
// exactly one meta event first, one terminal event (result or error) last.
func StreamNDJSON(w io.Writer, run func(Emit) (any, error), buildMap func() *mapview.Data) error {
	em := newEmitter(w)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if buildMap == nil {
			return
		}
		if m := buildMap(); m != nil {
			if svg := m.SVG(); svg != "" {
				em.emit(MapEvent{Event: "map", HTML: svg})
			}
		}
	}()
	res, err := run(em.emit)
	wg.Wait() // the terminal event must be last
	if err != nil {
		em.emit(ErrorEvent{Event: "error", Error: err.Error()})
		return err
	}
	em.emit(ResultEvent{Event: "result", Data: res})
	return nil
}

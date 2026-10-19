//go:build cgo

package wasmruntime

import (
	"container/list"
	"crypto/sha256"
	"sync"
	"sync/atomic"

	wasmtime "github.com/bytecodealliance/wasmtime-go/v49"
)

// maxCachedModuleBytes bounds the memory the compiled-module cache may hold. A
// compiled module is about three to four times the size of its .wasm file.
const maxCachedModuleBytes = 256 << 20

// compiledModules holds Wasmtime's compiled form of recently run modules, so a
// module is compiled once instead of on every run. Compiling a 1 to 2 MB module
// takes a quarter to a third of a second; loading the compiled form takes about
// a millisecond.
//
// Entries are keyed by the SHA-256 of the .wasm bytes, so two Artifacts with the
// same module share one entry and a changed module never matches a stale one.
// Only bytes this process produced with Module.Serialize are ever loaded, which
// is the one case Wasmtime allows.
type compiledModules struct {
	mu      sync.Mutex
	max     int64
	size    int64
	order   *list.List // front is the most recently used
	entries map[[sha256.Size]byte]*list.Element

	hits, misses atomic.Int64
}

type compiledEntry struct {
	key  [sha256.Size]byte
	data []byte
}

func newCompiledModules(max int64) *compiledModules {
	return &compiledModules{max: max, order: list.New(), entries: map[[sha256.Size]byte]*list.Element{}}
}

var moduleCache = newCompiledModules(maxCachedModuleBytes)

func (c *compiledModules) get(key [sha256.Size]byte) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(element)
	return element.Value.(*compiledEntry).data, true
}

func (c *compiledModules) put(key [sha256.Size]byte, data []byte) {
	if int64(len(data)) > c.max {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		c.order.MoveToFront(element)
		return
	}
	c.entries[key] = c.order.PushFront(&compiledEntry{key: key, data: data})
	c.size += int64(len(data))
	for c.size > c.max {
		oldest := c.order.Back()
		entry := oldest.Value.(*compiledEntry)
		c.order.Remove(oldest)
		delete(c.entries, entry.key)
		c.size -= int64(len(entry.data))
	}
}

func (c *compiledModules) drop(key [sha256.Size]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		c.size -= int64(len(element.Value.(*compiledEntry).data))
		c.order.Remove(element)
		delete(c.entries, key)
	}
}

// loadModule returns the module for wasm in engine, from the cache when it holds
// a compiled copy and by compiling it otherwise. The engine must be configured
// like every other engine this package creates, because a compiled module only
// loads into an engine with the same settings. If a cached copy does not load,
// it is dropped and the module is compiled again.
func loadModule(engine *wasmtime.Engine, wasm []byte) (*wasmtime.Module, error) {
	key := sha256.Sum256(wasm)
	if compiled, ok := moduleCache.get(key); ok {
		if module, err := wasmtime.NewModuleDeserialize(engine, compiled); err == nil {
			moduleCache.hits.Add(1)
			return module, nil
		}
		moduleCache.drop(key)
	}
	moduleCache.misses.Add(1)
	module, err := wasmtime.NewModule(engine, wasm)
	if err != nil {
		return nil, err
	}
	if compiled, err := module.Serialize(); err == nil {
		moduleCache.put(key, compiled)
	}
	return module, nil
}

//go:build cgo

package wasmtime

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

// freshCache swaps in an empty cache for the test, so counts do not depend on
// which modules other tests already ran.
func freshCache(t *testing.T) {
	t.Helper()
	saved := moduleCache
	moduleCache = newCompiledModules(maxCachedModuleBytes)
	t.Cleanup(func() { moduleCache = saved })
}

func TestSecondRunOfAModuleLoadsItFromTheCache(t *testing.T) {
	freshCache(t)
	module := compileWAT(t, strings.Replace(countLoop, "1000", "1111", 1))
	hits, misses := moduleCache.hits.Load(), moduleCache.misses.Load()

	_, _, first, err := ExecuteWASI(module, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := moduleCache.misses.Load() - misses; got != 1 {
		t.Fatalf("misses after the first run = %d, want 1", got)
	}
	_, _, second, err := ExecuteWASI(module, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := moduleCache.hits.Load() - hits; got != 1 {
		t.Fatalf("hits after the second run = %d, want 1", got)
	}
	if first.Fuel != second.Fuel {
		t.Fatalf("a cached module spent %d fuel, an uncached one %d: the cache changed the run", second.Fuel, first.Fuel)
	}
}

func TestDifferentModulesDoNotShareACacheEntry(t *testing.T) {
	small := compileWAT(t, countLoop)
	other := compileWAT(t, `(module
  (func (export "_start")
    (local $i i32)
    (loop $again
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $again (i32.lt_u (local.get $i) (i32.const 5000))))))`)
	_, _, a, err := ExecuteWASI(small, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, _, b, err := ExecuteWASI(other, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if a.Fuel == b.Fuel {
		t.Fatalf("both modules spent %d fuel: the second ran the first one's cached code", a.Fuel)
	}
}

func TestACachedEntryThatWillNotLoadIsReplaced(t *testing.T) {
	freshCache(t)
	module := compileWAT(t, strings.Replace(countLoop, "1000", "2222", 1))
	key := sha256.Sum256(module)
	moduleCache.put(key, []byte("not a compiled module"))

	if _, _, _, err := ExecuteWASI(module, nil, 5*time.Second); err != nil {
		t.Fatalf("a bad cache entry failed the run: %v", err)
	}
	misses := moduleCache.misses.Load()
	if _, _, _, err := ExecuteWASI(module, nil, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if moduleCache.misses.Load() != misses {
		t.Fatal("the bad entry was not replaced by a good one")
	}
}

func TestCacheEvictsTheLeastRecentlyUsedEntryOverItsLimit(t *testing.T) {
	cache := newCompiledModules(10)
	a, b, c := sha256.Sum256([]byte("a")), sha256.Sum256([]byte("b")), sha256.Sum256([]byte("c"))
	cache.put(a, make([]byte, 4))
	cache.put(b, make([]byte, 4))
	if _, ok := cache.get(a); !ok { // a is now newer than b
		t.Fatal("a missing")
	}
	cache.put(c, make([]byte, 4)) // 12 bytes: b goes
	if _, ok := cache.get(b); ok {
		t.Fatal("b should have been evicted")
	}
	if _, ok := cache.get(a); !ok {
		t.Fatal("a was evicted though it was used more recently than b")
	}
	if _, ok := cache.get(c); !ok {
		t.Fatal("c missing")
	}
	if cache.size != 8 {
		t.Fatalf("size = %d, want 8", cache.size)
	}
}

func TestCacheIgnoresAnEntryLargerThanItsLimit(t *testing.T) {
	cache := newCompiledModules(10)
	key := sha256.Sum256([]byte("big"))
	cache.put(key, make([]byte, 11))
	if _, ok := cache.get(key); ok || cache.size != 0 {
		t.Fatalf("an oversize entry was kept (size %d)", cache.size)
	}
}

package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"mneme/store"
)

func testSequences(t *testing.T, s store.Store) {
	ctx := context.Background()
	for want := int64(1); want <= 3; want++ {
		if got := must[int64](t)(s.Next(ctx, "e", "seq", "")); got != want {
			t.Fatalf("Next: want %d, got %d", want, got)
		}
	}
	if got := must[int64](t)(s.Next(ctx, "e", "other", "")); got != 1 {
		t.Fatalf("independent name: want 1, got %d", got)
	}
	if got := must[int64](t)(s.Next(ctx, "e", "seq", "2026")); got != 1 {
		t.Fatalf("independent scope: want 1, got %d", got)
	}
	if got := must[int64](t)(s.Next(ctx, "f", "seq", "")); got != 1 {
		t.Fatalf("independent record type: want 1, got %d", got)
	}
	const workers, each = 8, 25
	seen := sync.Map{}
	wg := sync.WaitGroup{}
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				n, err := s.Next(ctx, "conc", "seq", "")
				if err != nil {
					errCh <- err
					return
				}
				if _, dup := seen.LoadOrStore(n, true); dup {
					errCh <- fmt.Errorf("duplicate sequence value %d", n)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	for n := int64(1); n <= workers*each; n++ {
		if _, ok := seen.Load(n); !ok {
			t.Fatalf("concurrent Next skipped %d", n)
		}
	}
}

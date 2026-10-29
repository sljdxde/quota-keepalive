package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestConcurrentReadWriteIsSafe(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	const n = 20
	var wg sync.WaitGroup
	errCh := make(chan error, n*2)
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			if _, err := s.HealthCheck(context.Background()); err != nil {
				errCh <- err
			}
		}()

		go func() {
			defer wg.Done()
			<-start
			_, err := s.db.ExecContext(context.Background(),
				`INSERT INTO logs(ts, target, message) VALUES(?, ?, ?)`,
				time.Now().UTC().Format(time.RFC3339Nano), "t", "x")
			if err != nil {
				errCh <- err
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("并发读写出错: %v", err)
	}
}

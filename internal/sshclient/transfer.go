package sshclient

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// 传输的分块:ChunkThreshold 以下的文件不值得开多路;每个 worker
// 一块一块地取,写完报进度。
const (
	// ChunkThreshold is the size a file must reach for a copy to go in
	// concurrent pieces; the file panel's local side splits at the same
	// size.
	ChunkThreshold = 8 << 20
	chunkThreshold = ChunkThreshold // the same size, for tests in this package
	chunkSize      = 4 << 20        // 每块 4 MB
	chunkWorkers   = 4              // 同时飞的块数
)

// SendChunked copies total bytes from src to dst in chunks, several in
// the air at once: each worker takes the next offset, reads it out of
// src and writes it to dst at the same offset, so a slow write never
// stalls the reads behind it. report hears the running total as chunks
// land; nil reports nothing.
//
// src and dst must take reads and writes at an offset — *os.File and
// *sftp.File both do — and the caller opens and closes them. report runs
// on the workers but one call at a time, so it may touch shared state
// without a lock of its own.
func SendChunked(src io.ReaderAt, dst io.WriterAt, total int64, report func(done, total int64)) error {
	if total <= 0 {
		return nil
	}
	workers := chunkWorkers
	if total < int64(workers)*chunkSize {
		workers = 2 // 中型文件两路就够
	}
	var done atomic.Int64
	var mu sync.Mutex // firstErr, and one report at a time
	var firstErr error
	var wg sync.WaitGroup
	work := make(chan int64, workers*2)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, chunkSize)
			for off := range work {
				n := int64(chunkSize)
				if off+n > total {
					n = total - off
				}
				// ReadAt/WriteAt 要么整段要么报错:写失败即停,不重试
				// 也不跳过,洞留给调用方按错误处理。
				if _, err := src.ReadAt(buf[:n], off); err != nil {
					fail(&mu, &firstErr, fmt.Errorf("read at %d: %w", off, err))
					continue
				}
				if _, err := dst.WriteAt(buf[:n], off); err != nil {
					fail(&mu, &firstErr, fmt.Errorf("write at %d: %w", off, err))
					continue
				}
				if report != nil {
					d := done.Add(n)
					mu.Lock()
					report(d, total)
					mu.Unlock()
				}
			}
		}()
	}
feed:
	for off := int64(0); off < total; off += chunkSize {
		mu.Lock()
		broken := firstErr != nil
		mu.Unlock()
		if broken {
			break feed
		}
		work <- off
	}
	close(work)
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	return firstErr
}

// fail records the first chunk that went wrong, of however many do.
func fail(mu *sync.Mutex, first *error, err error) {
	mu.Lock()
	if *first == nil {
		*first = err
	}
	mu.Unlock()
}

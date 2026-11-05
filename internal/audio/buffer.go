// Package audio implements the low-latency audio engine for Chordly:
// hardware output, Opus/AAC decoding, and asynchronous stream buffering.
package audio

import (
	"errors"
	"io"
	"sync"
)

// DefaultBufferCapacity is the pre-buffer size used when a caller does not
// specify one: ~1.3s of 48kHz stereo 16-bit PCM is ~384KB per second, so
// 5MB holds roughly 13 seconds of decoded audio. Plan callers typically
// buffer compressed (Opus) bytes, where 5MB holds far more audio.
const DefaultBufferCapacity = 5 << 20 // 5 MiB

// ErrClosed is returned by Read/Write after Close has been called.
var ErrClosed = errors.New("audio: ring buffer closed")

// RingReader is a thread-safe asynchronous ring buffer decoupling a slow or
// jittery chunk producer (e.g. an HTTP audio stream) from a real-time
// consumer (e.g. the sound device or a decoder).
//
// Usage: a background pump goroutine started by Fill reads from the source
// and stores chunks into the ring, while callers consume PCM with Read.
// Put/Get expose the underlying ring directly for callers that already hold
// chunks (e.g. a demuxer emitting PCM frames).
//
// The zero value is invalid; use NewRingReader.
type RingReader struct {
	buf  []byte
	size int
	cap  int

	mu       sync.Mutex
	notEmpty *sync.Cond // signalled when Size grows or on Close
	notFull  *sync.Cond // signalled when Size shrinks or on Close

	head   int  // index of oldest unread byte
	tail   int  // index where the next byte will be written
	closed bool // Close called; no further writes accepted
	eof    bool // source EOF reached; Read returns io.EOF once drained
	readErr error // first non-EOF source error; returned after drain
}

// NewRingReader returns a RingReader with the given capacity in bytes. A
// non-positive capacity falls back to DefaultBufferCapacity.
func NewRingReader(capacity int) *RingReader {
	if capacity <= 0 {
		capacity = DefaultBufferCapacity
	}
	r := &RingReader{
		buf: make([]byte, capacity),
		cap: capacity,
	}
	r.notEmpty = sync.NewCond(&r.mu)
	r.notFull = sync.NewCond(&r.mu)
	return r
}

// Len returns the number of bytes currently buffered.
func (r *RingReader) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size
}

// Cap returns the total ring capacity in bytes.
func (r *RingReader) Cap() int { return r.cap }

// Available returns the number of bytes that can currently be written
// without blocking.
func (r *RingReader) Available() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cap - r.size
}

// Put copies p into the ring, blocking while the ring is full. It returns
// the number of bytes written and ErrClosed if the ring is closed.
func (r *RingReader) Put(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	written := 0
	for written < len(p) {
		if r.closed {
			return written, ErrClosed
		}
		if r.size == r.cap {
			r.notFull.Wait()
			continue
		}
		n := r.copyInLocked(p[written:])
		written += n
		r.notEmpty.Signal()
	}
	return written, nil
}

// Get copies up to len(p) bytes out of the ring, blocking while empty and
// not at EOF. It returns io.EOF once the ring is drained after the source
// reached EOF.
func (r *RingReader) Get(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for r.size == 0 {
		if r.closed {
			return 0, io.EOF
		}
		if r.eof {
			if r.readErr != nil {
				return 0, r.readErr
			}
			return 0, io.EOF
		}
		r.notEmpty.Wait()
	}
	n := r.copyOutLocked(p)
	r.notFull.Signal()
	return n, nil
}

// Read implements io.Reader, serving buffered bytes to the consumer.
func (r *RingReader) Read(p []byte) (int, error) { return r.Get(p) }

// Write implements io.Writer, storing p into the ring. It is the same as
// Put and blocks while the ring is full.
func (r *RingReader) Write(p []byte) (int, error) { return r.Put(p) }

// copyInLocked copies min(len(p), free) bytes into the tail. Caller holds mu.
func (r *RingReader) copyInLocked(p []byte) int {
	n := min(len(p), r.cap-r.size)
	if n == 0 {
		return 0
	}
	// First write up to the end of the backing array, then wrap.
	first := min(n, r.cap-r.tail)
	copy(r.buf[r.tail:r.tail+first], p[:first])
	if rest := n - first; rest > 0 {
		copy(r.buf[:rest], p[first:first+rest])
	}
	r.tail = (r.tail + n) % r.cap
	r.size += n
	return n
}

// copyOutLocked copies min(len(p), size) bytes out from the head. Caller holds mu.
func (r *RingReader) copyOutLocked(p []byte) int {
	n := min(len(p), r.size)
	if n == 0 {
		return 0
	}
	first := min(n, r.cap-r.head)
	copy(p[:first], r.buf[r.head:r.head+first])
	if rest := n - first; rest > 0 {
		copy(p[first:first+rest], r.buf[:rest])
	}
	r.head = (r.head + n) % r.cap
	r.size -= n
	return n
}

// Fill starts a background pump goroutine that copies from src into the ring
// in chunk-sized reads. Fill returns immediately; the pump runs until src
// reaches EOF (or returns a non-EOF read error) or the ring is Closed.
// Decoded PCM consumers should call io.CopyBuffer or io.Copy on the
// RingReader as the reader and let the writer side run Fill.
//
// The source is not closed by Fill; ownership stays with the caller.
func (r *RingReader) Fill(src io.Reader) {
	go r.pump(src)
}

// FillFromSize is a convenience used by the engine: it pumps src with a
// 32KiB stack chunk buffer. See Fill.
func (r *RingReader) FillFromSize(src io.Reader, chunk int) {
	if chunk <= 0 {
		chunk = 32 << 10
	}
	go r.pumpChunked(src, chunk)
}

// pump copies src into the ring until EOF or Close.
func (r *RingReader) pump(src io.Reader) { r.pumpChunked(src, 32<<10) }

func (r *RingReader) pumpChunked(src io.Reader, chunk int) {
	buf := make([]byte, chunk)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := r.Put(buf[:n]); werr == ErrClosed {
				return
			} else if werr != nil {
				r.setReadErr(werr)
				return
			}
		}
		switch err {
		case nil:
		case io.EOF:
			r.setEOF()
			return
		default:
			r.setReadErr(err)
			return
		}
	}
}

// Drain blocks until the ring is empty (all bytes consumed). It returns
// immediately if the ring already has zero buffered bytes.
func (r *RingReader) Drain() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for r.size > 0 {
		r.notFull.Wait()
	}
}

// Reset discards all buffered bytes and clears EOF/error state, preparing the
// ring for a new stream. It does not cancel an in-flight Fill goroutine;
// call Close first if a pump may still be holding a reference.
func (r *RingReader) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.head, r.tail, r.size = 0, 0, 0
	r.eof, r.readErr = false, nil
	r.closed = false
	r.notEmpty.Broadcast()
	r.notFull.Broadcast()
}

// Close marks the ring closed: pending/future Writes fail with ErrClosed,
// pending Reads return io.EOF once the ring is drained. Close is idempotent
// and unblocks both producers and consumers.
func (r *RingReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		r.notEmpty.Broadcast()
		r.notFull.Broadcast()
	}
	return nil
}

// Closed reports whether Close has been called.
func (r *RingReader) Closed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *RingReader) setEOF() {
	r.mu.Lock()
	r.eof = true
	r.mu.Unlock()
	r.notEmpty.Broadcast()
}

func (r *RingReader) setReadErr(err error) {
	r.mu.Lock()
	if r.readErr == nil {
		r.readErr = err
	}
	r.eof = true
	r.mu.Unlock()
	r.notEmpty.Broadcast()
}

// StreamBuffer is the name used by plan-1 §2.2 for the same concept. It is an
// alias for RingReader so both names refer to one implementation.
type StreamBuffer = RingReader

package audio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"testing"
	"time"
)

// slowReader returns at most maxPerRead bytes per Read call to simulate a
// jittery network chunk producer.
type slowReader struct {
	data       []byte
	pos        int
	maxPerRead int
}

func (s *slowReader) Read(p []byte) (int, error) {
	if s.pos >= len(s.data) {
		return 0, io.EOF
	}
	n := min(len(p), s.maxPerRead, len(s.data)-s.pos)
	copy(p[:n], s.data[s.pos:s.pos+n])
	s.pos += n
	return n, nil
}

func TestPutGetRoundTrip(t *testing.T) {
	r := NewRingReader(64)
	defer r.Close()

	chunk := []byte("hello ring buffer")
	if n, err := r.Put(chunk); err != nil || n != len(chunk) {
		t.Fatalf("Put = (%d, %v), want (%d, nil)", n, err, len(chunk))
	}
	if got := r.Len(); got != len(chunk) {
		t.Fatalf("Len = %d, want %d", got, len(chunk))
	}

	out := make([]byte, len(chunk))
	if n, err := r.Get(out); err != nil || n != len(chunk) {
		t.Fatalf("Get = (%d, %v), want (%d, nil)", n, err, len(chunk))
	}
	if !bytes.Equal(out, chunk) {
		t.Fatalf("Get data = %q, want %q", out, chunk)
	}
	if got := r.Len(); got != 0 {
		t.Fatalf("Len after Get = %d, want 0", got)
	}
}

func TestPutGetWrapsAround(t *testing.T) {
	r := NewRingReader(16)
	defer r.Close()

	// Fill to capacity, drain two bytes, then write across the array boundary.
	for i := 0; i < 16; i++ {
		if _, err := r.Put([]byte{byte(i)}); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	two := make([]byte, 2)
	if _, err := r.Get(two); err != nil || two[0] != 0 || two[1] != 1 {
		t.Fatalf("Get first two = (%v, %v, %v)", two[0], two[1], err)
	}
	if _, err := r.Put([]byte{16, 17}); err != nil {
		t.Fatalf("wrap Put: %v", err)
	}

	got := make([]byte, 16)
	if n, err := r.Get(got); err != nil || n != 16 {
		t.Fatalf("Get = (%d, %v), want 16", n, err)
	}
	want := []byte{2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17}
	if !bytes.Equal(got, want) {
		t.Fatalf("wrap order = %v, want %v", got, want)
	}
}

func TestGetBlocksWhenEmpty(t *testing.T) {
	r := NewRingReader(16)
	defer r.Close()

	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4)
		if _, err := r.Get(buf); err != nil {
			t.Errorf("Get: %v", err)
		}
		close(done)
	}()

	// Consumer must still be blocked after 30ms.
	select {
	case <-done:
		t.Fatal("Get returned while ring was empty")
	case <-time.After(30 * time.Millisecond):
	}

	if _, err := r.Put([]byte("ping")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Get did not unblock after Put")
	}
}

func TestPutBlocksWhenFull(t *testing.T) {
	r := NewRingReader(4)
	defer r.Close()

	if _, err := r.Put([]byte("full")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.Put([]byte("x"))
		done <- err
	}()

	// Producer must still be blocked after 30ms.
	select {
	case err := <-done:
		t.Fatalf("Put returned (%v) while ring was full", err)
	case <-time.After(30 * time.Millisecond):
	}

	buf := make([]byte, 1)
	if _, err := r.Get(buf); err != nil {
		t.Fatalf("Get: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unblocked Put returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Put did not unblock after Get")
	}
}

func TestGetReturnsEOFAtDrainedEOF(t *testing.T) {
	r := NewRingReader(16)
	defer r.Close()

	r.Fill(bytes.NewReader([]byte("short")))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "short" {
		t.Fatalf("ReadAll = %q, want %q", got, "short")
	}
	// After EOF and drain, further Reads report io.EOF.
	if n, err := r.Get(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Fatalf("Get after EOF = (%d, %v), want (0, EOF)", n, err)
	}
}

func TestWriteReadViaInterfacesClosed(t *testing.T) {
	r := NewRingReader(64)

	if _, err := io.WriteString(r, "pcm-bytes"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "pcm-bytes" {
		t.Fatalf("ReadAll = %q, want %q", got, "pcm-bytes")
	}
}

func TestFillPumpsSlowSource(t *testing.T) {
	want := make([]byte, 2048)
	for i := range want {
		want[i] = byte(i % 251)
	}
	r := NewRingReader(256)
	defer r.Close()

	r.Fill(&slowReader{data: want, maxPerRead: 64})
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("pumped %d bytes, want %d identical", len(got), len(want))
	}
}

func TestCloseUnblocksBlockedReader(t *testing.T) {
	r := NewRingReader(4)

	readDone := make(chan error, 1)
	go func() {
		_, err := r.Read(make([]byte, 1))
		readDone <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-readDone:
		if err != io.EOF {
			t.Fatalf("blocked Read returned %v, want EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock blocked Read")
	}
	// Second Close is a no-op success.
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestCloseUnblocksBlockedWriter(t *testing.T) {
	r := NewRingReader(4)

	// Fill the buffer.
	if _, err := r.Put([]byte("full")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := r.Put([]byte("z"))
		writeDone <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-writeDone:
		if err != ErrClosed {
			t.Fatalf("blocked Put returned %v, want ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock blocked Put")
	}
	// Writes after close fail with ErrClosed.
	if _, err := r.Put([]byte("x")); err != ErrClosed {
		t.Fatalf("Put after Close = %v, want ErrClosed", err)
	}
}

func TestResetClearsState(t *testing.T) {
	r := NewRingReader(16)
	defer r.Close()

	if _, err := r.Put([]byte("stale")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	r.Reset()
	if got := r.Len(); got != 0 {
		t.Fatalf("Len after Reset = %d, want 0", got)
	}
	if _, err := r.Put([]byte("fresh")); err != nil {
		t.Fatalf("Put after Reset: %v", err)
	}
	out := make([]byte, 5)
	if _, err := r.Get(out); err != nil || string(out) != "fresh" {
		t.Fatalf("Get = %q, %v, want %q", out, err, "fresh")
	}
}

func TestConcurrentProducersConsumers(t *testing.T) {
	const chunks = 64
	const chunkSize = 1024

	r := NewRingReader(8 << 10)
	defer r.Close()

	// Build the expected byte stream.
	want := make([]byte, 0, chunks*chunkSize)
	rng := rand.New(rand.NewSource(42))
	producers := make([][]byte, chunks)
	for i := range producers {
		producers[i] = make([]byte, chunkSize)
		_, _ = rng.Read(producers[i])
		want = append(want, producers[i]...)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, p := range producers {
			if _, err := r.Write(p); err != nil {
				t.Errorf("Write: %v", err)
				return
			}
		}
		if err := r.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	<-done
	if !bytes.Equal(got, want) {
		t.Fatalf("stream mismatch: got %d bytes, want %d", len(got), len(want))
	}
}

func TestDrainWaitsForEmpty(t *testing.T) {
	r := NewRingReader(16)
	defer r.Close()

	if _, err := r.Put([]byte("stale data")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = r.Get(make([]byte, 10))
	}()
	done := make(chan struct{})
	go func() { r.Drain(); close(done) }()

	select {
	case <-done:
		t.Fatal("Drain returned before ring emptied")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Drain did not return after ring emptied")
	}
}

func TestSourceErrorPropagatesAfterDrain(t *testing.T) {
	r := NewRingReader(16)
	defer r.Close()

	wantErr := errors.New("network reset")
	r.Fill(&errReader{data: []byte("partial"), err: wantErr})

	got, err := io.ReadAll(r)
	if string(got) != "partial" {
		t.Fatalf("ReadAll = %q, want %q", got, "partial")
	}
	if err == nil {
		t.Fatal("ReadAll returned nil error, want source error")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReadAll error = %v, want %v", err, wantErr)
	}
}

type errReader struct {
	data []byte
	err  error
	pos  int
}

func (e *errReader) Read(p []byte) (int, error) {
	if e.pos < len(e.data) {
		n := min(len(p), len(e.data)-e.pos)
		copy(p[:n], e.data[e.pos:e.pos+n])
		e.pos += n
		return n, nil
	}
	return 0, e.err
}

func ExampleRingReader() {
	r := NewRingReader(1024)
	defer r.Close()
	r.Fill(bytes.NewReader([]byte("pcm frames")))
	out, err := io.ReadAll(r)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(out))
	// Output: pcm frames
}

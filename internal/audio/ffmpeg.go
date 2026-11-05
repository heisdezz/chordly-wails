// FFmpeg-backed stream decoder.
//
// The pure-Go demuxers in decoder.go cover WebM/Opus, Ogg Opus, and WAV. Some
// YouTube Music formats are AAC/MP4, and seeking inside a container needs
// demuxer support the Go decoders do not expose. Both cases fall back to an
// ffmpeg subprocess that yields the same 48kHz stereo s16le PCM as the Go
// decoders, so the Device/CommandSink pipeline is unchanged.
package audio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	ffmpegSampleRate = SampleRate
	ffmpegChannels   = Channels
)

// FFmpegAvailable reports whether an ffmpeg binary can be located.
func FFmpegAvailable() bool {
	return findFFmpeg() != ""
}

// findFFmpeg resolves the ffmpeg binary. CHORDLY_FFMPEG overrides PATH.
func findFFmpeg() string {
	if configured := os.Getenv("CHORDLY_FFMPEG"); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured
		}
	}
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		return path
	}
	return ""
}

// FFmpegDecoder decodes any ffmpeg-supported container to 48kHz stereo s16le.
// start positions the output at a timestamp (ffmpeg decodes and discards the
// leading audio, which is reliable for non-seekable HTTP streams).
type FFmpegDecoder struct {
	cmd      *exec.Cmd
	stdout   io.ReadCloser
	stderr   lockedBuffer
	start    time.Duration
	duration time.Duration

	bytesRead atomic.Int64
	closeOnce sync.Once
	closeErr  error
}

// NewFFmpegDecoder starts ffmpeg reading the compressed stream from r.
// duration is only a hint for Duration(); pass 0 when unknown.
func NewFFmpegDecoder(r io.Reader, start, duration time.Duration) (*FFmpegDecoder, error) {
	path := findFFmpeg()
	if path == "" {
		return nil, fmt.Errorf("%w: ffmpeg not found (set CHORDLY_FFMPEG or install ffmpeg)", ErrUnsupportedFormat)
	}
	if start < 0 {
		start = 0
	}

	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-i", "pipe:0"}
	if start > 0 {
		// Output seeking: works on pipes and keeps timestamps accurate.
		args = append(args, "-ss", strconv.FormatFloat(start.Seconds(), 'f', 3, 64))
	}
	args = append(args,
		"-vn",
		"-map", "0:a:0",
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"-ar", strconv.Itoa(ffmpegSampleRate),
		"-ac", strconv.Itoa(ffmpegChannels),
		"pipe:1",
	)

	cmd := exec.Command(path, args...)
	cmd.Stdin = r

	d := &FFmpegDecoder{cmd: cmd, start: start, duration: duration}
	cmd.Stderr = &d.stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("audio/ffmpeg: create stdout pipe: %w", err)
	}
	d.stdout = stdout

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("audio/ffmpeg: start: %w: %s", err, d.stderr.String())
	}
	return d, nil
}

func (d *FFmpegDecoder) Format() DeviceConfig {
	return DeviceConfig{SampleRate: ffmpegSampleRate, Channels: ffmpegChannels, Volume: 0.8}
}

func (d *FFmpegDecoder) Duration() time.Duration { return d.duration }

func (d *FFmpegDecoder) Position() time.Duration {
	frames := d.bytesRead.Load() / int64(ffmpegChannels*BytesPerSample)
	return d.start + time.Duration(frames)*time.Second/time.Duration(ffmpegSampleRate)
}

func (d *FFmpegDecoder) Read(p []byte) (int, error) {
	n, err := d.stdout.Read(p)
	if n > 0 {
		d.bytesRead.Add(int64(n))
	}
	if err != nil && !errors.Is(err, io.EOF) {
		if detail := d.stderr.String(); detail != "" {
			return n, fmt.Errorf("audio/ffmpeg: %w: %s", err, detail)
		}
	}
	return n, err
}

func (d *FFmpegDecoder) Close() error {
	d.closeOnce.Do(func() {
		if d.stdout != nil {
			_ = d.stdout.Close()
		}
		if d.cmd != nil && d.cmd.Process != nil {
			_ = d.cmd.Process.Kill()
			// ffmpeg exits non-zero when killed; that is expected teardown.
			_ = d.cmd.Wait()
		}
	})
	return d.closeErr
}

// lockedBuffer is a concurrency-safe stderr sink: exec writes from its own
// goroutine while Read may inspect the tail for diagnostics.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(bytes.TrimSpace(l.b.Bytes()))
}

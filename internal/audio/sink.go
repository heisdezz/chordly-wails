package audio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// CommandSink writes raw PCM to an external audio player process. It avoids
// CGO audio bindings and works with the audio servers already present on most
// Linux desktops (PipeWire, PulseAudio, ALSA), with ffplay as a portable
// fallback.
type CommandSink struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr bytes.Buffer
	name   string

	mu     sync.Mutex
	closed bool
}

// OpenCommandSink starts the first available raw-PCM sink for 48kHz stereo
// signed 16-bit little-endian audio.
func OpenCommandSink() (*CommandSink, error) {
	type candidate struct {
		name string
		args []string
	}

	candidates := []candidate{
		{
			name: "pw-play",
			args: []string{"--raw", "--format=s16", "--rate=48000", "--channels=2", "-"},
		},
		{
			name: "paplay",
			args: []string{"--raw", "--format=s16le", "--rate=48000", "--channels=2"},
		},
		{
			name: "aplay",
			args: []string{"-q", "-t", "raw", "-f", "S16_LE", "-r", "48000", "-c", "2", "-"},
		},
		{
			name: "ffplay",
			args: []string{"-nodisp", "-autoexit", "-loglevel", "quiet", "-f", "s16le", "-ar", "48000", "-ac", "2", "-"},
		},
	}

	// CHORDLY_AUDIO_SINK can force a specific executable (useful in tests or
	// for users on unusual setups). It receives the candidate args for the
	// matching built-in, or the pw-play args otherwise.
	if forced := os.Getenv("CHORDLY_AUDIO_SINK"); forced != "" {
		args := candidates[0].args
		for _, c := range candidates {
			if c.name == forced {
				args = c.args
				break
			}
		}
		candidates = append([]candidate{{name: forced, args: args}}, candidates...)
	}

	var lastErr error
	for _, c := range candidates {
		path, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}

		cmd := exec.Command(path, c.args...)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			lastErr = err
			continue
		}

		sink := &CommandSink{cmd: cmd, stdin: stdin, name: c.name}
		cmd.Stderr = &sink.stderr
		if err := cmd.Start(); err != nil {
			lastErr = err
			_ = stdin.Close()
			continue
		}
		return sink, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("audio: no usable output sink: %w", lastErr)
	}
	return nil, errors.New("audio: no output sink found (install pw-play, paplay, aplay, or ffplay)")
}

// Name returns the executable backing the sink.
func (s *CommandSink) Name() string { return s.name }

// Write sends PCM bytes to the player process.
func (s *CommandSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	return s.stdin.Write(p)
}

// Close closes the PCM pipe and waits for the player to exit. It is safe to
// call multiple times.
func (s *CommandSink) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	stdin := s.stdin
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	return s.cmd.Wait()
}

// Audio output driver for Chordly: low-latency PCM device management.
//
// TSK-101 — the playback pipeline is:
//
//	decoder → RingReader → VolumeReader → Device (io.Writer consumer)
//
// The driver is interface-first: Device wraps an io.Writer sink (e.g. an
// oto/v3 player or a malgo device) so the engine can operate headless in
// tests and CI. When a real backend is available, the same Device pumps
// PCM from a *RingReader into the backend sink.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Format constants for the driver (plan-1 §2.1).
const (
	// SampleRate is the device sample rate: 48kHz.
	SampleRate = 48000
	// Channels is the device channel count: stereo.
	Channels = 2
	// BytesPerSample is the size of one int16 sample in the PCM stream.
	BytesPerSample = 2
	// FrameSize is the size of one multi-channel frame (L+R).
	FrameSize = Channels * BytesPerSample
)

var (
	// ErrInvalidSampleRate is returned for a non-positive sample rate.
	ErrInvalidSampleRate = errors.New("audio: invalid sample rate")
	// ErrInvalidChannels is returned for a non-positive channel count.
	ErrInvalidChannels = errors.New("audio: invalid channel count")
	// ErrInvalidVolume is returned when a volume level is outside [0,1].
	ErrInvalidVolume = errors.New("audio: volume out of range")
)

// DeviceConfig describes the PCM format of a Device.
type DeviceConfig struct {
	SampleRate int // default SampleRate
	Channels   int // default Channels
	Volume     float32
}

// DefaultDeviceConfig returns the plan defaults (48kHz stereo, 0.8 gain).
func DefaultDeviceConfig() DeviceConfig {
	return DeviceConfig{SampleRate: SampleRate, Channels: Channels, Volume: 0.8}
}

func (c DeviceConfig) validate() (DeviceConfig, error) {
	if c.SampleRate <= 0 {
		c.SampleRate = SampleRate
	}
	if c.Channels <= 0 {
		c.Channels = Channels
	}
	if c.Volume == 0 {
		c.Volume = 0.8
	}
	if c.Volume < 0 || c.Volume > 1 {
		return c, ErrInvalidVolume
	}
	return c, nil
}

// Format returns a human-readable format summary (e.g. "48000Hz/2ch").
func (c DeviceConfig) Format() string {
	return fmt.Sprintf("%dHz/%dch", c.SampleRate, c.Channels)
}

// FrameBytes returns the size in bytes of one audio frame across all channels.
func (c DeviceConfig) FrameBytes() int {
	return c.Channels * BytesPerSample
}

// DeviceState mirrors the engine-level playback states relevant to output.
type DeviceState int

const (
	DeviceStopped DeviceState = iota
	DevicePlaying
	DevicePaused
)

// Device is the low-latency audio output driver.
//
// Concurrency: Device is safe for concurrent Play/Pause/Volume/State calls.
// The pump goroutine started by Play reads from the attached source and
// writes PCM frames to the output writer.
type Device struct {
	mu     sync.Mutex
	config DeviceConfig

	// sink is the PCM consumer (real backend in production, recorder in
	// tests). It is set at construction.
	sink io.Writer

	source io.Reader
	srcMu  sync.Mutex

	// lifecycle
	state DeviceState

	// pump goroutine
	pumpDone chan struct{}
	stopCh   chan struct{}
	stopOnce sync.Once

	// accounting
	framesPlayed atomic.Int64 // total frames written to sink
	startedAt    time.Time
	position     time.Duration // accumulated playhead at last pause/stop
	pausedAt     time.Time
}

// NewDevice creates a Device writing PCM frames to sink. The sink must not be
// nil; in production this is wired to the oto/malgo backend:
//
//	sink := backend.NewPlayer(...)
//
// A nil sink is rejected.
func NewDevice(sink io.Writer, cfg DeviceConfig) (*Device, error) {
	if sink == nil {
		return nil, errors.New("audio: nil output writer")
	}
	validated, err := cfg.validate()
	if err != nil {
		return nil, err
	}
	return &Device{
		config: validated,
		sink:   sink,
		state:  DeviceStopped,
	}, nil
}

// NewDeviceWithReader attaches an initial PCM source so Play can start
// immediately.
func NewDeviceWithReader(sink io.Writer, src io.Reader, cfg DeviceConfig) (*Device, error) {
	if src == nil {
		return nil, errors.New("audio: nil PCM source")
	}
	d, err := NewDevice(sink, cfg)
	if err != nil {
		return nil, err
	}
	d.source = src
	return d, nil
}

// Config returns the current device configuration.
func (d *Device) Config() DeviceConfig {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.config
}

// Format returns the device format string.
func (d *Device) Format() string { return d.Config().Format() }

// State returns the current device state.
func (d *Device) State() DeviceState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

// Position returns the current playhead position.
func (d *Device) Position() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch d.state {
	case DevicePlaying:
		return d.position + time.Since(d.startedAt)
	default:
		return d.position
	}
}

// FramesPlayed reports the total number of PCM frames written to the sink.
func (d *Device) FramesPlayed() int64 {
	return d.framesPlayed.Load()
}

// Play starts (or resumes) PCM output from the current source. Play is a
// no-op when already Playing; it must be called with a source set either via
// NewDeviceWithReader or Load.
func (d *Device) Play() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state == DevicePlaying {
		return nil
	}
	if d.source == nil {
		return errors.New("audio: no PCM source loaded")
	}

	// Cleanly terminate any prior pump if present
	if d.stopCh != nil {
		d.stopOnce.Do(func() { close(d.stopCh) })
	}

	d.stopCh = make(chan struct{})
	d.pumpDone = make(chan struct{})
	d.stopOnce = sync.Once{}

	if d.state == DevicePaused {
		// Resume from pause position.
		d.startedAt = time.Now()
	} else {
		// Fresh start.
		d.startedAt = time.Now()
		d.position = 0
	}
	d.state = DevicePlaying

	stopCh := d.stopCh
	done := d.pumpDone
	src := d.source
	sink := d.sink
	frameSize := d.config.Channels * BytesPerSample
	go d.pump(src, sink, stopCh, done, frameSize, &d.framesPlayed)
	return nil
}

// Pause suspends output, preserving the playhead position.
func (d *Device) Pause() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state != DevicePlaying {
		return nil
	}
	d.position += time.Since(d.startedAt)
	d.state = DevicePaused
	if d.stopCh != nil {
		d.stopOnce.Do(func() { close(d.stopCh) })
	}
	return nil
}

// Stop halts output and resets the playhead to zero.
func (d *Device) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state == DeviceStopped {
		return nil
	}
	if d.stopCh != nil {
		d.stopOnce.Do(func() { close(d.stopCh) })
	}
	d.state = DeviceStopped
	d.position = 0
	d.framesPlayed.Store(0)
	return nil
}

// Wait blocks until the current pump goroutine exits. Call Stop and unblock the
// source/sink first; otherwise a pump blocked on Read or Write will keep
// Wait blocked. It is intended for teardown between tracks.
func (d *Device) Wait() {
	d.mu.Lock()
	done := d.pumpDone
	d.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Load replaces the current PCM source. If the device is Playing, playback
// continues from the new source starting at zero; callers should Stop first
// for a clean seek.
func (d *Device) Load(src io.Reader) error {
	if src == nil {
		return errors.New("audio: nil PCM source")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopCh != nil {
		d.stopOnce.Do(func() { close(d.stopCh) })
	}
	d.source = src
	if d.state == DevicePlaying {
		d.stopCh = make(chan struct{})
		d.pumpDone = make(chan struct{})
		d.stopOnce = sync.Once{}
		stopCh := d.stopCh
		done := d.pumpDone
		d.startedAt = time.Now()
		d.position = 0
		go d.pump(src, d.sink, stopCh, done, d.config.Channels*BytesPerSample, &d.framesPlayed)
	}
	return nil
}

// SetVolume sets the master gain (0.0–1.0).
func (d *Device) SetVolume(level float32) error {
	if level < 0 || level > 1 {
		return fmt.Errorf("%w: %v", ErrInvalidVolume, level)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.config.Volume = level
	return nil
}

// Volume returns the current master gain.
func (d *Device) Volume() float32 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.config.Volume
}

// pump copies PCM frames from src to sink until the source reaches EOF or
// stopCh is closed.
func (d *Device) pump(src io.Reader, sink io.Writer, stopCh <-chan struct{}, done chan<- struct{}, frameSize int, framesPlayed *atomic.Int64) {
	defer close(done)

	block := make([]byte, frameSize*512)
	for {
		select {
		case <-stopCh:
			return
		default:
		}
		d.srcMu.Lock()
		n, err := src.Read(block)
		d.srcMu.Unlock()
		if n > 0 {
			gain := d.Volume()
			out := applyGain(block[:n], gain)
			if _, werr := sink.Write(out); werr != nil {
				return
			}
			framesPlayed.Add(int64(len(out) / frameSize))
		}
		if err != nil {
			d.mu.Lock()
			if d.state == DevicePlaying {
				d.state = DeviceStopped
			}
			d.mu.Unlock()
			return
		}
	}
}

// applyGain scales int16 PCM samples by gain (0–1) in place and returns the
// scaled bytes. Odd trailing bytes are passed through unchanged.
func applyGain(pcm []byte, gain float32) []byte {
	if len(pcm) == 0 {
		return pcm
	}
	if gain == 1 {
		return pcm
	}
	out := make([]byte, len(pcm))
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(binary.LittleEndian.Uint16(pcm[i : i+2]))
		scaled := int32(float32(sample) * gain)
		if scaled > math.MaxInt16 {
			scaled = math.MaxInt16
		} else if scaled < math.MinInt16 {
			scaled = math.MinInt16
		}
		binary.LittleEndian.PutUint16(out[i:i+2], uint16(int16(scaled)))
	}
	// Pass through a trailing odd byte (corrupt frame) unchanged.
	if len(pcm)%2 == 1 {
		out[len(out)-1] = pcm[len(pcm)-1]
	}
	return out
}

// Seek offsets the playhead position by delta.
func (d *Device) Seek(delta time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state != DevicePlaying {
		d.position += delta
	} else {
		d.position += time.Since(d.startedAt)
		d.startedAt = time.Now()
		d.position += delta
	}
	if d.position < 0 {
		d.position = 0
	}
	return nil
}

// VolumeReader wraps an io.Reader and applies a live gain.
type VolumeReader struct {
	src  io.Reader
	gain func() float32
}

func NewVolumeReader(src io.Reader, gain func() float32) *VolumeReader {
	return &VolumeReader{src: src, gain: gain}
}

func (v *VolumeReader) Read(p []byte) (int, error) {
	n, err := v.src.Read(p)
	if n == 0 {
		return 0, err
	}
	g := float32(1)
	if v.gain != nil {
		g = v.gain()
	}
	scaled := applyGain(p[:n], g)
	copy(p[:n], scaled)
	return n, err
}

// Closer is satisfied by sinks that must be released.
type Closer interface {
	io.Writer
	Close() error
}

func closeSink(sink io.Writer) {
	if c, ok := sink.(Closer); ok {
		_ = c.Close()
	}
}

// WaitPump blocks until the pump goroutine (if any) has exited. It returns
// false on timeout.
func (d *Device) WaitPump(timeout time.Duration) bool {
	d.mu.Lock()
	done := d.pumpDone
	d.mu.Unlock()
	if done == nil {
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

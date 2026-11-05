package audio

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// recorder is a thread-safe sink that captures written PCM.
type recorder struct {
	mu   sync.Mutex
	data []byte
}

func (r *recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = append(r.data, p...)
	return len(p), nil
}

func (r *recorder) bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.data...)
}

func (r *recorder) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.data)
}

type pacedReader struct {
	r     io.Reader
	delay time.Duration
}

func (p *pacedReader) Read(b []byte) (int, error) {
	time.Sleep(p.delay)
	return p.r.Read(b)
}

type blockingReader struct {
	ch chan struct{}
}

func (b *blockingReader) Read(p []byte) (int, error) {
	<-b.ch
	return 0, io.EOF
}

func TestNewDeviceValidatesConfig(t *testing.T) {
	if _, err := NewDevice(nil, DefaultDeviceConfig()); err == nil {
		t.Error("NewDevice(nil sink) should fail")
	}
	// Zero config defaults (should succeed).
	d, err := NewDevice(&recorder{}, DeviceConfig{})
	if err != nil {
		t.Fatalf("NewDevice with zero config: %v", err)
	}
	if d.Format() != "48000Hz/2ch" {
		t.Errorf("Format = %q, want 48000Hz/2ch", d.Format())
	}
	// Volume out of range rejected.
	if _, err := NewDevice(&recorder{}, DeviceConfig{Volume: 1.5}); !errors.Is(err, ErrInvalidVolume) {
		t.Errorf("Volume 1.5 err = %v, want ErrInvalidVolume", err)
	}
}

func TestNewDeviceStoresConfig(t *testing.T) {
	d, _ := NewDevice(&recorder{}, DeviceConfig{SampleRate: 44100, Channels: 2, Volume: 0.5})
	if d.Config().SampleRate != 44100 || d.Config().Volume != 0.5 {
		t.Errorf("Config = %+v, want 44100Hz/0.5", d.Config())
	}
	if d.State() != DeviceStopped {
		t.Errorf("State = %v, want DeviceStopped", d.State())
	}
}

func TestSetVolumeValidatesRange(t *testing.T) {
	d, _ := NewDevice(&recorder{}, DefaultDeviceConfig())
	if err := d.SetVolume(-0.1); !errors.Is(err, ErrInvalidVolume) {
		t.Errorf("SetVolume(-0.1) = %v, want ErrInvalidVolume", err)
	}
	if err := d.SetVolume(1.1); !errors.Is(err, ErrInvalidVolume) {
		t.Errorf("SetVolume(1.1) = %v, want ErrInvalidVolume", err)
	}
	if err := d.SetVolume(0.5); err != nil {
		t.Errorf("SetVolume(0.5) = %v", err)
	}
	if d.Volume() != 0.5 {
		t.Errorf("Volume = %v, want 0.5", d.Volume())
	}
}

func TestApplyGainScalesPCM(t *testing.T) {
	// Two samples: 1000, -1000. Gain 0.5 → 500, -500.
	pcm := []byte{0xe8, 0x03, 0x18, 0xfc}
	out := applyGain(pcm, 0.5)
	want := []byte{0xf4, 0x01, 0x0c, 0xfe}
	if !bytes.Equal(out, want) {
		t.Errorf("applyGain = %v, want %v", out, want)
	}
	// Gain 0 → silence.
	out = applyGain(pcm, 0)
	if !bytes.Equal(out, []byte{0, 0, 0, 0}) {
		t.Errorf("applyGain 0 = %v, want zeros", out)
	}
	// Gain 1 is a pass-through (no copy, but equal bytes).
	out = applyGain(pcm, 1)
	if !bytes.Equal(out, pcm) {
		t.Errorf("applyGain 1 = %v, want identity", out)
	}
}

func TestPlayPumpsPCMToSink(t *testing.T) {
	sink := &recorder{}
	// 1 second of stereo PCM at 1000 amplitude.
	pcm := bytes.Repeat([]byte{0xe8, 0x03, 0xe8, 0x03}, SampleRate)
	d, err := NewDeviceWithReader(sink, bytes.NewReader(pcm), DeviceConfig{SampleRate: SampleRate, Channels: 2, Volume: 1})
	if err != nil {
		t.Fatalf("NewDeviceWithReader: %v", err)
	}
	if err := d.Play(); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if !d.WaitPump(2 * time.Second) {
		t.Fatal("pump did not finish")
	}
	if got := sink.bytes(); !bytes.Equal(got, pcm) {
		t.Errorf("pumped %d bytes, want %d identical PCM", len(got), len(pcm))
	}
	if d.State() != DeviceStopped {
		t.Errorf("State after EOF = %v, want DeviceStopped", d.State())
	}
}

func TestPlayWithoutSourceFails(t *testing.T) {
	d, _ := NewDevice(&recorder{}, DefaultDeviceConfig())
	if err := d.Play(); err == nil {
		t.Error("Play without source should fail")
	}
}

func TestPausePreservesPosition(t *testing.T) {
	sink := &recorder{}
	pcm := bytes.Repeat([]byte{0xe8, 0x03}, 48000*10)
	d, _ := NewDeviceWithReader(sink, &pacedReader{r: bytes.NewReader(pcm), delay: 5 * time.Millisecond}, DefaultDeviceConfig())
	if err := d.Play(); err != nil {
		t.Fatalf("Play: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := d.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if d.State() != DevicePaused {
		t.Errorf("State = %v, want DevicePaused", d.State())
	}
	pos := d.Position()
	if pos <= 0 {
		t.Errorf("Position after pause = %v, want > 0", pos)
	}
	// Position stays constant while paused.
	time.Sleep(20 * time.Millisecond)
	if d.Position() != pos {
		t.Errorf("Position drifted while paused: %v → %v", pos, d.Position())
	}
	_ = d.Stop()
	_ = d.WaitPump(time.Second)
}

func TestStopResetsPosition(t *testing.T) {
	sink := &recorder{}
	pcm := make([]byte, 48000)
	d, _ := NewDeviceWithReader(sink, bytes.NewReader(pcm), DefaultDeviceConfig())
	if err := d.Play(); err != nil {
		t.Fatalf("Play: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if d.State() != DeviceStopped {
		t.Errorf("State = %v, want DeviceStopped", d.State())
	}
	if d.Position() != 0 {
		t.Errorf("Position = %v, want 0", d.Position())
	}
	_ = d.WaitPump(time.Second)
}

func TestVolumeAppliedToOutput(t *testing.T) {
	sink := &recorder{}
	// Sample value 1000 scaled by gain 0.5 → 500.
	pcm := []byte{0xe8, 0x03, 0xe8, 0x03}
	cfg := DefaultDeviceConfig()
	cfg.Volume = 0.5
	d, _ := NewDeviceWithReader(sink, bytes.NewReader(pcm), cfg)
	_ = d.Play()
	_ = d.WaitPump(time.Second)

	want := []byte{0xf4, 0x01, 0xf4, 0x01}
	if got := sink.bytes(); !bytes.Equal(got, want) {
		t.Errorf("Volume 0.5 output = %v, want %v", got, want)
	}
}

func TestVolumeReaderScales(t *testing.T) {
	pcm := []byte{0xe8, 0x03, 0x18, 0xfc}
	vol := float32(0.5)
	vr := NewVolumeReader(bytes.NewReader(pcm), func() float32 { return vol })
	buf := make([]byte, 4)
	n, err := vr.Read(buf)
	if err != nil || n != 4 {
		t.Fatalf("Read: %d, %v", n, err)
	}
	want := []byte{0xf4, 0x01, 0x0c, 0xfe}
	if !bytes.Equal(buf, want) {
		t.Errorf("VolumeReader = %v, want %v", buf, want)
	}
}

func TestConcurrentPlayPauseVolume(t *testing.T) {
	sink := &recorder{}
	pcm := bytes.Repeat([]byte{1, 2, 3, 4}, 100000)
	d, _ := NewDeviceWithReader(sink, bytes.NewReader(pcm), DefaultDeviceConfig())

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				switch j % 3 {
				case 0:
					_ = d.Play()
				case 1:
					_ = d.Pause()
				case 2:
					_ = d.SetVolume(0.5)
				}
				_ = d.Position()
				_ = d.State()
			}
		}(i)
	}
	wg.Wait()
	_ = d.Stop()
	_ = d.WaitPump(time.Second)
}

func TestRingReaderFeedsDevice(t *testing.T) {
	pcm := bytes.Repeat([]byte{0xe8, 0x03, 0xe8, 0x03}, 1024)
	buf := NewRingReader(64 << 10)
	buf.Fill(bytes.NewReader(pcm))

	sink := &recorder{}
	cfg := DefaultDeviceConfig()
	cfg.Volume = 1.0
	d, _ := NewDeviceWithReader(sink, buf, cfg)
	if err := d.Play(); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if !d.WaitPump(3 * time.Second) {
		t.Fatal("pump did not finish")
	}
	if got := sink.bytes(); !bytes.Equal(got, pcm) {
		t.Errorf("got %d bytes, want %d", len(got), len(pcm))
	}
	_ = buf.Close()
}

func TestWaitPumpTimeout(t *testing.T) {
	src := &blockingReader{ch: make(chan struct{})}
	d, _ := NewDeviceWithReader(&recorder{}, src, DefaultDeviceConfig())
	if err := d.Play(); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if d.WaitPump(50 * time.Millisecond) {
		t.Error("WaitPump returned true for blocked pump")
	}
	_ = d.Stop()
	close(src.ch)
	_ = d.WaitPump(time.Second)
}

func TestLoadReplacesSource(t *testing.T) {
	sink := &recorder{}
	cfg := DefaultDeviceConfig()
	cfg.Volume = 1.0
	d, _ := NewDeviceWithReader(sink, bytes.NewReader(nil), cfg)
	if err := d.Play(); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if !d.WaitPump(time.Second) {
		t.Fatal("pump did not finish")
	}
	if err := d.Load(bytes.NewReader([]byte{0xe8, 0x03, 0xe8, 0x03})); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := d.Play(); err != nil {
		t.Fatalf("Play after Load: %v", err)
	}
	if !d.WaitPump(2 * time.Second) {
		t.Fatal("pump did not finish after Load")
	}
	if got := sink.bytes(); !bytes.Equal(got, []byte{0xe8, 0x03, 0xe8, 0x03}) {
		t.Errorf("got %v, want loaded PCM", got)
	}
}

func TestLoadNilFails(t *testing.T) {
	d, _ := NewDevice(&recorder{}, DefaultDeviceConfig())
	if err := d.Load(nil); err == nil {
		t.Error("Load(nil) should fail")
	}
}

func TestSeekClampsToZero(t *testing.T) {
	d, _ := NewDeviceWithReader(&recorder{}, bytes.NewReader(make([]byte, 4)), DefaultDeviceConfig())
	if err := d.Seek(-5 * time.Second); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if d.Position() != 0 {
		t.Errorf("Position = %v, want 0 (clamped)", d.Position())
	}
	if err := d.Seek(3 * time.Second); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if d.Position() < 3*time.Second {
		t.Errorf("Position = %v, want >= 3s", d.Position())
	}
}

func TestApplyGainClampsToInt16(t *testing.T) {
	pcm := []byte{0xff, 0x7f, 0x00, 0x80} // MaxInt16, MinInt16
	out := applyGain(pcm, 1.0)
	if !bytes.Equal(out, pcm) {
		t.Errorf("applyGain 1.0 did not preserve extremes: got %v", out)
	}
}

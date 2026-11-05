package audio

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
	"time"
)

// synthWAV builds a 48kHz stereo 16-bit silence WAV of the given duration.
func synthWAV(t *testing.T, d time.Duration, rate, channels int) []byte {
	t.Helper()

	samples := int(d.Seconds() * float64(rate))
	dataSize := samples * channels * BytesPerSample

	var b bytes.Buffer
	write := func(v any) {
		if err := binary.Write(&b, binary.LittleEndian, v); err != nil {
			t.Fatalf("encode wav: %v", err)
		}
	}

	b.WriteString("RIFF")
	write(uint32(36 + dataSize))
	b.WriteString("WAVEfmt ")
	write(uint32(16))
	write(uint16(1)) // PCM
	write(uint16(channels))
	write(uint32(rate))
	write(uint32(rate * channels * BytesPerSample))
	write(uint16(channels * BytesPerSample))
	write(uint16(16))
	b.WriteString("data")
	write(uint32(dataSize))
	b.Write(make([]byte, dataSize))

	return b.Bytes()
}

func TestFFmpegDecoderDecodesWAV(t *testing.T) {
	if !FFmpegAvailable() {
		t.Skip("ffmpeg not installed")
	}

	const duration = 2 * time.Second
	wav := synthWAV(t, duration, SampleRate, Channels)

	dec, err := NewFFmpegDecoder(bytes.NewReader(wav), 0, duration)
	if err != nil {
		t.Fatalf("NewFFmpegDecoder: %v", err)
	}
	defer dec.Close()

	if got := dec.Format(); got.SampleRate != SampleRate || got.Channels != Channels {
		t.Fatalf("Format = %+v, want %dHz/%dch", got, SampleRate, Channels)
	}
	if got := dec.Duration(); got != duration {
		t.Errorf("Duration = %v, want %v", got, duration)
	}

	pcm, err := io.ReadAll(dec)
	if err != nil {
		t.Fatalf("read PCM: %v", err)
	}

	want := int(duration.Seconds()) * SampleRate * Channels * BytesPerSample
	if diff := len(pcm) - want; diff < -want/10 || diff > want/10 {
		t.Fatalf("PCM length = %d, want ~%d (±10%%)", len(pcm), want)
	}
	if pos := dec.Position(); pos < duration-500*time.Millisecond {
		t.Errorf("Position = %v, want ~%v", pos, duration)
	}
}

func TestFFmpegDecoderSeeksToOffset(t *testing.T) {
	if !FFmpegAvailable() {
		t.Skip("ffmpeg not installed")
	}

	const duration = 4 * time.Second
	const offset = 3 * time.Second
	wav := synthWAV(t, duration, SampleRate, Channels)

	dec, err := NewFFmpegDecoder(bytes.NewReader(wav), offset, duration)
	if err != nil {
		t.Fatalf("NewFFmpegDecoder: %v", err)
	}
	defer dec.Close()

	pcm, err := io.ReadAll(dec)
	if err != nil {
		t.Fatalf("read PCM: %v", err)
	}

	full := int(duration.Seconds()) * SampleRate * Channels * BytesPerSample
	if len(pcm) >= full/2 {
		t.Fatalf("seeked PCM length = %d, want well under %d", len(pcm), full)
	}
	// ~1s of audio remains after seeking to 3s out of 4s.
	if pos := dec.Position(); pos < offset+500*time.Millisecond {
		t.Errorf("Position = %v, want >= %v", pos, offset)
	}
}

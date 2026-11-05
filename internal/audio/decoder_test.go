package audio

import (
	"bytes"
	"errors"
	"math"
	"encoding/binary"
	"io"
	"testing"
	"time"

	"github.com/tphakala/go-opus/opus"
)

func TestSniffContainer(t *testing.T) {
	tests := []struct {
		name     string
		header   []byte
		expected string
	}{
		{
			name:     "WebM EBML",
			header:   []byte{0x1A, 0x45, 0xDF, 0xA3, 0x01, 0x00},
			expected: "webm",
		},
		{
			name:     "Ogg Opus",
			header:   []byte("OggS\x00\x02"),
			expected: "ogg",
		},
		{
			name:     "WAV RIFF",
			header:   append(append([]byte("RIFF"), 0x24, 0x00, 0x00, 0x00), []byte("WAVE")...),
			expected: "wav",
		},
		{
			name:     "MP4 ftyp",
			header:   []byte{0x00, 0x00, 0x00, 0x20, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '},
			expected: "mp4",
		},
		{
			name:     "Unknown",
			header:   []byte{0x01, 0x02, 0x03, 0x04},
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SniffContainer(tt.header)
			if got != tt.expected {
				t.Errorf("SniffContainer = %q, want %q", got, tt.expected)
			}
		})
	}
}

// buildSyntheticWAV creates a valid 16-bit 48kHz stereo WAV container.
func buildSyntheticWAV(numFrames int) []byte {
	var buf bytes.Buffer
	dataSize := uint32(numFrames * FrameSize)
	riffSize := 36 + dataSize

	// RIFF header
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, riffSize)
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16)) // subchunk size
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))  // PCM format
	_ = binary.Write(&buf, binary.LittleEndian, uint16(2))  // stereo
	_ = binary.Write(&buf, binary.LittleEndian, uint32(SampleRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(SampleRate*FrameSize)) // byte rate
	_ = binary.Write(&buf, binary.LittleEndian, uint16(FrameSize))            // block align
	_ = binary.Write(&buf, binary.LittleEndian, uint16(16))                   // bits per sample

	// data chunk
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, dataSize)

	for i := 0; i < numFrames; i++ {
		sample := int16(i * 10)
		_ = binary.Write(&buf, binary.LittleEndian, sample) // Left
		_ = binary.Write(&buf, binary.LittleEndian, sample) // Right
	}

	return buf.Bytes()
}

func TestWAVDecoder(t *testing.T) {
	numFrames := 480 // 10ms of 48kHz audio
	wavBytes := buildSyntheticWAV(numFrames)

	dec, err := NewWAVDecoder(bytes.NewReader(wavBytes))
	if err != nil {
		t.Fatalf("NewWAVDecoder error: %v", err)
	}

	cfg := dec.Format()
	if cfg.SampleRate != SampleRate || cfg.Channels != Channels {
		t.Errorf("Format mismatch: got %v", cfg)
	}

	expectedDuration := 10 * time.Millisecond
	if dec.Duration() != expectedDuration {
		t.Errorf("Duration mismatch: got %v, want %v", dec.Duration(), expectedDuration)
	}

	// Read all PCM
	out, err := io.ReadAll(dec)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}

	expectedPCMBytes := numFrames * FrameSize
	if len(out) != expectedPCMBytes {
		t.Errorf("Output length = %d, want %d", len(out), expectedPCMBytes)
	}

	if dec.Position() != expectedDuration {
		t.Errorf("Position = %v, want %v", dec.Position(), expectedDuration)
	}

	_ = dec.Close()
}

func TestNewStreamDecoderWAV(t *testing.T) {
	wavBytes := buildSyntheticWAV(240)
	dec, err := NewStreamDecoder(bytes.NewReader(wavBytes), "audio/wav")
	if err != nil {
		t.Fatalf("NewStreamDecoder(wav) error: %v", err)
	}
	defer dec.Close()

	buf := make([]byte, 512)
	n, err := dec.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read error: %v", err)
	}
	if n <= 0 {
		t.Errorf("Read returned %d bytes, want > 0", n)
	}
}

func TestDecoderFeedToDevice(t *testing.T) {
	// End-to-end integration:
	// WAV/PCM Stream -> StreamDecoder -> Device -> Sink
	numFrames := 480
	wavBytes := buildSyntheticWAV(numFrames)

	dec, err := NewStreamDecoder(bytes.NewReader(wavBytes), "")
	if err != nil {
		t.Fatalf("NewStreamDecoder: %v", err)
	}

	sink := &recorder{}
	cfg := DefaultDeviceConfig()
	cfg.Volume = 1.0

	dev, err := NewDeviceWithReader(sink, dec, cfg)
	if err != nil {
		t.Fatalf("NewDeviceWithReader: %v", err)
	}

	if err := dev.Play(); err != nil {
		t.Fatalf("Play: %v", err)
	}

	if !dev.WaitPump(2 * time.Second) {
		t.Fatal("Device pump timed out")
	}

	output := sink.bytes()
	if len(output) != numFrames*FrameSize {
		t.Errorf("Pumped bytes = %d, want %d", len(output), numFrames*FrameSize)
	}
}

func TestNewStreamDecoderNil(t *testing.T) {
	if _, err := NewStreamDecoder(nil, ""); err == nil {
		t.Error("NewStreamDecoder(nil) should fail")
	}
}


// ----------------------------------------------------------------------------
// WebM Opus Demuxing & Decoding Tests
// ----------------------------------------------------------------------------

func TestWebMOpusDecoder_Synthesized(t *testing.T) {
	// Encode 20ms (960 samples @ 48kHz stereo) of a 440Hz sine wave using opus.NewEncoder
	enc, err := opus.NewEncoder(opus.EncoderConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	if err != nil {
		t.Fatalf("opus.NewEncoder failed: %v", err)
	}

	numSamples := 960
	pcmInput := make([]int16, numSamples*2)
	for i := 0; i < numSamples; i++ {
		sampleVal := int16(math.Sin(2*math.Pi*440.0*float64(i)/48000.0) * 16000.0)
		pcmInput[i*2] = sampleVal
		pcmInput[i*2+1] = sampleVal
	}

	opusPkt := make([]byte, 1000)
	pktLen, err := enc.Encode(pcmInput, opusPkt)
	if err != nil {
		t.Fatalf("encode opus packet failed: %v", err)
	}
	opusPkt = opusPkt[:pktLen]

	// Build a valid WebM bitstream containing this packet
	webmBytes := buildMinimalWebMOpusStream(t, opusPkt)

	// Demux and decode with WebMOpusDecoder
	dec, err := NewWebMOpusDecoder(bytes.NewReader(webmBytes))
	if err != nil {
		t.Fatalf("NewWebMOpusDecoder failed: %v", err)
	}
	defer dec.Close()

	if dec.Format().SampleRate != 48000 || dec.Format().Channels != 2 {
		t.Errorf("unexpected format: %+v", dec.Format())
	}

	// Read decoded PCM frames
	outPCM := make([]byte, numSamples*FrameSize)
	n, err := io.ReadFull(dec, outPCM)
	if err != nil && err != io.EOF {
		t.Fatalf("read decoded pcm failed: %v", err)
	}
	if n != numSamples*FrameSize {
		t.Errorf("read %d bytes, want %d", n, numSamples*FrameSize)
	}

	if dec.Position() == 0 {
		t.Errorf("expected position > 0, got %v", dec.Position())
	}

	// Next read should return EOF
	var tail [16]byte
	nTail, errTail := dec.Read(tail[:])
	if nTail != 0 || errTail != io.EOF {
		t.Errorf("expected EOF at stream end, got n=%d, err=%v", nTail, errTail)
	}
}

func TestNewStreamDecoder_MP4Rejection(t *testing.T) {
	mp4Header := []byte("\x00\x00\x00\x20ftypisom")
	_, err := NewStreamDecoder(bytes.NewReader(mp4Header), "audio/mp4")
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("expected ErrUnsupportedFormat for mp4, got %v", err)
	}
}

func TestParseXiphLacing(t *testing.T) {
	// Single frame lacing
	p1 := []byte{0x00, 0xAA, 0xBB} // 1 frame, payload 0xAA, 0xBB
	frames := parseXiphLacing(p1)
	if len(frames) != 1 || !bytes.Equal(frames[0], []byte{0xAA, 0xBB}) {
		t.Errorf("expected 1 frame, got %v", frames)
	}

	// Multi frame: 2 frames (lengths 3, 2)
	p2 := []byte{0x01, 0x03, 0x01, 0x02, 0x03, 0x04, 0x05}
	frames2 := parseXiphLacing(p2)
	if len(frames2) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(frames2))
	}
	if !bytes.Equal(frames2[0], []byte{0x01, 0x02, 0x03}) {
		t.Errorf("frame 0 mismatch: %v", frames2[0])
	}
	if !bytes.Equal(frames2[1], []byte{0x04, 0x05}) {
		t.Errorf("frame 1 mismatch: %v", frames2[1])
	}
}

func TestCorruptEBML(t *testing.T) {
	// Truncated header
	truncated := []byte{0x1A, 0x45}
	_, err := NewWebMOpusDecoder(bytes.NewReader(truncated))
	if err == nil {
		t.Error("expected error on truncated EBML header, got nil")
	}

	// Non-EBML header
	invalid := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}
	_, err2 := NewWebMOpusDecoder(bytes.NewReader(invalid))
	if err2 == nil {
		t.Error("expected error on invalid EBML header, got nil")
	}
}

func buildMinimalWebMOpusStream(t *testing.T, opusPkt []byte) []byte {
	buf := new(bytes.Buffer)

	// EBML Header
	ebmlBody := new(bytes.Buffer)
	ebmlBody.Write(makeEBMLUint(0x4286, 1))           // EBMLVersion = 1
	ebmlBody.Write(makeEBMLUint(0x42F7, 1))           // EBMLReadVersion = 1
	ebmlBody.Write(makeEBMLUint(0x42F2, 4))           // EBMLMaxIDLength = 4
	ebmlBody.Write(makeEBMLUint(0x42F3, 8))           // EBMLMaxSizeLength = 8
	ebmlBody.Write(makeEBMLString(0x4282, "webm"))    // DocType = "webm"
	ebmlBody.Write(makeEBMLUint(0x4287, 2))           // DocTypeVersion = 2
	ebmlBody.Write(makeEBMLUint(0x4285, 2))           // DocTypeReadVersion = 2

	buf.Write(makeEBMLElement(ebmlIDHeader, ebmlBody.Bytes()))

	// Segment
	segmentBody := new(bytes.Buffer)

	// Info
	infoBody := new(bytes.Buffer)
	infoBody.Write(makeEBMLUint(ebmlIDTimecodeScale, 1_000_000)) // 1ms scale
	infoBody.Write(makeEBMLFloat32(ebmlIDDuration, 20.0))        // 20ms duration
	segmentBody.Write(makeEBMLElement(ebmlIDInfo, infoBody.Bytes()))

	// Tracks
	tracksBody := new(bytes.Buffer)
	trackEntryBody := new(bytes.Buffer)
	trackEntryBody.Write(makeEBMLUint(ebmlIDTrackNumber, 1))
	trackEntryBody.Write(makeEBMLUint(0x73C5, 1)) // TrackUID
	trackEntryBody.Write(makeEBMLUint(ebmlIDTrackType, 2)) // Audio = 2
	trackEntryBody.Write(makeEBMLString(ebmlIDCodecID, "A_OPUS"))

	// Audio settings
	audioBody := new(bytes.Buffer)
	audioBody.Write(makeEBMLFloat32(ebmlIDSampleFrequency, 48000.0))
	audioBody.Write(makeEBMLUint(ebmlIDChannels, 2))
	trackEntryBody.Write(makeEBMLElement(ebmlIDAudio, audioBody.Bytes()))

	tracksBody.Write(makeEBMLElement(ebmlIDTrackEntry, trackEntryBody.Bytes()))
	segmentBody.Write(makeEBMLElement(ebmlIDTracks, tracksBody.Bytes()))

	// Cluster
	clusterBody := new(bytes.Buffer)
	clusterBody.Write(makeEBMLUint(ebmlIDClusterTimecode, 0)) // Timecode 0

	// SimpleBlock: Track 1 (VINT 0x81), RelTimecode 0 (int16), Flags 0x80 (keyframe, no lacing)
	simpleBlockPayload := new(bytes.Buffer)
	simpleBlockPayload.WriteByte(0x81) // Track 1
	binary.Write(simpleBlockPayload, binary.BigEndian, int16(0)) // rel timecode
	simpleBlockPayload.WriteByte(0x80) // Keyframe, no lacing
	simpleBlockPayload.Write(opusPkt)  // Opus packet bytes

	clusterBody.Write(makeEBMLElement(ebmlIDSimpleBlock, simpleBlockPayload.Bytes()))
	segmentBody.Write(makeEBMLElement(ebmlIDCluster, clusterBody.Bytes()))

	buf.Write(makeEBMLElement(ebmlIDSegment, segmentBody.Bytes()))
	return buf.Bytes()
}

func makeEBMLElement(id uint64, data []byte) []byte {
	out := new(bytes.Buffer)
	out.Write(encodeEBMLID(id))
	out.Write(encodeEBMLSize(uint64(len(data))))
	out.Write(data)
	return out.Bytes()
}

func encodeEBMLID(id uint64) []byte {
	if id > 0x00FFFFFF {
		return []byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}
	}
	if id > 0x0000FFFF {
		return []byte{byte(id >> 16), byte(id >> 8), byte(id)}
	}
	if id > 0x000000FF {
		return []byte{byte(id >> 8), byte(id)}
	}
	return []byte{byte(id)}
}

func encodeEBMLSize(size uint64) []byte {
	if size < 0x7F {
		return []byte{0x80 | byte(size)}
	}
	if size < 0x3FFF {
		return []byte{0x40 | byte(size>>8), byte(size)}
	}
	if size < 0x1FFFFF {
		return []byte{0x20 | byte(size>>16), byte(size>>8), byte(size)}
	}
	return []byte{0x10 | byte(size>>24), byte(size>>16), byte(size>>8), byte(size)}
}

func makeEBMLUint(id uint64, val uint64) []byte {
	buf := new(bytes.Buffer)
	if val <= 0xFF {
		buf.WriteByte(byte(val))
	} else if val <= 0xFFFF {
		binary.Write(buf, binary.BigEndian, uint16(val))
	} else if val <= 0xFFFFFFFF {
		binary.Write(buf, binary.BigEndian, uint32(val))
	} else {
		binary.Write(buf, binary.BigEndian, val)
	}
	return makeEBMLElement(id, buf.Bytes())
}

func makeEBMLFloat32(id uint64, val float32) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, val)
	return makeEBMLElement(id, buf.Bytes())
}

func makeEBMLString(id uint64, str string) []byte {
	return makeEBMLElement(id, []byte(str))
}

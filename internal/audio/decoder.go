// Package audio provides audio decoding, ring buffering, and device output.
//
// TSK-102: Opus/AAC stream demuxer and decoder yielding 48kHz stereo 16-bit PCM frames.
package audio

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/go-opus/oggopus"
	"github.com/tphakala/go-opus/opus"
)

var (
	// ErrUnsupportedFormat is returned when a stream container or codec is unsupported.
	ErrUnsupportedFormat = errors.New("audio/decoder: unsupported stream format")
	// ErrCorruptStream is returned when stream metadata or container frames are invalid.
	ErrCorruptStream = errors.New("audio/decoder: corrupt stream data")
	// ErrNoAudioTrack is returned when a container contains no audio track.
	ErrNoAudioTrack = errors.New("audio/decoder: no audio track found")
)

// StreamDecoder decodes a compressed or uncompressed audio stream into 48kHz
// stereo 16-bit little-endian PCM frames (FrameSize = 4 bytes).
type StreamDecoder interface {
	io.Reader
	io.Closer
	// Format returns the PCM configuration (48kHz stereo).
	Format() DeviceConfig
	// Position returns the current playback timestamp.
	Position() time.Duration
	// Duration returns the total stream duration if known, or 0.
	Duration() time.Duration
}

// SniffContainer inspects the leading bytes of an io.Reader and returns the
// detected stream type ("webm", "ogg", "wav", "mp4", or "unknown").
func SniffContainer(header []byte) string {
	if len(header) >= 4 {
		// WebM / EBML magic: 0x1A 0x45 0xDF 0xA3
		if bytes.HasPrefix(header, []byte{0x1A, 0x45, 0xDF, 0xA3}) {
			return "webm"
		}
		// Ogg container magic: "OggS"
		if bytes.HasPrefix(header, []byte("OggS")) {
			return "ogg"
		}
		// RIFF WAV magic: "RIFF"
		if bytes.HasPrefix(header, []byte("RIFF")) && len(header) >= 12 && string(header[8:12]) == "WAVE" {
			return "wav"
		}
		// MP4 magic: ftyp at offset 4
		if len(header) >= 8 && string(header[4:8]) == "ftyp" {
			return "mp4"
		}
	}
	return "unknown"
}

// NewStreamDecoder inspects r (and optional mimeType hint) and creates an
// appropriate StreamDecoder yielding 48kHz 16-bit stereo PCM.
func NewStreamDecoder(r io.Reader, mimeType string) (StreamDecoder, error) {
	if r == nil {
		return nil, errors.New("audio/decoder: nil reader")
	}

	br := bufio.NewReader(r)
	peek, _ := br.Peek(32)

	detected := SniffContainer(peek)

	switch {
	case detected == "webm" || contains(mimeType, "webm"):
		return NewWebMOpusDecoder(br)
	case detected == "ogg" || contains(mimeType, "ogg") || contains(mimeType, "opus"):
		return NewOggOpusDecoder(br)
	case detected == "wav" || contains(mimeType, "wav"):
		return NewWAVDecoder(br)
	case detected == "mp4" || contains(mimeType, "mp4") || contains(mimeType, "m4a") || contains(mimeType, "aac"):
		return nil, fmt.Errorf("%w: MP4/AAC streams require WebM/Opus stream format (itag 251)", ErrUnsupportedFormat)
	default:
		// Attempt WebM demuxing first, then fallback to OggOpus
		if dec, err := NewWebMOpusDecoder(br); err == nil {
			return dec, nil
		}
		if dec, err := NewOggOpusDecoder(br); err == nil {
			return dec, nil
		}
		return nil, ErrUnsupportedFormat
	}
}

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}

// ============================================================================
// WebM Opus Demuxer & Decoder
// ============================================================================

// EBML Element IDs (Matroska / WebM specification)
const (
	ebmlIDHeader          uint64 = 0x1A45DFA3
	ebmlIDSegment         uint64 = 0x18538067
	ebmlIDSeekHead        uint64 = 0x114D9B74
	ebmlIDInfo            uint64 = 0x1549A966
	ebmlIDTimecodeScale   uint64 = 0x2AD7B1
	ebmlIDDuration        uint64 = 0x4489
	ebmlIDTracks          uint64 = 0x1654AE6B
	ebmlIDTrackEntry      uint64 = 0xAE
	ebmlIDTrackNumber     uint64 = 0xD7
	ebmlIDTrackType       uint64 = 0x83
	ebmlIDCodecID         uint64 = 0x86
	ebmlIDAudio           uint64 = 0xE1
	ebmlIDChannels        uint64 = 0x9F
	ebmlIDSampleFrequency uint64 = 0xB5
	ebmlIDCodecPrivate    uint64 = 0x63A2
	ebmlIDCluster         uint64 = 0x1F43B675
	ebmlIDClusterTimecode uint64 = 0xE7
	ebmlIDSimpleBlock     uint64 = 0xA3
	ebmlIDBlockGroup      uint64 = 0xA0
	ebmlIDBlock           uint64 = 0xA1
)

// WebMOpusDecoder streams WebM Matroska data containing an Opus track and
// outputs decoded 48kHz stereo 16-bit little-endian PCM.
type WebMOpusDecoder struct {
	r             io.Reader
	closer        io.Closer
	opusDec       *opus.Decoder
	config        DeviceConfig
	duration      time.Duration
	timecodeScale int64 // Nanoseconds per timecode unit (default 1,000,000)

	trackNumber     uint64 // Audio track number
	clusterTimecode uint64 // Timestamp of current cluster in timecodeScale units

	pcmBuf         []int16 // Decode scratch buffer
	outBuf         []byte  // Buffer of decoded PCM bytes ready for Read()
	outHead        int
	decodedSamples int64 // Total frames delivered (at 48kHz stereo)

	closed bool
	mu     sync.Mutex
}

// NewWebMOpusDecoder creates a new WebMOpusDecoder reading from r.
func NewWebMOpusDecoder(r io.Reader) (*WebMOpusDecoder, error) {
	var closer io.Closer
	if c, ok := r.(io.Closer); ok {
		closer = c
	}

	dec, err := opus.NewDecoder(SampleRate, Channels)
	if err != nil {
		return nil, fmt.Errorf("audio/decoder: initialize opus: %w", err)
	}

	w := &WebMOpusDecoder{
		r:             r,
		closer:        closer,
		opusDec:       dec,
		config:        DefaultDeviceConfig(),
		timecodeScale: 1_000_000, // 1ms default
		trackNumber:   1,         // default to track 1
		pcmBuf:        make([]int16, 5760*Channels), // Max 120ms Opus frame
	}

	// Read WebM headers (EBML, Segment Info, Tracks)
	if err := w.parseHeaders(); err != nil {
		return nil, err
	}

	return w, nil
}

func (w *WebMOpusDecoder) Format() DeviceConfig {
	return w.config
}

func (w *WebMOpusDecoder) Duration() time.Duration {
	return w.duration
}

func (w *WebMOpusDecoder) Position() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return time.Duration(w.decodedSamples) * time.Second / SampleRate
}

func (w *WebMOpusDecoder) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.closer != nil {
		return w.closer.Close()
	}
	return nil
}

func (w *WebMOpusDecoder) Read(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, io.ErrClosedPipe
	}

	for len(w.outBuf)-w.outHead == 0 {
		// Buffer is empty: demux next Opus packet and decode into outBuf
		done, err := w.decodeNextPacket()
		if err != nil {
			return 0, err
		}
		if done {
			return 0, io.EOF
		}
	}

	n := copy(p, w.outBuf[w.outHead:])
	w.outHead += n
	w.decodedSamples += int64(n / FrameSize)

	// Trim buffer when drained to avoid memory creep
	if w.outHead >= len(w.outBuf) {
		w.outBuf = w.outBuf[:0]
		w.outHead = 0
	}

	return n, nil
}

// parseHeaders processes EBML Header, Segment, Info, and Tracks.
func (w *WebMOpusDecoder) parseHeaders() error {
	id, size, err := readElementHeader(w.r)
	if err != nil {
		return fmt.Errorf("%w: failed to read EBML header: %v", ErrCorruptStream, err)
	}
	if id != ebmlIDHeader {
		return fmt.Errorf("%w: expected EBML header (0x1A45DFA3), got 0x%X", ErrUnsupportedFormat, id)
	}

	// Skip EBML header payload
	if err := discardBytes(w.r, size); err != nil {
		return err
	}

	// Read Segment Element
	id, _, err = readElementHeader(w.r)
	if err != nil {
		return fmt.Errorf("%w: failed to read Segment header: %v", ErrCorruptStream, err)
	}
	if id != ebmlIDSegment {
		return fmt.Errorf("%w: expected Segment (0x18538067), got 0x%X", ErrUnsupportedFormat, id)
	}

	// Segment child loop: read until we encounter the first Cluster
	for {
		childID, childSize, err := readElementHeader(w.r)
		if err != nil {
			return err
		}

		switch childID {
		case ebmlIDInfo:
			if err := w.parseInfo(childSize); err != nil {
				return err
			}
		case ebmlIDTracks:
			if err := w.parseTracks(childSize); err != nil {
				return err
			}
		case ebmlIDCluster:
			// Reached the first cluster
			w.clusterTimecode = 0
			return nil
		default:
			// Skip other segment children (SeekHead, Cues, Tags, etc.)
			if childSize > 0 {
				if err := discardBytes(w.r, childSize); err != nil {
					return err
				}
			}
		}
	}
}

func (w *WebMOpusDecoder) parseInfo(size uint64) error {
	lr := io.LimitReader(w.r, int64(size))
	for {
		id, elemSize, err := readElementHeader(lr)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		switch id {
		case ebmlIDTimecodeScale:
			val, err := readUint(lr, elemSize)
			if err != nil {
				return err
			}
			if val > 0 {
				w.timecodeScale = int64(val)
			}
		case ebmlIDDuration:
			durFloat, err := readFloat(lr, elemSize)
			if err != nil {
				return err
			}
			ns := durFloat * float64(w.timecodeScale)
			w.duration = time.Duration(ns)
		default:
			if err := discardBytes(lr, elemSize); err != nil {
				return err
			}
		}
	}
}

func (w *WebMOpusDecoder) parseTracks(size uint64) error {
	lr := io.LimitReader(w.r, int64(size))
	foundAudio := false

	for {
		id, elemSize, err := readElementHeader(lr)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}

		if id == ebmlIDTrackEntry {
			entryLR := io.LimitReader(lr, int64(elemSize))
			var trackNum uint64
			var trackType uint64
			var codecID string

			for {
				tid, tsize, terr := readElementHeader(entryLR)
				if terr != nil {
					if errors.Is(terr, io.EOF) {
						break
					}
					return terr
				}

				switch tid {
				case ebmlIDTrackNumber:
					trackNum, _ = readUint(entryLR, tsize)
				case ebmlIDTrackType:
					trackType, _ = readUint(entryLR, tsize)
				case ebmlIDCodecID:
					strBytes := make([]byte, tsize)
					if _, err := io.ReadFull(entryLR, strBytes); err == nil {
						codecID = string(bytes.TrimRight(strBytes, "\x00"))
					}
				default:
					_ = discardBytes(entryLR, tsize)
				}
			}

			// TrackType 2 is Audio
			if trackType == 2 || codecID == "A_OPUS" {
				w.trackNumber = trackNum
				foundAudio = true
			}
		} else {
			_ = discardBytes(lr, elemSize)
		}
	}

	if !foundAudio && w.trackNumber == 0 {
		w.trackNumber = 1 // Fallback default
	}
	return nil
}

// decodeNextPacket scans forward for the next SimpleBlock or Block belonging
// to the audio track, decodes it via Opus, and appends the PCM bytes to outBuf.
func (w *WebMOpusDecoder) decodeNextPacket() (bool, error) {
	for {
		id, size, err := readElementHeader(w.r)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return true, nil
			}
			return false, err
		}

		switch id {
		case ebmlIDCluster:
			// New cluster encountered
			continue
		case ebmlIDClusterTimecode:
			tc, err := readUint(w.r, size)
			if err != nil {
				return false, err
			}
			w.clusterTimecode = tc
		case ebmlIDSimpleBlock, ebmlIDBlock:
			// Read block data
			blockData := make([]byte, size)
			if _, err := io.ReadFull(w.r, blockData); err != nil {
				return false, err
			}
			if len(blockData) < 4 {
				continue
			}

			// Matroska Block Header:
			// 1. Track Number (EBML VINT)
			trackNum, offset := parseVINT(blockData)
			if trackNum != w.trackNumber && w.trackNumber != 0 {
				continue // Skip other tracks
			}

			if offset+3 > len(blockData) {
				continue
			}

			// 2. Relative timecode (int16 signed)
			offset += 2

			// 3. Flags (1 byte)
			flags := blockData[offset]
			offset++

			lacing := (flags >> 1) & 0x03
			payload := blockData[offset:]

			// Decode packet frames
			switch lacing {
			case 0:
				// No lacing: payload is a single Opus packet
				if err := w.decodeOpusFrame(payload); err != nil {
					// Soft packet error: concealment
					_, _ = w.opusDec.Decode(nil, w.pcmBuf)
				}
				return false, nil
			case 1:
				// Xiph lacing
				frames := parseXiphLacing(payload)
				for _, f := range frames {
					_ = w.decodeOpusFrame(f)
				}
				return false, nil
			default:
				// Fallback decode whole payload
				_ = w.decodeOpusFrame(payload)
				return false, nil
			}

		default:
			// Skip irrelevant sub-elements or data
			if size > 0 {
				if err := discardBytes(w.r, size); err != nil {
					if errors.Is(err, io.EOF) {
						return true, nil
					}
					return false, err
				}
			}
		}
	}
}

func (w *WebMOpusDecoder) decodeOpusFrame(pkt []byte) error {
	if len(pkt) == 0 {
		return nil
	}

	samples, err := w.opusDec.Decode(pkt, w.pcmBuf)
	if err != nil {
		return err
	}

	total := samples * Channels
	if total > len(w.pcmBuf) {
		total = len(w.pcmBuf)
	}

	// Convert int16 samples to little-endian bytes
	start := len(w.outBuf)
	needed := total * BytesPerSample
	w.outBuf = growSlice(w.outBuf, needed)

	for i := 0; i < total; i++ {
		s := uint16(w.pcmBuf[i])
		idx := start + i*2
		w.outBuf[idx] = byte(s)
		w.outBuf[idx+1] = byte(s >> 8)
	}

	return nil
}

func growSlice(b []byte, extra int) []byte {
	n := len(b)
	if cap(b)-n < extra {
		newCap := 2 * cap(b)
		if newCap < n+extra {
			newCap = n + extra
		}
		newB := make([]byte, n+extra, newCap)
		copy(newB, b)
		return newB
	}
	return b[:n+extra]
}

// parseXiphLacing extracts individual frames from a Xiph-laced payload.
func parseXiphLacing(payload []byte) [][]byte {
	if len(payload) == 0 {
		return nil
	}
	numFrames := int(payload[0]) + 1
	offset := 1

	lengths := make([]int, numFrames)
	for i := 0; i < numFrames-1; i++ {
		l := 0
		for offset < len(payload) {
			b := int(payload[offset])
			offset++
			l += b
			if b < 255 {
				break
			}
		}
		lengths[i] = l
	}

	// Last frame gets the remainder
	consumed := 0
	for i := 0; i < numFrames-1; i++ {
		consumed += lengths[i]
	}
	rem := len(payload) - offset - consumed
	if rem >= 0 {
		lengths[numFrames-1] = rem
	}

	var frames [][]byte
	curr := offset
	for _, l := range lengths {
		if curr+l <= len(payload) {
			frames = append(frames, payload[curr:curr+l])
			curr += l
		}
	}
	return frames
}

// ============================================================================
// Ogg Opus Decoder
// ============================================================================

// OggOpusDecoder wraps github.com/tphakala/go-opus/oggopus yielding 48kHz stereo PCM.
type OggOpusDecoder struct {
	dec       *oggopus.Decoder
	closer    io.Closer
	bytesRead atomic.Int64
	config    DeviceConfig
}

// NewOggOpusDecoder creates a StreamDecoder from an Ogg Opus stream.
func NewOggOpusDecoder(r io.Reader) (*OggOpusDecoder, error) {
	var closer io.Closer
	if c, ok := r.(io.Closer); ok {
		closer = c
	}

	dec, err := oggopus.NewDecoder(r)
	if err != nil {
		return nil, fmt.Errorf("audio/decoder: initialize oggopus: %w", err)
	}

	info := dec.Info()
	cfg := DeviceConfig{
		SampleRate: SampleRate,
		Channels:   info.Channels,
		Volume:     0.8,
	}

	return &OggOpusDecoder{
		dec:    dec,
		closer: closer,
		config: cfg,
	}, nil
}

func (d *OggOpusDecoder) Format() DeviceConfig {
	return d.config
}

func (d *OggOpusDecoder) Duration() time.Duration {
	return 0 // Dynamic/stream duration
}

func (d *OggOpusDecoder) Position() time.Duration {
	b := d.bytesRead.Load()
	frames := b / int64(FrameSize)
	return time.Duration(frames) * time.Second / SampleRate
}

func (d *OggOpusDecoder) Read(p []byte) (int, error) {
	n, err := d.dec.Read(p)
	if n > 0 {
		d.bytesRead.Add(int64(n))
	}
	return n, err
}

func (d *OggOpusDecoder) Close() error {
	if d.closer != nil {
		return d.closer.Close()
	}
	return nil
}

// ============================================================================
// WAV / Raw PCM Decoder
// ============================================================================

// WAVDecoder reads a RIFF/WAVE file and streams 48kHz stereo 16-bit PCM.
type WAVDecoder struct {
	r          io.Reader
	closer     io.Closer
	config     DeviceConfig
	bytesLeft  int64
	totalBytes int64
	bytesRead  atomic.Int64
}

// NewWAVDecoder parses a RIFF/WAVE header and returns a StreamDecoder.
func NewWAVDecoder(r io.Reader) (*WAVDecoder, error) {
	var closer io.Closer
	if c, ok := r.(io.Closer); ok {
		closer = c
	}

	header := make([]byte, 12)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%w: invalid RIFF/WAVE header", ErrUnsupportedFormat)
	}

	var audioFormat uint16
	var numChannels uint16
	var sampleRate uint32
	var bitsPerSample uint16
	var dataSize int64

	// Read chunks
	for {
		chunkHeader := make([]byte, 8)
		if _, err := io.ReadFull(r, chunkHeader); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}

		chunkID := string(chunkHeader[0:4])
		chunkSize := int64(binary.LittleEndian.Uint32(chunkHeader[4:8]))

		if chunkID == "fmt " {
			fmtData := make([]byte, chunkSize)
			if _, err := io.ReadFull(r, fmtData); err != nil {
				return nil, err
			}
			if len(fmtData) < 16 {
				return nil, ErrCorruptStream
			}
			audioFormat = binary.LittleEndian.Uint16(fmtData[0:2])
			numChannels = binary.LittleEndian.Uint16(fmtData[2:4])
			sampleRate = binary.LittleEndian.Uint32(fmtData[4:8])
			bitsPerSample = binary.LittleEndian.Uint16(fmtData[14:16])
		} else if chunkID == "data" {
			dataSize = chunkSize
			break
		} else {
			if err := discardBytes(r, uint64(chunkSize)); err != nil {
				return nil, err
			}
		}
	}

	if audioFormat != 1 {
		return nil, fmt.Errorf("%w: unsupported non-PCM WAV format (%d)", ErrUnsupportedFormat, audioFormat)
	}
	if bitsPerSample != 16 {
		return nil, fmt.Errorf("%w: unsupported WAV bit depth (%d, want 16)", ErrUnsupportedFormat, bitsPerSample)
	}

	cfg := DeviceConfig{
		SampleRate: int(sampleRate),
		Channels:   int(numChannels),
		Volume:     0.8,
	}

	return &WAVDecoder{
		r:          r,
		closer:     closer,
		config:     cfg,
		bytesLeft:  dataSize,
		totalBytes: dataSize,
	}, nil
}

func (w *WAVDecoder) Format() DeviceConfig {
	return w.config
}

func (w *WAVDecoder) Duration() time.Duration {
	if w.totalBytes <= 0 || w.config.SampleRate <= 0 || w.config.Channels <= 0 {
		return 0
	}
	frames := w.totalBytes / int64(w.config.Channels*BytesPerSample)
	return time.Duration(frames) * time.Second / time.Duration(w.config.SampleRate)
}

func (w *WAVDecoder) Position() time.Duration {
	frames := w.bytesRead.Load() / int64(w.config.Channels*BytesPerSample)
	return time.Duration(frames) * time.Second / time.Duration(w.config.SampleRate)
}

func (w *WAVDecoder) Read(p []byte) (int, error) {
	if w.bytesLeft <= 0 && w.totalBytes > 0 {
		return 0, io.EOF
	}

	toRead := len(p)
	if w.totalBytes > 0 && int64(toRead) > w.bytesLeft {
		toRead = int(w.bytesLeft)
	}

	n, err := w.r.Read(p[:toRead])
	if n > 0 {
		w.bytesLeft -= int64(n)
		w.bytesRead.Add(int64(n))
	}
	return n, err
}

func (w *WAVDecoder) Close() error {
	if w.closer != nil {
		return w.closer.Close()
	}
	return nil
}

// ============================================================================
// EBML Low-Level Helpers
// ============================================================================

// readElementHeader parses an EBML Element ID and its Data Size.
func readElementHeader(r io.Reader) (id uint64, size uint64, err error) {
	id, _, err = readVINT(r, true)
	if err != nil {
		return 0, 0, err
	}

	size, _, err = readVINT(r, false)
	if err != nil {
		return 0, 0, err
	}

	return id, size, nil
}

// readVINT parses an EBML variable-length integer.
// When keepMask is true (for Element IDs), the leading marker bit is retained.
func readVINT(r io.Reader, keepMask bool) (uint64, int, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, 0, err
	}

	first := b[0]
	if first == 0 {
		return 0, 0, ErrCorruptStream
	}

	// Calculate length from leading zeros
	length := 1
	var mask byte = 0x80
	for (first & mask) == 0 {
		length++
		mask >>= 1
	}

	if length > 8 {
		return 0, 0, ErrCorruptStream
	}

	var val uint64
	if keepMask {
		val = uint64(first)
	} else {
		val = uint64(first & (^mask))
	}

	for i := 1; i < length; i++ {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, 0, err
		}
		val = (val << 8) | uint64(b[0])
	}

	return val, length, nil
}

// parseVINT parses an EBML VINT from a byte slice, returning the value and byte length.
func parseVINT(b []byte) (uint64, int) {
	if len(b) == 0 {
		return 0, 0
	}
	first := b[0]
	length := 1
	var mask byte = 0x80
	for (first & mask) == 0 {
		length++
		mask >>= 1
		if length > 8 || length > len(b) {
			return 0, 0
		}
	}

	val := uint64(first & (^mask))
	for i := 1; i < length; i++ {
		val = (val << 8) | uint64(b[i])
	}
	return val, length
}

func readUint(r io.Reader, size uint64) (uint64, error) {
	if size > 8 {
		return 0, ErrCorruptStream
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	var val uint64
	for _, b := range buf {
		val = (val << 8) | uint64(b)
	}
	return val, nil
}

func readFloat(r io.Reader, size uint64) (float64, error) {
	if size == 4 {
		var u uint32
		if err := binary.Read(r, binary.BigEndian, &u); err != nil {
			return 0, err
		}
		return float64(math.Float32frombits(u)), nil
	} else if size == 8 {
		var u uint64
		if err := binary.Read(r, binary.BigEndian, &u); err != nil {
			return 0, err
		}
		return math.Float64frombits(u), nil
	}
	return 0, ErrCorruptStream
}

func discardBytes(r io.Reader, n uint64) error {
	if n == 0 {
		return nil
	}
	_, err := io.CopyN(io.Discard, r, int64(n))
	return err
}

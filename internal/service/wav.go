package service

import (
	"encoding/binary"
	"fmt"
	"github.com/pion/rtp"
	"io"
	"sync"
)

// WAVWriter handles writing PCM samples into a WAV container.
type WAVWriter struct {
	file          io.WriteCloser
	sampleRate    int
	channels      int
	bytesWritten  uint32
	headerWritten bool
	finalized     bool
	mu            sync.Mutex
}

// NewWAVWriter creates a WAV writer and writes an initial header.
func NewWAVWriter(file io.WriteCloser, sampleRate, channels int) (*WAVWriter, error) {
	if file == nil {
		return nil, fmt.Errorf("nil file provided for WAV writer")
	}
	if sampleRate <= 0 {
		sampleRate = 8000
	}
	if channels <= 0 {
		channels = 1
	}

	writer := &WAVWriter{
		file:       file,
		sampleRate: sampleRate,
		channels:   channels,
	}

	if err := writer.writeHeader(); err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *WAVWriter) WriteRTP(packet *rtp.Packet) error {
	if len(packet.Payload) == 0 {
		return nil
	}
	_, err := w.file.Write(packet.Payload)
	return err
}

func (w *WAVWriter) Close() error {
	return w.file.Close()
}

// Write appends PCM samples to the WAV file.
func (w *WAVWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.headerWritten {
		if err := w.writeHeaderLocked(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.bytesWritten += uint32(n)
	return n, err
}

func (w *WAVWriter) writeHeader() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeHeaderLocked()
}

func (w *WAVWriter) writeHeaderLocked() error {
	header := make([]byte, 44)

	// ChunkID "RIFF"
	copy(header[0:], []byte("RIFF"))
	// ChunkSize (placeholder, will be updated in Finalize)
	binary.LittleEndian.PutUint32(header[4:], 36)
	// Format "WAVE"
	copy(header[8:], []byte("WAVE"))
	// Subchunk1ID "fmt "
	copy(header[12:], []byte("fmt "))
	// Subchunk1Size (16 for PCM)
	binary.LittleEndian.PutUint32(header[16:], 16)
	// AudioFormat (1 = PCM)
	binary.LittleEndian.PutUint16(header[20:], 1)
	// NumChannels
	binary.LittleEndian.PutUint16(header[22:], uint16(w.channels))
	// SampleRate
	binary.LittleEndian.PutUint32(header[24:], uint32(w.sampleRate))
	// ByteRate = SampleRate * NumChannels * BitsPerSample/8 (16-bit samples)
	byteRate := uint32(w.sampleRate * w.channels * 2)
	binary.LittleEndian.PutUint32(header[28:], byteRate)
	// BlockAlign = NumChannels * BitsPerSample/8
	blockAlign := uint16(w.channels * 2)
	binary.LittleEndian.PutUint16(header[32:], blockAlign)
	// BitsPerSample = 16
	binary.LittleEndian.PutUint16(header[34:], 16)
	// Subchunk2ID "data"
	copy(header[36:], []byte("data"))
	// Subchunk2Size placeholder
	binary.LittleEndian.PutUint32(header[40:], 0)

	_, err := w.file.Write(header)
	if err != nil {
		return err
	}

	w.headerWritten = true
	return nil
}

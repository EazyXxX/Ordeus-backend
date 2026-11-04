package ai

import (
	"encoding/binary"
	"errors"
)

const (
	sampleRate     = 16_000
	bytesPerSample = 2
	bytesPerSecond = sampleRate * bytesPerSample
	wavHeaderSize  = 44
	riffChunkHead  = 8
	wavFormatPCM   = 1

	// The model's context fits about two minutes of audio with its transcript.
	maxChunkBytes = 90 * bytesPerSecond
	// Each cut lands on the quietest moment of this final stretch of a chunk.
	cutSearchBytes = 10 * bytesPerSecond
	cutWindowBytes = bytesPerSecond / 10
)

var errInvalidAudio = errors.New("audio must be 16 kHz mono 16-bit PCM WAV")

// parseWAV returns the samples of a WAV file in the one format the app records.
func parseWAV(data []byte) ([]byte, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errInvalidAudio
	}

	var validFormat bool
	for offset := 12; offset+riffChunkHead <= len(data); {
		id := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4:]))
		start := offset + riffChunkHead
		end := min(start+size, len(data))

		switch id {
		case "fmt ":
			validFormat = isSupportedFormat(data[start:end])
		case "data":
			if !validFormat {
				return nil, errInvalidAudio
			}
			return data[start:end], nil
		}
		// Chunks are padded to an even size.
		offset = start + size + size%2
	}
	return nil, errInvalidAudio
}

func isSupportedFormat(format []byte) bool {
	return len(format) >= 16 &&
		binary.LittleEndian.Uint16(format[0:]) == wavFormatPCM &&
		binary.LittleEndian.Uint16(format[2:]) == 1 &&
		binary.LittleEndian.Uint32(format[4:]) == sampleRate &&
		binary.LittleEndian.Uint16(format[14:]) == 8*bytesPerSample
}

func encodeWAV(pcm []byte) []byte {
	wav := make([]byte, wavHeaderSize, wavHeaderSize+len(pcm))
	copy(wav[0:], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(wavHeaderSize-riffChunkHead+len(pcm)))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], wavFormatPCM)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], sampleRate)
	binary.LittleEndian.PutUint32(wav[28:], bytesPerSecond)
	binary.LittleEndian.PutUint16(wav[32:], bytesPerSample)
	binary.LittleEndian.PutUint16(wav[34:], 8*bytesPerSample)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(pcm)))
	return append(wav, pcm...)
}

// splitPCM cuts long audio at quiet moments, so words stay whole.
func splitPCM(pcm []byte, limit int) [][]byte {
	var chunks [][]byte
	for len(pcm) > limit {
		cut := quietestCut(pcm[:limit])
		chunks = append(chunks, pcm[:cut])
		pcm = pcm[cut:]
	}
	return append(chunks, pcm)
}

func quietestCut(pcm []byte) int {
	best, bestEnergy := len(pcm), int64(-1)
	for end := len(pcm); end >= max(len(pcm)-cutSearchBytes, cutWindowBytes); end -= cutWindowBytes {
		if energy := windowEnergy(pcm[end-cutWindowBytes : end]); bestEnergy < 0 || energy < bestEnergy {
			best, bestEnergy = end, energy
		}
	}
	return best
}

func windowEnergy(window []byte) int64 {
	var energy int64
	for i := 0; i+1 < len(window); i += bytesPerSample {
		sample := int64(int16(binary.LittleEndian.Uint16(window[i:])))
		energy += max(sample, -sample)
	}
	return energy
}

package ai

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func tone(seconds float64, amplitude int16) []byte {
	pcm := make([]byte, int(seconds*bytesPerSecond)&^1)
	for i := 0; i+1 < len(pcm); i += bytesPerSample {
		binary.LittleEndian.PutUint16(pcm[i:], uint16(amplitude))
	}
	return pcm
}

func TestWAVRoundTrip(t *testing.T) {
	pcm := tone(0.5, 1000)
	got, err := parseWAV(encodeWAV(pcm))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pcm) {
		t.Errorf("samples changed: %d bytes, want %d", len(got), len(pcm))
	}
}

func TestParseWAVSkipsExtraChunks(t *testing.T) {
	wav := encodeWAV(tone(0.1, 1))
	extra := append([]byte("LIST"), 3, 0, 0, 0, 'a', 'b', 'c', 0)
	withExtra := bytes.Join([][]byte{wav[:36], extra, wav[36:]}, nil)

	if _, err := parseWAV(withExtra); err != nil {
		t.Fatal(err)
	}
}

func TestParseWAVRejectsOtherFormats(t *testing.T) {
	stereo := encodeWAV(tone(0.1, 1))
	binary.LittleEndian.PutUint16(stereo[22:], 2)
	otherRate := encodeWAV(tone(0.1, 1))
	binary.LittleEndian.PutUint32(otherRate[24:], 44_100)

	for name, data := range map[string][]byte{
		"not audio": []byte("hello"),
		"stereo":    stereo,
		"44.1 kHz":  otherRate,
		"truncated": encodeWAV(nil)[:20],
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseWAV(data); !errors.Is(err, errInvalidAudio) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestSplitPCMKeepsShortAudioWhole(t *testing.T) {
	pcm := tone(5, 1000)
	if chunks := splitPCM(pcm, maxChunkBytes); len(chunks) != 1 || !bytes.Equal(chunks[0], pcm) {
		t.Errorf("chunks = %d", len(chunks))
	}
}

func TestSplitPCMCutsAtSilenceAndLosesNothing(t *testing.T) {
	const pauseAt = 85
	pcm := bytes.Join([][]byte{tone(pauseAt, 3000), tone(1, 0), tone(100, 3000)}, nil)

	chunks := splitPCM(pcm, maxChunkBytes)

	if len(chunks) < 2 {
		t.Fatalf("chunks = %d", len(chunks))
	}
	first := len(chunks[0]) / bytesPerSecond
	if first < pauseAt || first > pauseAt+1 {
		t.Errorf("first cut at %d s, want the pause at %d s", first, pauseAt)
	}
	if !bytes.Equal(bytes.Join(chunks, nil), pcm) {
		t.Error("joined chunks differ from the original audio")
	}
	for i, chunk := range chunks {
		if len(chunk) > maxChunkBytes {
			t.Errorf("chunk %d is %d bytes", i, len(chunk))
		}
		if len(chunk)%bytesPerSample != 0 {
			t.Errorf("chunk %d splits a sample", i)
		}
	}
}

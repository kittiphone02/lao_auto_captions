package main

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
)

// wavPeaks reads a 16-bit PCM WAV and returns n peak levels (0-1) plus its length in seconds.
func wavPeaks(path string, n int) ([]float64, float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	var riff [12]byte
	if _, err := io.ReadFull(f, riff[:]); err != nil || string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return nil, 0, errors.New("not a WAV file")
	}
	var channels, bits uint16
	var rate uint32
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			return nil, 0, errors.New("no audio data in WAV")
		}
		size := int64(binary.LittleEndian.Uint32(hdr[4:8]))
		switch string(hdr[0:4]) {
		case "fmt ":
			fmtb := make([]byte, size)
			if _, err := io.ReadFull(f, fmtb); err != nil {
				return nil, 0, err
			}
			channels = binary.LittleEndian.Uint16(fmtb[2:4])
			rate = binary.LittleEndian.Uint32(fmtb[4:8])
			bits = binary.LittleEndian.Uint16(fmtb[14:16])
		case "data":
			if bits != 16 || channels == 0 || rate == 0 {
				return nil, 0, errors.New("only 16-bit PCM WAV is supported")
			}
			data := make([]byte, size)
			k, _ := io.ReadFull(f, data)
			data = data[:k-k%int(2*channels)]
			frames := len(data) / int(2*channels)
			peaks := make([]float64, n)
			for i := 0; i < frames; i++ {
				v := math.Abs(float64(int16(binary.LittleEndian.Uint16(data[i*int(2*channels):])))) / 32768
				b := i * n / max(frames, 1)
				peaks[b] = max(peaks[b], v)
			}
			for i := range peaks {
				peaks[i] = math.Round(peaks[i]*1000) / 1000
			}
			return peaks, float64(frames) / float64(rate), nil
		default:
			if _, err := f.Seek(size+size%2, io.SeekCurrent); err != nil {
				return nil, 0, err
			}
		}
	}
}

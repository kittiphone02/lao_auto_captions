package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Segment is one caption line, as returned by GET /jobs/:id/segments and accepted by PUT.
type Segment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Lao     string  `json:"lao"`
	Sfx     string  `json:"sfx,omitempty"`     // sound effect played when the caption appears, e.g. "pop"
	Whisper string  `json:"whisper,omitempty"` // raw candidates from candidates.json (read-only)
	Buzz    string  `json:"buzz,omitempty"`
	W2V     string  `json:"w2v,omitempty"`
	LLMOK   *bool   `json:"llm_ok,omitempty"`
}

var blockSep = regexp.MustCompile(`\r?\n\s*\r?\n`)

// parseSRT mirrors read_srt in lao_caption.py: wrapped lines are joined with a space.
func parseSRT(s string) ([]Segment, error) {
	s = strings.TrimPrefix(s, string(rune(0xFEFF))) // BOM
	out := []Segment{}
	for _, b := range blockSep.Split(strings.TrimSpace(s), -1) {
		lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(b), "\r\n", "\n"), "\n")
		if len(lines) < 3 {
			continue
		}
		parts := strings.Split(lines[1], "-->")
		if len(parts) != 2 {
			return nil, fmt.Errorf("bad timing line %q", lines[1])
		}
		start, err1 := parseTS(parts[0])
		end, err2 := parseTS(parts[1])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("bad timing line %q", lines[1])
		}
		text := make([]string, 0, len(lines)-2)
		for _, l := range lines[2:] {
			text = append(text, strings.TrimSpace(l))
		}
		out = append(out, Segment{Start: start, End: end, Lao: strings.Join(text, " ")})
	}
	return out, nil
}

func parseTS(t string) (float64, error) {
	p := strings.Split(strings.ReplaceAll(strings.TrimSpace(t), ",", "."), ":")
	if len(p) != 3 {
		return 0, fmt.Errorf("bad timestamp %q", t)
	}
	var sec float64
	for i, m := range []float64{3600, 60, 1} {
		v, err := strconv.ParseFloat(p[i], 64)
		if err != nil {
			return 0, err
		}
		sec += v * m
	}
	return sec, nil
}

// writeSRT puts each caption on one line; lao_caption.py burn re-wraps long lines itself.
func writeSRT(segs []Segment) string {
	var b strings.Builder
	for i, s := range segs {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, fmtTS(s.Start), fmtTS(s.End), strings.TrimSpace(s.Lao))
	}
	return b.String()
}

func fmtTS(t float64) string {
	ms := int64(t*1000 + 0.5)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

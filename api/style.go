package main

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// Style is how burned captions look. Zero values mean "use the default"
// (lao_caption.py fills them in: size 3.7, position 13, white on black, no animation).
type Style struct {
	Size         float64  `json:"size,omitempty"`          // font size, % of video height (1.5-12)
	Position     float64  `json:"position,omitempty"`      // gap from the bottom, % of video height (2-60)
	Color        string   `json:"color,omitempty"`         // "#RRGGBB"
	OutlineColor string   `json:"outline_color,omitempty"` // "#RRGGBB"
	Animation    string   `json:"animation,omitempty"`     // none | fade | pop | slide
	AnimMS       int      `json:"anim_ms,omitempty"`       // 50-2000
	SfxVolume    *float64 `json:"sfx_volume,omitempty"`    // sound effect loudness 0-2 (nil = 1)
	MaxLines     int      `json:"max_lines,omitempty"`     // longer captions show as pages of this many lines (1-6, default 2)
}

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func (s *Style) Validate() error {
	switch {
	case s.Size != 0 && (s.Size < 1.5 || s.Size > 12):
		return fmt.Errorf("style.size must be 1.5-12 (%% of video height)")
	case s.Position != 0 && (s.Position < 2 || s.Position > 60):
		return fmt.Errorf("style.position must be 2-60 (%% of video height)")
	case s.Color != "" && !hexColor.MatchString(s.Color):
		return fmt.Errorf("style.color must look like #FFFFFF")
	case s.OutlineColor != "" && !hexColor.MatchString(s.OutlineColor):
		return fmt.Errorf("style.outline_color must look like #000000")
	case s.AnimMS != 0 && (s.AnimMS < 50 || s.AnimMS > 2000):
		return fmt.Errorf("style.anim_ms must be 50-2000")
	case s.SfxVolume != nil && (*s.SfxVolume < 0 || *s.SfxVolume > 2):
		return fmt.Errorf("style.sfx_volume must be 0-2")
	case s.MaxLines != 0 && (s.MaxLines < 1 || s.MaxLines > 6):
		return fmt.Errorf("style.max_lines must be 1-6")
	}
	switch s.Animation {
	case "", "none", "fade", "pop", "slide":
		return nil
	}
	return fmt.Errorf("style.animation must be none, fade, pop or slide")
}

// Arg is the value for lao_caption.py --style.
func (s *Style) Arg() string {
	b, _ := json.Marshal(s)
	return string(b)
}

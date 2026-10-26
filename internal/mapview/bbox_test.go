package mapview

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestParseBBox(t *testing.T) {
	valid := BBox{MinLng: 13.3, MinLat: 52.45, MaxLng: 13.5, MaxLat: 52.6}
	cases := []struct {
		name string
		vals []string
		want BBox
		ok   bool
	}{
		{"valid", []string{"13.3,52.45,13.5,52.6"}, valid, true},
		{"whole world", []string{"-180,-90,180,90"}, BBox{-180, -90, 180, 90}, true},
		{"signed", []string{"-13.5,-0.1,+0.5,0.2"}, BBox{-13.5, -0.1, 0.5, 0.2}, true},
		{"missing", nil, BBox{}, false},
		{"duplicated", []string{"13.3,52.45,13.5,52.6", "13.3,52.45,13.5,52.6"}, BBox{}, false},
		{"empty value", []string{""}, BBox{}, false},
		{"three parts", []string{"13.3,52.45,13.5"}, BBox{}, false},
		{"five parts", []string{"13.3,52.45,13.5,52.6,1"}, BBox{}, false},
		{"leading space", []string{" 13.3,52.45,13.5,52.6"}, BBox{}, false},
		{"trailing space", []string{"13.3,52.45,13.5,52.6 "}, BBox{}, false},
		{"inner space", []string{"13.3, 52.45,13.5,52.6"}, BBox{}, false},
		{"non-numeric", []string{"13.3,52.45,abc,52.6"}, BBox{}, false},
		{"NaN", []string{"NaN,52.45,13.5,52.6"}, BBox{}, false},
		{"Inf", []string{"13.3,52.45,Inf,52.6"}, BBox{}, false},
		{"negative Inf", []string{"13.3,-Inf,13.5,52.6"}, BBox{}, false},
		{"exponent", []string{"1e1,52.45,13.5,52.6"}, BBox{}, false},
		{"hex float", []string{"0x1p-2,52.45,13.5,52.6"}, BBox{}, false},
		{"lng below range", []string{"-180.5,52.45,13.5,52.6"}, BBox{}, false},
		{"lng above range", []string{"13.3,52.45,180.5,52.6"}, BBox{}, false},
		{"lat below range", []string{"13.3,-90.5,13.5,52.6"}, BBox{}, false},
		{"lat above range", []string{"13.3,52.45,13.5,90.5"}, BBox{}, false},
		{"zero-area lng", []string{"13.3,52.45,13.3,52.6"}, BBox{}, false},
		{"zero-area lat", []string{"13.3,52.45,13.5,52.45"}, BBox{}, false},
		{"inverted lng", []string{"13.5,52.45,13.3,52.6"}, BBox{}, false},
		{"inverted lat", []string{"13.3,52.6,13.5,52.45"}, BBox{}, false},
		{"antimeridian", []string{"170,-10,-170,10"}, BBox{}, false},
		{"leading comma", []string{",13.3,52.45,13.5,52.6"}, BBox{}, false},
		{"trailing comma", []string{"13.3,52.45,13.5,52.6,"}, BBox{}, false},
		{"empty field", []string{"13.3,,13.5,52.6"}, BBox{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseBBox(tc.vals)
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("ParseBBox = %+v, %v; want %+v", got, err, tc.want)
				}
				return
			}
			if !errors.Is(err, ErrBBox) {
				t.Fatalf("ParseBBox = %+v, %v; want ErrBBox", got, err)
			}
			// Errors never echo the input (GF-127): the message is the sentinel alone.
			if err.Error() != ErrBBox.Error() {
				t.Fatalf("error leaks input: %q", err)
			}
		})
	}
}

// TestParseBBoxProperty checks that random valid boxes parse to the same four
// numbers, and that random single mutations are rejected (GF-127).
func TestParseBBoxProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		minLng := rapid.Float64Range(-180, 179).Draw(rt, "minLng")
		maxLng := rapid.Float64Range(minLng, 180).Draw(rt, "maxLng")
		minLat := rapid.Float64Range(-90, 89).Draw(rt, "minLat")
		maxLat := rapid.Float64Range(minLat, 90).Draw(rt, "maxLat")
		if minLng == maxLng || minLat == maxLat {
			rt.Skip()
		}
		want := BBox{minLng, minLat, maxLng, maxLat}
		parts := []string{
			strconv.FormatFloat(minLng, 'f', -1, 64),
			strconv.FormatFloat(minLat, 'f', -1, 64),
			strconv.FormatFloat(maxLng, 'f', -1, 64),
			strconv.FormatFloat(maxLat, 'f', -1, 64),
		}
		valid := strings.Join(parts, ",")

		got, err := ParseBBox([]string{valid})
		if err != nil || got != want {
			rt.Fatalf("ParseBBox(%q) = %+v, %v; want %+v", valid, got, err, want)
		}

		var mutated string
		switch rapid.IntRange(0, 5).Draw(rt, "mutation") {
		case 0:
			mutated = valid + ",0"
		case 1:
			mutated = strings.Join(parts[:3], ",")
		case 2:
			mutated = strings.Replace(valid, parts[0], "NaN", 1)
		case 3:
			// Swapping min and max inverts the box, which is always rejected.
			mutated = strings.Join([]string{parts[2], parts[1], parts[0], parts[3]}, ",")
		case 4:
			mutated = " " + valid
		default:
			mutated = valid + ","
		}
		if _, err := ParseBBox([]string{mutated}); !errors.Is(err, ErrBBox) {
			rt.Fatalf("mutated %q accepted", mutated)
		}
	})
}

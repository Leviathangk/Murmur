package main

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/egoist/mygo"
)

func TestTrayIcons(t *testing.T) {
	for _, c := range []struct {
		name     string
		data     []byte
		size     int
		template bool
	}{
		// 18 points tall, as macOS shows it, on Retina; black, for macOS
		// to tint.
		{"macOS", trayMacOS, 36, true},
		{"Windows", trayWindows, 32, false},
	} {
		img, err := png.Decode(bytes.NewReader(c.data))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if b := img.Bounds(); b != image.Rect(0, 0, c.size, c.size) {
			t.Errorf("%s: bounds %v, want %d×%d", c.name, b, c.size, c.size)
		}
		if !c.template {
			continue
		}
		drawn := 0
		for y := range c.size {
			for x := range c.size {
				r, g, b, a := img.At(x, y).RGBA()
				if r|g|b != 0 {
					t.Fatalf("%s: (%d, %d) is not black", c.name, x, y)
				}
				if a > 0 {
					drawn++
				}
			}
		}
		if drawn == 0 {
			t.Errorf("%s: nothing drawn", c.name)
		}
	}
}

func TestTrayMenu(t *testing.T) {
	opened := false
	items := trayMenu(func() { opened = true })
	if items[0].Label != "打开 Murmur" {
		t.Fatalf("first item %q", items[0].Label)
	}
	items[0].Click(items[0], nil)
	if !opened {
		t.Error("the window did not open")
	}
	if last := items[len(items)-1]; last.Label != "退出 Murmur" || last.Role != mygo.RoleQuit {
		t.Errorf("last item %+v, want quit", last)
	}
}

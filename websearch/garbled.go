package websearch

import (
	"unicode"

	"github.com/Qsnh/Murmur/locales"
)

type GarbledError struct{}

func (GarbledError) Error() string { return locales.ReadGarbled() }

var blocks = []struct{ from, to rune }{
	{0x100, 0x24f},
	{0x250, 0x2ff},
	{0x300, 0x36f},
	{0x370, 0x3ff},
	{0x400, 0x52f},
	{0x530, 0x58f},
	{0x590, 0x5ff},
	{0x600, 0x6ff},
	{0x700, 0x74f},
	{0x750, 0x77f},
	{0x780, 0x7bf},
	{0x7c0, 0x7ff},
}

func block(r rune) int {
	switch r {
	case 'ʹ', 'ʺ', 'ʻ', 'ʼ', 'ʾ', 'ʿ', 'ˆ', 'ˇ', 'ˈ', 'ˉ', 'ˊ', 'ˋ', 'ˌ', '˘', '˙', '˚', '˛', '˜', '˝':
		return -1
	}
	for i, b := range blocks {
		if r >= b.from && r <= b.to {
			return i
		}
	}
	return -1
}

func Garbled(s string) bool {
	counts := make([]int, len(blocks))
	total, letters, mixed := 0, 0, 0
	run, mixing := -1, false
	prev := ' '
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
		}
		b := block(r)
		if b == 2 && unicode.IsLetter(prev) {
			b = -2
		}
		prev = r
		switch {
		case b == -2:
			continue
		case b < 0:
			run, mixing = -1, false
			continue
		}
		counts[b]++
		total++
		if run >= 0 && run != b && !mixing {
			mixed++
			mixing = true
		}
		run = b
	}
	if total < 3 || total*5 < letters || mixed == 0 {
		return false
	}
	scripts := 0
	for _, n := range counts {
		if n > 0 && n*20 >= total {
			scripts++
		}
	}
	return scripts >= 3 || scripts == 2 && mixed >= 2
}

package extract

import (
	"bytes"
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"

	"github.com/Qsnh/Murmur/locales"
)

func errBinary() error { return errors.New(locales.ExtractUnsupported()) }

// readText writes the text of data, a file of text: UTF-8, UTF-16 with its
// byte order mark, or else GB18030, as Windows in Chinese saves text and
// Excel saves CSV.
func readText(w *writer, data []byte) error {
	var enc encoding.Encoding
	switch {
	case bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}):
		data = data[3:]
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
		enc = unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM)
	case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		enc = unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM)
	case binary(data):
		return errBinary()
	case !utf8.Valid(data):
		enc = simplifiedchinese.GB18030
	}
	// Of UTF-8, what the writer takes, and a byte to tell it there is more.
	text := ""
	if enc == nil {
		text = string(data[:min(len(data), w.max+1)])
	} else {
		b, err := enc.NewDecoder().Bytes(data)
		if err != nil {
			return errBinary()
		}
		text = string(b)
		// Bytes GB18030 does not take come out as U+FFFD: many of them
		// mean the file was not in it.
		if enc == simplifiedchinese.GB18030 && strings.Count(text, string(utf8.RuneError))*50 > utf8.RuneCountInString(text) {
			return errors.New(locales.ExtractEncoding())
		}
	}
	w.WriteString(text)
	return nil
}

// binary reports whether data, not UTF-16, looks like no text: it has NUL
// bytes, or many control characters, near its start.
func binary(data []byte) bool {
	head := data[:min(len(data), 8000)]
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	n := 0
	for _, c := range head {
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' && c != '\f' && c != 0x1b {
			n++
		}
	}
	return n*10 > len(head)
}

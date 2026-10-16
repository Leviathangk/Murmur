// Package extract reads the text of documents, for a model that reads
// text: PDF, Word, Excel and PowerPoint files, and files of text, as notes,
// tables and code, in UTF-8, UTF-16 or GB18030.
package extract

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Qsnh/Murmur/locales"
)

// Kind is the kind of a document.
type Kind string

const (
	PDF        Kind = "pdf"
	Word       Kind = "docx"
	Excel      Kind = "xlsx"
	PowerPoint Kind = "pptx"
	Text       Kind = "text"
)

// TextExtensions are those of the files of text that a file dialog shows:
// others are read as text too, when they are.
var TextExtensions = []string{
	"txt", "md", "markdown", "csv", "tsv", "json", "jsonl", "xml", "yaml", "yml", "toml", "ini", "cfg", "conf", "log",
	"html", "htm", "css", "scss", "less", "js", "mjs", "cjs", "jsx", "ts", "tsx", "vue", "svelte",
	"py", "ipynb", "go", "java", "kt", "swift", "c", "h", "cc", "cpp", "hpp", "cs", "rs", "rb", "php", "lua",
	"sh", "bash", "zsh", "ps1", "bat", "sql", "r", "m", "scala", "dart", "pl", "tex", "rst", "srt", "vtt",
	"diff", "patch", "properties", "gradle", "proto", "graphql",
}

// Extensions are those of the documents a file dialog shows.
var Extensions = append([]string{"pdf", "docx", "xlsx", "pptx"}, TextExtensions...)

// unsupported says what to do with the files of extensions that look like
// documents but are not read.
var unsupported = map[string]func() string{
	"doc":     locales.ExtractOldWord,
	"wps":     locales.ExtractWPSWriter,
	"xls":     locales.ExtractOldExcel,
	"et":      locales.ExtractWPSSheet,
	"ppt":     locales.ExtractOldPPT,
	"dps":     locales.ExtractWPSSlides,
	"pages":   locales.ExtractPages,
	"numbers": locales.ExtractNumbers,
	"key":     locales.ExtractKeynote,
}

// KindOf returns the kind of the document named name, by its extension:
// Text for any but PDF and Office's.
func KindOf(name string) Kind {
	switch ext(name) {
	case "pdf":
		return PDF
	case "docx":
		return Word
	case "xlsx":
		return Excel
	case "pptx":
		return PowerPoint
	}
	return Text
}

func ext(name string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
}

// Read returns the text of the document named name, of data: its first
// max characters, and whether it had more.
func Read(name string, data []byte, max int) (text string, more bool, err error) {
	if hint, ok := unsupported[ext(name)]; ok {
		return "", false, errors.New(hint())
	}
	// The text is kept in bytes until it is cleaned: four a character
	// leaves room for any, and for the blank lines cleaning drops.
	w := &writer{max: max*4 + 1024}
	switch KindOf(name) {
	case PDF:
		err = readPDF(w, data)
	case Word:
		err = readDocx(w, data)
	case Excel:
		err = readXlsx(w, data)
	case PowerPoint:
		err = readPptx(w, data)
	default:
		err = readText(w, data)
	}
	if err != nil {
		return "", false, err
	}
	text = clean(w.b.String())
	if text == "" {
		if KindOf(name) == PDF {
			return "", false, errors.New(locales.ExtractScanned())
		}
		return "", false, errors.New(locales.ExtractEmpty())
	}
	more = w.cut
	if utf8.RuneCountInString(text) > max {
		text, more = strings.TrimRightFunc(string([]rune(text)[:max]), unicode.IsSpace), true
	}
	return text, more, nil
}

// writer holds the text of a document as it is read, up to max bytes:
// what comes after is left out, and reading may stop.
type writer struct {
	b   strings.Builder
	max int
	cut bool // text was left out
}

func (w *writer) WriteString(s string) {
	if w.full() {
		return
	}
	if room := w.max - w.b.Len(); len(s) > room {
		s, w.cut = s[:room], true
	}
	w.b.WriteString(s)
}

// full reports whether the writer takes no more text.
func (w *writer) full() bool {
	if w.b.Len() >= w.max {
		w.cut = true
		return true
	}
	return false
}

var blankLines = regexp.MustCompile(`\n{3,}`)

// clean returns text with its lines ending in \n, without trailing spaces
// nor control characters but tabs, and no more than one blank line
// between paragraphs.
func clean(text string) string {
	text = strings.ToValidUTF8(text, "")
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\u2028", "\n", "\u2029", "\n").Replace(text)
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || r == '\ufeff' {
			return -1
		}
		return r
	}, text)
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRightFunc(l, unicode.IsSpace)
	}
	// The first line keeps its indent, as code's does.
	return strings.Trim(blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"), "\n")
}

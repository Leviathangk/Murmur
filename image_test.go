package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/image/bmp"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

// picture returns a w×h image, red on the left half and blue on the
// right, of opaque pixels unless alpha is less than 255.
func picture(w, h int, alpha uint8) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.NRGBA{R: 220, B: 30, A: alpha}
			if x >= w/2 {
				c = color.NRGBA{R: 30, B: 220, A: alpha}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func pngOf(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// jpegOf encodes img as JPEG, with EXIF orientation o unless it is 0.
func jpegOf(t *testing.T, img image.Image, o int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if o == 0 {
		return b
	}
	// A little-endian TIFF of one IFD holding the orientation.
	tiff := []byte("II*\x00\x08\x00\x00\x00\x01\x00")
	tiff = binary.LittleEndian.AppendUint16(tiff, 0x0112)
	tiff = binary.LittleEndian.AppendUint16(tiff, 3)
	tiff = binary.LittleEndian.AppendUint32(tiff, 1)
	tiff = binary.LittleEndian.AppendUint16(tiff, uint16(o))
	tiff = append(tiff, 0, 0, 0, 0, 0, 0)
	seg := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xff, 0xe1}
	app1 = binary.BigEndian.AppendUint16(app1, uint16(len(seg)+2))
	app1 = append(app1, seg...)
	return slices.Concat(b[:2], app1, b[2:])
}

func TestNewImage(t *testing.T) {
	// Small enough images of the formats providers take are sent as they are.
	small := pngOf(t, picture(40, 30, 255))
	img, err := newImage(small)
	if err != nil || img.MIME != "image/png" || !bytes.Equal(img.data, small) || img.W != 40 || img.H != 30 || img.thumb == nil {
		t.Fatalf("small PNG: %+v, %v", img, err)
	}

	// Large ones are scaled down, photos as JPEG.
	img, err = newImage(jpegOf(t, picture(3000, 1000, 255), 0))
	if err != nil || img.MIME != "image/jpeg" || img.W != maxImageSide || img.H != 682 {
		t.Fatalf("large JPEG: %+v, %v", img, err)
	}
	if w, h := img.thumb.Size(); w != thumbSide || h != 199 {
		t.Errorf("thumbnail %d×%d", w, h)
	}
	// Transparent ones as PNG.
	img, err = newImage(pngOf(t, picture(2500, 100, 128)))
	if err != nil || img.MIME != "image/png" || img.W != maxImageSide {
		t.Fatalf("large transparent PNG: %+v, %v", img, err)
	}

	// A photo turned by its EXIF orientation is sent upright: 6 turns it a
	// quarter clockwise, so that the left half, red, is on top.
	if o := exifOrientation(jpegOf(t, picture(40, 20, 255), 6)); o != 6 {
		t.Fatalf("orientation %d", o)
	}
	img, err = newImage(jpegOf(t, picture(40, 20, 255), 6))
	if err != nil || img.MIME != "image/jpeg" || img.W != 20 || img.H != 40 {
		t.Fatalf("turned JPEG: %+v, %v", img, err)
	}
	pic, err := jpeg.Decode(bytes.NewReader(img.data))
	if err != nil {
		t.Fatal(err)
	}
	if r, _, b, _ := pic.At(10, 5).RGBA(); r < b {
		t.Errorf("the top is not red: %v", pic.At(10, 5))
	}

	// Formats that not every provider takes are converted.
	var buf bytes.Buffer
	bmp.Encode(&buf, picture(16, 16, 255))
	if img, err := newImage(buf.Bytes()); err != nil || img.MIME != "image/jpeg" {
		t.Errorf("BMP: %+v, %v", img, err)
	}
	if _, err := newImage([]byte("%PDF-1.7")); err == nil || !strings.Contains(err.Error(), "不是支持的图片格式") {
		t.Errorf("a PDF: %v", err)
	}
}

// pasteImage pastes the image of data into the composer of tt, as a
// screenshot is pasted.
func pasteImage(t *testing.T, tt *ui.Tester, data []byte) {
	t.Helper()
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.SetClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, data))))
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
}

func TestPasteImage(t *testing.T) {
	a, reqs := testApp(t)
	a.addModel(presets[0])
	tt := ui.NewTester(a.view, 1080, 720)
	data := pngOf(t, picture(320, 200, 255))
	pasteImage(t, tt, data)
	if len(a.pending) != 1 || a.draft != "" {
		t.Fatalf("%d images attached, draft %q", len(a.pending), a.draft)
	}
	snapshot(t, tt, "attached")

	// A text copied with a picture of it, as cells of a spreadsheet, is
	// pasted as text.
	tt.SetClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.Text, []byte("这是什么？")), transfer.Bytes(transfer.PNG, data))))
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if len(a.pending) != 1 || a.draft != "这是什么？" {
		t.Fatalf("%d images attached, draft %q", len(a.pending), a.draft)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	if len(*reqs) != 1 {
		t.Fatalf("%d requests", len(*reqs))
	}
	msg := (*reqs)[0].Messages[0]
	if msg.Content != "这是什么？" || len(msg.Images) != 1 || msg.Images[0].MIME != "image/png" || !bytes.Equal(msg.Images[0].Data, data) {
		t.Fatalf("message sent %q with %d images", msg.Content, len(msg.Images))
	}
	if len(a.pending) != 0 {
		t.Errorf("%d images still attached", len(a.pending))
	}
	tt.Frame()
	snapshot(t, tt, "image")

	// The image is kept in a file of its own, and loads again.
	sent := a.current.Messages[0].Images[0]
	if b, err := os.ReadFile(a.store.imagePath(sent.File)); err != nil || !bytes.Equal(b, data) {
		t.Fatalf("image file %q: %v", sent.File, err)
	}
	b := newApp(a.store)
	b.addModel(presets[0])
	loaded := b.chats[0].Messages[0].Images
	if len(loaded) != 1 || loaded[0].File != sent.File || loaded[0].MIME != "image/png" || loaded[0].W != 320 {
		t.Fatalf("loaded images %+v", loaded)
	}
	b.stream = a.stream
	tb := ui.NewTester(b.view, 1080, 720)
	b.open(b.chats[0].ID)
	tb.Frame()
	tb.Frame()
	if loaded[0].thumb == nil || loaded[0].missing {
		t.Errorf("thumbnail not loaded: %+v", loaded[0])
	}
	// And is sent again with the next message.
	b.send("再看看")
	if r := (*reqs)[1]; len(r.Messages) != 3 || len(r.Messages[0].Images) != 1 || !bytes.Equal(r.Messages[0].Images[0].Data, data) {
		t.Errorf("second request %+v", r.Messages)
	}

	// Clicked, it shows large, until clicked again.
	if err := tb.Click("查看图片"); err != nil {
		t.Fatal(err)
	}
	tb.Frame()
	if !b.viewOpen || b.viewing != loaded[0] || b.viewFull == nil {
		t.Fatalf("viewer open %v, image %v", b.viewOpen, b.viewFull)
	}
	snapshot(t, tb, "image-viewer")
	if err := tb.Click("关闭图片"); err != nil {
		t.Fatal(err)
	}
	tb.Frame()
	if b.viewOpen {
		t.Error("the viewer stays open")
	}
}

func TestAttachFiles(t *testing.T) {
	a, reqs := testApp(t)
	a.addModel(presets[1])
	dir := t.TempDir()
	small := filepath.Join(dir, "small.png")
	os.WriteFile(small, pngOf(t, picture(80, 60, 255)), 0o644)
	notes := filepath.Join(dir, "notes.txt")
	os.WriteFile(notes, []byte("hello"), 0o644)
	a.openImages = func() ([]string, error) { return []string{small, notes}, nil }

	tt := ui.NewTester(a.view, 1080, 720)
	if err := tt.Click("添加图片"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.pending) != 1 || a.pending[0].W != 80 || a.pending[0].H != 60 {
		t.Fatalf("attached %+v", a.pending)
	}
	if !tt.HasText("没有添加：notes.txt：不是支持的图片格式（PNG、JPEG、GIF、WebP、BMP）") {
		t.Errorf("no toast in %q", tt.Texts())
	}

	// Removed, it is not sent.
	if err := tt.Click("移除图片"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.pending) != 0 {
		t.Fatalf("%d images after removing", len(a.pending))
	}

	// A message may be images alone, which name the chat.
	a.attachFiles([]string{small, small})
	tt.Frame()
	if err := tt.Click("发送"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(*reqs) != 1 || len((*reqs)[0].Messages[0].Images) != 2 || (*reqs)[0].Messages[0].Content != "" {
		t.Fatalf("requests %+v", *reqs)
	}
	if a.current.Title != "图片" {
		t.Errorf("title %q", a.current.Title)
	}
	snapshot(t, tt, "images")

	// No more than maxImages a message.
	paths := make([]string, maxImages+2)
	for i := range paths {
		paths[i] = small
	}
	a.attachFiles(paths)
	tt.Frame()
	if len(a.pending) != maxImages || !tt.HasText("没有添加：一条消息最多 10 张图片") {
		t.Errorf("%d images attached, texts %q", len(a.pending), tt.Texts())
	}
	a.pending = nil

	// Deleting the chat deletes its images.
	files := []string{}
	for _, img := range a.current.Messages[0].Images {
		files = append(files, a.store.imagePath(img.File))
	}
	a.deleteChat(a.current.ID)
	for _, f := range files {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s left: %v", f, err)
		}
	}
}

// An image whose file is gone shows as lost, and is not sent.
func TestMissingImage(t *testing.T) {
	a, reqs := testApp(t)
	a.addModel(presets[0])
	a.attachData(pngOf(t, picture(64, 64, 255)))
	a.send("看图")
	os.Remove(a.store.imagePath(a.current.Messages[0].Images[0].File))

	b := newApp(a.store)
	b.addModel(presets[0])
	b.stream = a.stream
	tb := ui.NewTester(b.view, 1080, 720)
	b.open(b.chats[0].ID)
	tb.Frame()
	tb.Frame()
	if !b.chats[0].Messages[0].Images[0].missing {
		t.Fatal("the image is not missing")
	}
	if err := tb.Click("图片已丢失"); err != nil {
		t.Fatal(err)
	}
	tb.Frame()
	if b.viewOpen {
		t.Error("a lost image shows")
	}
	b.regenerate(b.current)
	if r := (*reqs)[1].Messages[0]; r.Content != "看图" || len(r.Images) != 0 {
		t.Errorf("sent %+v", r)
	}
}

// Image files copied in Finder or Explorer are attached as they paste.
func TestPasteImageFiles(t *testing.T) {
	a, _ := testApp(t)
	a.addModel(presets[0])
	dir := t.TempDir()
	photo := filepath.Join(dir, "a.png")
	os.WriteFile(photo, pngOf(t, picture(50, 50, 255)), 0o644)
	tt := ui.NewTester(a.view, 1080, 720)
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	data, err := transfer.FileData(photo)
	if err != nil {
		t.Fatal(err)
	}
	tt.SetClipboardData(data)
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if len(a.pending) != 1 || a.draft != "" {
		t.Errorf("%d images attached, draft %q", len(a.pending), a.draft)
	}
}

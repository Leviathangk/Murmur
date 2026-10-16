package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/egoist/mygo/ui"

	"github.com/Qsnh/Murmur/locales"
)

const (
	// maxImageSide is the longest side an image is sent at: the providers
	// scale larger ones down anyway, after taking their bytes.
	maxImageSide = 2048
	// maxImageBytes keeps an image well under Anthropic's 5 MB.
	maxImageBytes = 3 << 20
	// maxImagePixels refuses images that would take gigabytes to decode.
	maxImagePixels = 100 << 20
	// maxImageFile is the largest file read as an image.
	maxImageFile = 64 << 20
	// maxImages is how many images a message takes.
	maxImages = 10
	// thumbSide is the longest side of the copy an image shows from in the
	// chat, twice the most it shows at, for screens of two pixels a DIP.
	thumbSide = 600
)

// imageExtensions are the images the file picker shows.
var imageExtensions = []string{"png", "jpg", "jpeg", "gif", "webp", "bmp"}

// otherImageExtensions are those of images not sent, which are said to be
// so.
var otherImageExtensions = []string{"heic", "heif", "avif", "tif", "tiff", "svg", "ico"}

// mimeTypes are the formats image.Decode names that every provider takes,
// and their MIME types: others, as BMP, are sent as PNG or JPEG.
var mimeTypes = map[string]string{
	"png":  "image/png",
	"jpeg": "image/jpeg",
	"gif":  "image/gif",
	"webp": "image/webp",
}

// Image is a picture of a message, kept in a file of its own in the images
// directory rather than in chats.json, which is written whole on every
// change.
type Image struct {
	File string `json:"file,omitempty"` // its name in the images directory
	MIME string `json:"mime"`
	W    int    `json:"w,omitempty"` // its size in pixels, to lay it out before it loads
	H    int    `json:"h,omitempty"`

	data    []byte     // its bytes, until they are in File
	thumb   *ui.Bitmap // what shows in the chat, once decoded
	loading bool       // the thumbnail is being made
	missing bool       // its file could not be read
}

// imageSource is where an image's bytes are, to read them off the main
// thread.
type imageSource struct {
	mime string
	data []byte
	path string
}

func (s imageSource) read() ([]byte, error) {
	if s.data != nil {
		return s.data, nil
	}
	if s.path == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(s.path)
}

// source returns where the bytes of img are.
func (s store) source(img *Image) imageSource {
	src := imageSource{mime: img.MIME, data: img.data}
	if img.File != "" && s.dir != "" {
		src.path = s.imagePath(img.File)
	}
	return src
}

func (s store) imagePath(name string) string { return filepath.Join(s.dir, "images", name) }

// writeImage puts the bytes of img in a file of its own, readable by the
// user alone, as the chats are, and lets go of them: they are read from
// the file to send.
func (s store) writeImage(img *Image) error {
	if s.dir == "" || img.File != "" {
		return nil
	}
	name := newID() + "." + extension(img.MIME)
	if err := os.MkdirAll(filepath.Join(s.dir, "images"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(s.imagePath(name), img.data, 0o600); err != nil {
		return err
	}
	img.File, img.data = name, nil
	return nil
}

// extension returns the extension of a file of an image of the MIME type.
func extension(mime string) string {
	ext := strings.TrimPrefix(mime, "image/")
	if ext == "jpeg" {
		ext = "jpg"
	}
	return ext
}

// newImage makes an image of data fit to send: PNG, JPEG, GIF or WebP, no
// longer than maxImageSide nor larger than maxImageBytes, and upright, as
// a model may not read a photo's EXIF orientation. Others are drawn
// again, as JPEG, or PNG to keep their transparency. It also makes the
// thumbnail the composer shows.
func newImage(data []byte) (*Image, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New(locales.ImageUnsupported())
	}
	if cfg.Width*cfg.Height > maxImagePixels {
		return nil, errors.New(locales.ImageTooLarge(strconv.Itoa(cfg.Width), strconv.Itoa(cfg.Height)))
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New(locales.ImageDamaged())
	}
	orientation := 0
	if format == "jpeg" {
		orientation = exifOrientation(data)
	}
	mime := mimeTypes[format]
	if mime == "" || max(cfg.Width, cfg.Height) > maxImageSide || len(data) > maxImageBytes || orientation > 1 {
		pic := orient(scaleDown(img, maxImageSide), orientation)
		if mime, data, err = encode(pic); err != nil {
			return nil, err
		}
		img = pic
	}
	b := img.Bounds()
	return &Image{MIME: mime, W: b.Dx(), H: b.Dy(), data: data, thumb: ui.NewBitmap(scaleDown(img, thumbSide))}, nil
}

// encode encodes img as JPEG, or as PNG when it has transparency and that
// is small enough.
func encode(img *image.RGBA) (string, []byte, error) {
	var buf bytes.Buffer
	if !img.Opaque() {
		if err := png.Encode(&buf, img); err == nil && buf.Len() <= maxImageBytes {
			return "image/png", buf.Bytes(), nil
		}
		// Too large as PNG: on white, as a page shows it.
		flat := image.NewRGBA(img.Bounds())
		draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.Draw(flat, flat.Bounds(), img, img.Bounds().Min, draw.Over)
		img = flat
		buf.Reset()
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return "", nil, err
	}
	return "image/jpeg", buf.Bytes(), nil
}

// scaleDown returns img no longer than side, as RGBA.
func scaleDown(img image.Image, side int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if l := max(w, h); l > side {
		w, h = max(w*side/l, 1), max(h*side/l, 1)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	} else {
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	}
	return dst
}

// loadThumb makes the thumbnail of img, read from its file, off the main
// thread.
func (a *app) loadThumb(img *Image) {
	img.loading = true
	src := a.store.source(img)
	a.spawn(func() {
		var thumb *ui.Bitmap
		data, err := src.read()
		if err == nil {
			var pic image.Image
			if pic, _, err = image.Decode(bytes.NewReader(data)); err == nil {
				thumb = ui.NewBitmap(scaleDown(pic, thumbSide))
			}
		}
		a.update(func() {
			img.loading = false
			img.thumb, img.missing = thumb, err != nil
		})
	})
}

func readImage(path string) (*Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New(locales.FileCannotOpen())
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.IsDir() {
		return nil, errors.New(locales.FileNotImage())
	} else if st.Size() > maxImageFile {
		return nil, errors.New(locales.FileTooLarge())
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(f); err != nil {
		return nil, errors.New(locales.FileCannotRead())
	}
	return newImage(buf.Bytes())
}

// attachData adds the image of data, as a paste gives it, to the message
// being written.
func (a *app) attachData(data []byte) {
	a.attaching++
	a.spawn(func() {
		var imgs []*Image
		var errs []string
		if img, err := newImage(data); err != nil {
			errs = append(errs, err.Error())
		} else {
			imgs = append(imgs, img)
		}
		a.update(func() {
			a.attaching--
			a.attach(imgs, nil, errs)
		})
	})
}

// detach removes image i from the message being written.
func (a *app) detach(i int) {
	if i >= 0 && i < len(a.pending) {
		a.pending = slices.Delete(a.pending, i, i+1)
	}
}

// isImageFile reports whether the file at path is named as an image is,
// of a format sent or not.
func isImageFile(path string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	return slices.Contains(imageExtensions, ext) || slices.Contains(otherImageExtensions, ext)
}

// exifOrientation returns the EXIF orientation of a JPEG, 1 to 8, or 0
// without one, as MyGo's ui.DecodeBitmap reads it.
func exifOrientation(jpg []byte) int {
	if len(jpg) < 4 || jpg[0] != 0xff || jpg[1] != 0xd8 {
		return 0
	}
	for i := 2; i+4 <= len(jpg); {
		if jpg[i] != 0xff {
			return 0
		}
		marker, size := jpg[i+1], int(binary.BigEndian.Uint16(jpg[i+2:]))
		if marker == 0xda || size < 2 || i+2+size > len(jpg) { // the image data starts
			return 0
		}
		seg := jpg[i+4 : i+2+size]
		if marker == 0xe1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 0
}

// tiffOrientation reads the Orientation tag of the first IFD of TIFF data.
func tiffOrientation(t []byte) int {
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 0
	}
	n := int(bo.Uint16(t[ifd:]))
	for e := ifd + 2; e+12 <= len(t) && n > 0; e, n = e+12, n-1 {
		if bo.Uint16(t[e:]) == 0x0112 && bo.Uint16(t[e+2:]) == 3 { // Orientation, a SHORT
			if o := int(bo.Uint16(t[e+8:])); o >= 1 && o <= 8 {
				return o
			}
			return 0
		}
	}
	return 0
}

// orient turns and flips img as EXIF orientation o says it is to show.
func orient(img *image.RGBA, o int) *image.RGBA {
	if o <= 1 || o > 8 {
		return img
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			copy(out.Pix[out.PixOffset(x, y):][:4], img.Pix[img.PixOffset(sx, sy):])
		}
	}
	return out
}

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"

	"github.com/Qsnh/Murmur/extract"
	"github.com/Qsnh/Murmur/locales"
)

// attachFiles adds the images and documents in the files at paths to the
// message being written, read off the main thread, saying why those it
// cannot take are left out.
func (a *app) attachFiles(paths []string) {
	if len(paths) == 0 {
		return
	}
	a.attaching++
	a.spawn(func() {
		var imgs []*Image
		var docs []*Document
		var errs []string
		for _, p := range paths {
			if isImageFile(p) {
				img, err := readImage(p)
				if err != nil {
					errs = append(errs, locales.AttachFileError(filepath.Base(p), err.Error()))
					continue
				}
				imgs = append(imgs, img)
				continue
			}
			doc, err := readDocument(p)
			if err != nil {
				errs = append(errs, locales.AttachFileError(filepath.Base(p), err.Error()))
				continue
			}
			docs = append(docs, doc)
		}
		a.update(func() {
			a.attaching--
			a.attach(imgs, docs, errs)
		})
	})
}

// attach adds imgs and docs to the message being written, up to maxImages
// and maxDocuments, and says what was left out, and what was cut.
func (a *app) attach(imgs []*Image, docs []*Document, errs []string) {
	if room := maxImages - len(a.pending); len(imgs) > room {
		imgs = imgs[:max(room, 0)]
		errs = append(errs, locales.AttachTooManyImages(maxImages))
	}
	if room := maxDocuments - len(a.pendingDocs); len(docs) > room {
		docs = docs[:max(room, 0)]
		errs = append(errs, locales.AttachTooManyDocuments(maxDocuments))
	}
	for _, doc := range docs {
		doc.key = a.newKey()
	}
	a.pending = append(a.pending, imgs...)
	a.pendingDocs = append(a.pendingDocs, docs...)
	var said []string
	if len(errs) > 0 {
		said = append(said, locales.AttachSkipped(strings.Join(errs, locales.AttachSkippedSeparator())))
	}
	for _, doc := range docs {
		if doc.Cut {
			said = append(said, locales.AttachTooLong(doc.Name, characters(maxDocumentChars)))
		}
	}
	if len(said) > 0 {
		a.toast = strings.Join(said, locales.ToastSeparator())
	}
}

// pickFiles asks for images and documents to attach, in the system's
// dialog.
func (a *app) pickFiles() {
	a.spawn(func() {
		paths, err := a.openFiles()
		a.update(func() {
			if err != nil {
				a.toast = locales.OpenDialogFailed(err.Error())
				return
			}
			a.attachFiles(paths)
		})
	})
}

// openFilesDialog shows the system's dialog to choose images and
// documents; it blocks until the dialog closes.
func (a *app) openFilesDialog() ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      a.win,
		Title:       locales.OpenDialogTitle(),
		ButtonLabel: locales.OpenDialogButton(),
		Filters: []mygo.FileFilter{
			{Name: locales.OpenDialogImagesAndFiles(), Extensions: slices.Concat(imageExtensions, extract.Extensions)},
			{Name: locales.OpenDialogImages(), Extensions: imageExtensions},
			{Name: locales.OpenDialogFiles(), Extensions: extract.Extensions},
		},
		Multiple: true,
	})
}

// removeFiles deletes the files of the images and documents of c but those
// of kept, as a chat that went on from c shares them.
func (s store) removeFiles(c *Chat, kept []*Chat) {
	if s.dir == "" {
		return
	}
	images, docs := map[string]bool{}, map[string]bool{}
	for _, k := range kept {
		for _, m := range k.Messages {
			for _, img := range m.Images {
				images[img.File] = true
			}
			for _, doc := range m.Documents {
				docs[doc.File] = true
			}
		}
	}
	for _, m := range c.Messages {
		for _, img := range m.Images {
			if img.File != "" && !images[img.File] {
				os.Remove(s.imagePath(img.File))
			}
		}
		for _, doc := range m.Documents {
			if doc.File != "" && !docs[doc.File] {
				os.Remove(s.documentPath(doc.File))
			}
		}
	}
}

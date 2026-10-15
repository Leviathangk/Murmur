package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"

	"github.com/Qsnh/Murmur/extract"
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
					errs = append(errs, filepath.Base(p)+"："+err.Error())
					continue
				}
				imgs = append(imgs, img)
				continue
			}
			doc, err := readDocument(p)
			if err != nil {
				errs = append(errs, filepath.Base(p)+"："+err.Error())
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
		errs = append(errs, fmt.Sprintf("一条消息最多 %d 张图片", maxImages))
	}
	if room := maxDocuments - len(a.pendingDocs); len(docs) > room {
		docs = docs[:max(room, 0)]
		errs = append(errs, fmt.Sprintf("一条消息最多 %d 个文件", maxDocuments))
	}
	for _, doc := range docs {
		doc.key = a.newKey()
	}
	a.pending = append(a.pending, imgs...)
	a.pendingDocs = append(a.pendingDocs, docs...)
	var said []string
	if len(errs) > 0 {
		said = append(said, "没有添加："+strings.Join(errs, "；"))
	}
	for _, doc := range docs {
		if doc.Cut {
			said = append(said, "「"+doc.Name+"」太长，只发送开头的 "+characters(maxDocumentChars))
		}
	}
	if len(said) > 0 {
		a.toast = strings.Join(said, "。")
	}
}

// pickFiles asks for images and documents to attach, in the system's
// dialog.
func (a *app) pickFiles() {
	a.spawn(func() {
		paths, err := a.openFiles()
		a.update(func() {
			if err != nil {
				a.toast = "无法打开文件选择框：" + err.Error()
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
		Title:       "选择图片或文件",
		ButtonLabel: "添加",
		Filters: []mygo.FileFilter{
			{Name: "图片和文件", Extensions: slices.Concat(imageExtensions, extract.Extensions)},
			{Name: "图片", Extensions: imageExtensions},
			{Name: "文件", Extensions: extract.Extensions},
		},
		Multiple: true,
	})
}

// removeFiles deletes the files of the images and documents of c.
func (s store) removeFiles(c *Chat) {
	if s.dir == "" {
		return
	}
	for _, m := range c.Messages {
		for _, img := range m.Images {
			if img.File != "" {
				os.Remove(s.imagePath(img.File))
			}
		}
		for _, doc := range m.Documents {
			if doc.File != "" {
				os.Remove(s.documentPath(doc.File))
			}
		}
	}
}

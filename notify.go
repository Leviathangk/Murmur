package main

import (
	"errors"
	"log"
	"strings"

	"github.com/egoist/mygo"
)

// A reply that comes while the window is not in front, closed or behind
// another app's, tells of itself in a system notification: the chat's
// title, the model, and how the reply starts. A click shows the chat.

// replyNotification is the notification telling of reply, the last
// message of c. Its ID is the chat's, which a click opens, and a newer
// reply of the chat replaces it.
func replyNotification(c *Chat, reply *Message) mygo.NotificationOptions {
	body := preview(reply.Content)
	switch {
	case reply.Error != "":
		body = "回复失败：" + preview(reply.Error)
	case body == "":
		body = "回复完成"
	}
	return mygo.NotificationOptions{ID: c.ID, Title: c.Title, Subtitle: reply.Model, Body: body}
}

// preview returns how text starts, on one line, without the marks of
// bold, code, headings and quotes that a reply's Markdown shows as such.
func preview(text string) string {
	const most = 100
	lines := strings.Split(strings.NewReplacer("**", "", "`", "").Replace(text), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimLeft(strings.TrimSpace(l), "#> ")
	}
	s := strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
	if r := []rune(s); len(r) > most {
		s = string(r[:most]) + "…"
	}
	return s
}

// notifyDesktop shows n, unless the window is in front, where the reply
// shows already.
func (a *app) notifyDesktop(n mygo.NotificationOptions) {
	if a.win == nil || a.win.IsFocused() {
		return
	}
	// Show waits for the user to allow notifications, the first time on
	// macOS.
	go func() {
		if err := mygo.NewNotification(n).Show(); err != nil && !errors.Is(err, mygo.ErrNotificationsDenied) {
			log.Printf("no notification: %v", err)
		}
	}()
}

// openNotified shows the chat a notification clicked told of, at its end,
// where the reply is. The click that launched the app (macOS) comes before
// the window, which then opens on the chat.
func (a *app) openNotified(id string) {
	a.update(func() {
		a.open(id)
		if a.current != nil {
			a.current.list.ScrollToEnd()
		}
	})
	if a.win != nil {
		a.showWindow()
	}
}

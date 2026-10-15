//go:build demo

package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"

	"github.com/Qsnh/Murmur/llm"
)

// The demo build sends a prompt to the model at MURMUR_DEMO_URL, keeping
// its data in a temporary directory, captures the window while the model
// thinks and once it has answered into MURMUR_DEMO_OUT, and quits:
//
//	MURMUR_DEMO_URL=http://127.0.0.1:8899/v1 MURMUR_DEMO_OUT=/tmp go run -tags demo .
func init() {
	onReady = func(a *app) {
		out := os.Getenv("MURMUR_DEMO_OUT")
		dir, _ := os.MkdirTemp("", "murmur-demo")
		a.store = store{dir: dir}
		a.chats, a.current = nil, nil
		a.settings = &Settings{
			Models:   []*Model{{ID: "demo", Name: "DeepSeek（模拟）", Protocol: llm.OpenAI, BaseURL: os.Getenv("MURMUR_DEMO_URL"), Model: "deepseek-chat", ThinkStyle: llm.ThinkType}},
			Current:  "demo",
			Thinking: true,
		}
		capture := func(name string) {
			png, err := a.win.CapturePage()
			if err != nil {
				log.Printf("capture %s: %v", name, err)
				return
			}
			os.WriteFile(filepath.Join(out, name), png, 0o644)
			log.Printf("captured %s", name)
		}
		busy := func() bool {
			ch := make(chan bool)
			a.win.Update(func() { ch <- a.current != nil && a.current.busy() })
			return <-ch
		}
		go func() {
			time.Sleep(time.Second)
			capture("demo-welcome.png")
			a.win.Update(func() { a.send("用 Go 写一个并发的例子，再对比一下几种同步方式") })
			time.Sleep(2500 * time.Millisecond)
			capture("demo-thinking.png")
			for busy() {
				time.Sleep(200 * time.Millisecond)
			}
			time.Sleep(1500 * time.Millisecond) // the last words fade in
			capture("demo-answer.png")
			a.win.Update(func() {
				a.current.Messages[1].thinkOpen = true
				a.collapsed = true
			})
			time.Sleep(800 * time.Millisecond)
			capture("demo-expanded.png")
			a.win.Update(func() { a.collapsed = false; a.openSettings() })
			time.Sleep(800 * time.Millisecond)
			capture("demo-settings.png")
			b, _ := os.ReadFile(filepath.Join(dir, "chats.json"))
			log.Printf("chats.json: %s", b)
			os.RemoveAll(dir)
			a.win.Update(func() { mygo.App.Quit() })
		}()
	}
}

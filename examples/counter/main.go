// SPDX-License-Identifier: GPL-2.0-or-later

// Command counter shows a slider, a button and two labels. A goroutine updates
// one label every second through bui.Do; the button updates the other.
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	bui "github.com/CWBudde/bui-go"
)

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	quitAfter := flag.Duration("quit-after", 0, "quit after this duration (0: run until closed)")
	flag.Parse()

	root := bui.NewNode("frame")
	must(root.SetNum("padding", 8))

	clock := bui.NewNode("label")
	must(clock.SetStr("text", "Running for 0 s"))
	root.Add(clock)
	clock.Pack(bui.PackOptions{Fill: bui.FillX})

	size := bui.NewNode("slider")
	must(size.SetStr("label", "Step"))
	must(size.SetNum("min", 1))
	must(size.SetNum("max", 10))
	must(size.SetNum("value", 1))
	root.Add(size)
	size.Pack(bui.PackOptions{Fill: bui.FillX, PadY0: 6})

	count := bui.NewNode("label")
	must(count.SetStr("text", "Count: 0"))
	root.Add(count)
	count.Pack(bui.PackOptions{Fill: bui.FillX, PadY0: 6})

	button := bui.NewNode("button")
	must(button.SetStr("text", "Add step"))
	root.Add(button)
	button.Pack(bui.PackOptions{Fill: bui.FillX, PadY0: 6})

	total := 0
	button.OnClick(func() {
		total += int(size.Num("value"))
		must(count.SetStr("text", fmt.Sprintf("Count: %d", total)))
		fmt.Println("count:", total)
	})
	size.OnChange(func(v float64) { fmt.Println("step:", v) })

	win, err := bui.NewWindow(root, "BUI from Go", 320, 160)
	if err != nil {
		log.Fatal(err)
	}
	win.Show()

	stop := make(chan struct{})
	go func() {
		start := time.Now()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				text := fmt.Sprintf("Running for %.0f s", time.Since(start).Seconds())
				bui.Do(func() { must(clock.SetStr("text", text)) })
			}
		}
	}()

	if *quitAfter > 0 {
		time.AfterFunc(*quitAfter, func() { bui.Do(bui.Quit) })
	}

	must(bui.Run())

	// A tick can still queue a Do after this, but only Run drains the queue, and
	// it has returned: nothing touches the labels once they are closed.
	close(stop)
	win.Close()
	root.Close()
	fmt.Println("clean shutdown")
}

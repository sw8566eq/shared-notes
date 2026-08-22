// Command linkshr-desktop opens the linkshr web UI in a native window —
// no browser chrome, no URL bar, its own entry in your app launcher. It's
// a thin client: everything it does is point an OS-native webview at a
// running linkshr server (see the root `linkshr` command for that). The
// UI itself — templates, JS, CSS — is unchanged; this just changes how
// you open it.
package main

import (
	"flag"

	"linkshr/internal/webview"
)

func main() {
	server := flag.String("server", "http://localhost:8080", "linkshr server URL to open (use the host machine's LAN address from other machines, e.g. http://192.168.1.5:8080)")
	width := flag.Int("width", 480, "initial window width")
	height := flag.Int("height", 800, "initial window height")
	flag.Parse()

	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("linkshr")
	w.SetSize(*width, *height, webview.HintNone)
	w.Navigate(*server)
	w.Run()
}

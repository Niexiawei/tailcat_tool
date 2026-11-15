// Command tailcat-manager is the desktop GUI of Tailcat Manager.
package main

import (
	"log"

	"github.com/Niexiawei/tailcat-tool/internal/app"
	"github.com/egoist/mygo"
)

func main() {
	mygo.App.WhenReady(func() {
		app.OpenMainWindow()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

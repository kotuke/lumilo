// Lumilo is a macOS menu-bar panel for local LLM engines.
package main

import (
	"log"

	"github.com/egoist/mygo"
)

func main() {
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	setupLog()
	a := &app{}
	// No SetName: the name from mygo.json keeps dev and installed data apart.
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.WhenReady(a.ready)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

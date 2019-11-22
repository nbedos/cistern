package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/text"
)

func TestController_resize(t *testing.T) {
	t.Run("resize to (0, 0) should not cause any error", func(t *testing.T) {
		newScreen := func() (tcell.Screen, error) {
			return tcell.NewSimulationScreen(""), nil
		}
		tui, err := NewTUI(newScreen, tcell.StyleDefault, text.StyleSheet{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			tui.Finish()
		}()
		c := cache.NewCache(nil, nil)
		controller, err := NewController(&tui, (&c).BuildsByCommit(), time.UTC, "", "")
		if err != nil {
			t.Fatal(err)
		}

		// Must not panic
		controller.resize(0, 0)
		controller.refresh()
		controller.draw()
	})

	t.Run("status bar should keep a height of 1", func(t *testing.T) {
		newScreen := func() (tcell.Screen, error) {
			return tcell.NewSimulationScreen(""), nil
		}
		tui, err := NewTUI(newScreen, tcell.StyleDefault, text.StyleSheet{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			tui.Finish()
		}()
		c := cache.NewCache(nil, nil)
		controller, err := NewController(&tui, (&c).BuildsByCommit(), time.UTC, "", "")
		if err != nil {
			t.Fatal(err)
		}

		for _, height := range []int{1, 2, 3, 10, 100, 10, 3, 2, 1} {
			controller.resize(80, height)
			if _, statusHeight := controller.status.Size(); statusHeight != 1 {
				t.Fatalf("expected statusHeight == %d but got %d", 1, statusHeight)
			}
		}
	})
}

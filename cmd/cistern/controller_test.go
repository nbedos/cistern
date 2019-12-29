package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gdamore/tcell"
	"github.com/nbedos/cistern/cache"
	"github.com/nbedos/cistern/tui"
)

func TestController_resize(t *testing.T) {
	t.Run("resize to (0, 0) should not cause any error", func(t *testing.T) {
		newScreen := func() (tcell.Screen, error) {
			return tcell.NewSimulationScreen(""), nil
		}
		ui, err := tui.NewTUI(newScreen, tcell.StyleDefault, tui.StyleSheet{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			ui.Finish()
		}()
		c := cache.NewCache(nil, nil)
		conf := tui.ColumnConfiguration{}
		controller, err := NewController(&ui, conf, "", c, time.UTC, "", "")
		if err != nil {
			t.Fatal(err)
		}

		// Must not panic
		controller.resize(0, 0)
		controller.refresh()
		controller.draw()
	})
}


func TestRunApplication(t *testing.T) {
	var newScreen = func() (tcell.Screen, error) {
		return tcell.NewSimulationScreen(""), nil
	}

	t.Run("no provider should cause the function to return with an error", func(t *testing.T) {
		ctx := context.Background()
		pwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		err = RunApplication(ctx, newScreen, pwd, "HEAD", nil, nil, time.UTC, "")
		if err != ErrNoProvider {
			t.Fatalf("expected %v but got %v", ErrNoProvider, err)
		}
	})
}
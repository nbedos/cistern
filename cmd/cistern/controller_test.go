package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gdamore/tcell"
	"github.com/nbedos/cistern/providers"
	"github.com/nbedos/cistern/tui"
)

func setup() (Controller, func(), error) {
	newScreen := func() (tcell.Screen, error) {
		return tcell.NewSimulationScreen(""), nil
	}
	ui, err := tui.NewTUI(newScreen, tcell.StyleDefault)
	if err != nil {
		return Controller{}, nil, err
	}
	defer func() {

	}()
	c := providers.NewCache(nil, nil)
	conf := ControllerConfiguration{
		GitStyle: providers.GitStyle{
			Location: time.UTC,
		},
	}
	controller, err := NewController(&ui, conf, "", c, "")
	if err != nil {
		ui.Finish()
		return Controller{}, nil, err
	}

	return controller, func() { ui.Finish() }, nil
}

func TestController_resize(t *testing.T) {
	t.Run("resize to (0, 0) should not cause any error", func(t *testing.T) {
		controller, teardown, err := setup()
		if err != nil {
			t.Fatal(err)
		}
		defer teardown()

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

	t.Run("the absence of providers should cause the function to return ErrNoProvider", func(t *testing.T) {
		ctx := context.Background()
		pwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		err = RunApplication(ctx, newScreen, pwd, "HEAD", Configuration{})
		if err != providers.ErrNoProvider {
			t.Fatalf("expected %v but got %v", providers.ErrNoProvider, err)
		}
	})
}


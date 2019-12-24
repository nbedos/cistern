package tui

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gdamore/tcell"
	"github.com/nbedos/cistern/text"
)

var newScreen = func() (tcell.Screen, error) {
	return tcell.NewSimulationScreen(""), nil
}

func TestNewTUI(t *testing.T) {
	tui, err := NewTUI(newScreen, tcell.StyleDefault, text.StyleSheet{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		tui.Finish()
	}()
}

func TestTUI_Draw(t *testing.T) {
	tui, err := NewTUI(newScreen, tcell.StyleDefault, text.StyleSheet{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		tui.Finish()
	}()

	width, height := 80, 20
	tui.screen.(tcell.SimulationScreen).SetSize(width, height)
	s := text.LocalizedStyledString{
		X: 5,
		Y: 5,
		S: text.NewStyledString("a"),
	}

	tui.Draw(s)

	r, _, _, _ := tui.screen.(tcell.SimulationScreen).GetContent(5, 5)
	if expectedRune := []rune(s.S.String())[0]; r != expectedRune {
		t.Fatalf("invalid cell content: expected %v but got '%v'", expectedRune, r)
	}
}

func TestTUI_Exec(t *testing.T) {
	t.Run("invalid command should return an error", func(t *testing.T) {
		tui, err := NewTUI(newScreen, tcell.StyleDefault, text.StyleSheet{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			tui.Finish()
		}()

		// An empty name will cause a failure when the command is run
		if err = tui.Exec(context.Background(), "", nil, nil); err == nil {
			t.Fatal("expected error but got nil")
		}

		// tui.screen must remain usable after call to Exec()
		x, y, testRune := 0, 0, 'a'
		tui.screen.SetContent(x, y, testRune, nil, tcell.StyleDefault)
		if r, _, _, _ := tui.screen.GetContent(x, y); r != testRune {
			t.Fatalf("expected %v but got %v", testRune, r)
		}
	})

	t.Run("Execute command without stream", func(t *testing.T) {
		tui, err := NewTUI(newScreen, tcell.StyleDefault, text.StyleSheet{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			tui.Finish()
		}()

		if err = tui.Exec(context.Background(), "more", nil, nil); err != nil {
			t.Fatalf("expected nil but got %v", err)
		}

		// tui.screen must remain usable after call to Exec()
		x, y, testRune := 0, 0, 'a'
		tui.screen.SetContent(x, y, testRune, nil, tcell.StyleDefault)
		if r, _, _, _ := tui.screen.GetContent(x, y); r != testRune {
			t.Fatalf("expected %v but got %v", testRune, r)
		}
	})

	t.Run("Exec must return when the context is cancelled", func(t *testing.T) {
		tui, err := NewTUI(newScreen, tcell.StyleDefault, text.StyleSheet{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			tui.Finish()
		}()

		d := 5 * time.Second
		ctx, cancel := context.WithCancel(context.Background())
		errc := make(chan error)
		start := time.Now()
		go func() {
			errc <- tui.Exec(ctx, "sleep", []string{strconv.Itoa(int(d.Seconds()))}, nil)
		}()
		cancel()
		if err := <-errc; err == nil {
			t.Fatalf("expected error != %v but got %v", nil, err)
		}
		if elapsed := time.Since(start); elapsed >= d {
			t.Fatalf("tui.Exec call did not return in time: time elapsed %v exceeds delay %v",
				elapsed, d)
		}
	})
}

func TestRunApplication(t *testing.T) {
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

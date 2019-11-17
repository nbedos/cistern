package tui

import (
	"context"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/gdamore/tcell"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/text"
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

		err = tui.Exec(context.Background(), ExecCmd{
			// An empty name will cause a failure when the command is run
			name: "",
		})
		if err == nil {
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

		err = tui.Exec(context.Background(), ExecCmd{
			name: "date",
		})
		if err != nil {
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
			errc <- tui.Exec(ctx, ExecCmd{
				name: "sleep",
				args: []string{strconv.Itoa(int(d.Seconds()))},
			})
		}()
		cancel()
		if err := <-errc; err != context.Canceled {
			t.Fatalf("expected error %v but got %v", context.Canceled, err)
		}
		if elapsed := time.Since(start); elapsed >= d {
			t.Fatalf("tui.Exec call did not return in time: time elapsed %v exceeds delay %v",
				elapsed, d)
		}
	})
}

type mockProvider struct {
	id string
}

func (p mockProvider) AccountID() string { return p.id }
func (p mockProvider) Builds(ctx context.Context, repositoryURL string, limit int, buildc chan<- cache.Build) error {
	return nil
}
func (p mockProvider) Log(ctx context.Context, repository cache.Repository, jobID int) (string, error) {
	return "", nil
}
func (p mockProvider) StreamLog(ctx context.Context, repositoryID int, jobID int, writer io.Writer) error {
	return nil
}

func TestRunApplication(t *testing.T) {
	t.Run("no provider should cause the function to return with an error", func(t *testing.T) {
		ctx := context.Background()
		err := RunApplication(ctx, newScreen, "https://example.com/owner/project", nil)
		if err != ErrNoProvider {
			t.Fatalf("expected %v but got %v", ErrNoProvider, err)
		}
	})

	t.Run("application must keep running once provider returns", func(t *testing.T) {
		// Those providers do nothing. When their Builds() method is called it will return nil
		// immediately
		ps := []cache.Provider{
			mockProvider{id: "provider1"},
			mockProvider{id: "provider2"},
			mockProvider{id: "provider3"},
			mockProvider{id: "provider4"},
		}

		d := 100 * time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		errc := make(chan error)
		start := time.Now()
		var elapsed time.Duration // Only use this after reading error on errc
		go func() {
			e := RunApplication(ctx, newScreen, "https://example.com/owner/project", ps)
			elapsed = time.Since(start)
			errc <- e
		}()
		// Simulate user interacting with application for duration d after provider.Builds() returns
		time.Sleep(d)
		// Simulate user quitting application
		cancel()
		err := <-errc
		if elapsed < d {
			t.Fatalf("expected application to run for more than %v but it ran for %v", d, elapsed)
		}
		if err != context.Canceled {
			t.Fatalf("expected %v but got %v", context.Canceled, err)
		}
	})
}

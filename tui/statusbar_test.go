package tui

import "testing"

func TestStatusBar_Write(t *testing.T) {
	t.Run("status bar of height 0", func(t *testing.T) {
		s, err := NewStatusBar(80, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range []string{"a", "b", "c"} {
			s.Write(message)
			if len(s.outputBuffer) != 0 {
				t.Fatalf("expected len(s.outputBuffer) == %d but got %d", 0, len(s.outputBuffer))
			}
		}
	})

	t.Run("status bar of height 1", func(t *testing.T) {
		s, err := NewStatusBar(80, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range []string{"a", "b", "c"} {
			s.Write(message)
			if s.outputBuffer[0] != message {
				t.Fatalf("expected %q but got %q", message, s.outputBuffer[0])
			}
		}
	})
}

func TestStatusBar_Text(t *testing.T) {
	t.Run("Text of output buffer", func(t *testing.T) {
		s, err := NewStatusBar(80, 1)
		if err != nil {
			t.Fatal(err)
		}
		message := "output"
		s.Write(message)

		texts := s.Text()
		if len(texts) != 1 {
			t.Fatalf("expected len(texts) == %d but got %d", 1, len(texts))
		}

		if output := texts[0].S.String(); output != message {
			t.Fatalf("expected %q but got %q", message, output)
		}
	})

	t.Run("Text of input buffer", func(t *testing.T) {
		s, err := NewStatusBar(80, 1)
		if err != nil {
			t.Fatal(err)
		}
		message := "input"
		s.InputBuffer = message
		s.ShowInput = true

		texts := s.Text()
		if len(texts) != 1 {
			t.Fatalf("expected len(texts) == %d but got %d", 1, len(texts))
		}

		if input := texts[0].S.String(); input != s.inputPrefix+message {
			t.Fatalf("expected %q but got %q", message, input)
		}
	})
}

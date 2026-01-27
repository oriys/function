package docker

import "testing"

func TestExtractJSONFromStdout(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, ok := extractJSONFromStdout(nil)
		if !ok {
			t.Fatalf("ok=false, want true")
		}
		if got != nil {
			t.Fatalf("got=%q, want nil", string(got))
		}
	})

	t.Run("valid json", func(t *testing.T) {
		got, ok := extractJSONFromStdout([]byte("  {\"a\":1}\n"))
		if !ok {
			t.Fatalf("ok=false, want true")
		}
		if string(got) != "{\"a\":1}" {
			t.Fatalf("got=%q, want %q", string(got), "{\"a\":1}")
		}
	})

	t.Run("logs then json", func(t *testing.T) {
		got, ok := extractJSONFromStdout([]byte("hello\n{\"a\":1}\n"))
		if !ok {
			t.Fatalf("ok=false, want true")
		}
		if string(got) != "{\"a\":1}" {
			t.Fatalf("got=%q, want %q", string(got), "{\"a\":1}")
		}
	})

	t.Run("only logs", func(t *testing.T) {
		_, ok := extractJSONFromStdout([]byte("hello\nworld\n"))
		if ok {
			t.Fatalf("ok=true, want false")
		}
	})

	t.Run("returns copy", func(t *testing.T) {
		in := []byte("{\"a\":1}")
		got, ok := extractJSONFromStdout(in)
		if !ok {
			t.Fatalf("ok=false, want true")
		}
		in[0] = 'X'
		if string(got) != "{\"a\":1}" {
			t.Fatalf("got=%q, want %q", string(got), "{\"a\":1}")
		}
	})
}

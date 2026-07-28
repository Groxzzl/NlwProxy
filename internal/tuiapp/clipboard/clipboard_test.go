package clipboard

import "testing"

func TestWriterFunc(t *testing.T) {
	got := ""
	w := WriterFunc(func(s string) error { got = s; return nil })
	if err := w.WriteText("ready"); err != nil || got != "ready" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
func TestClipEXECommandError(t *testing.T) {
	if err := (ClipEXE{Command: "definitely-not-a-command.exe"}).WriteText("x"); err == nil {
		t.Fatal("expected command error")
	}
}

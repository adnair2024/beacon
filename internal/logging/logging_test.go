package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritesAndRotates(t *testing.T) {
	dir := t.TempDir()
	log, c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	log.Info("hello", "session_id", "abc")
	c.Close()
	b, _ := os.ReadFile(filepath.Join(dir, File))
	if !strings.Contains(string(b), "session_id=abc") {
		t.Fatal(string(b))
	}
	os.WriteFile(filepath.Join(dir, File), make([]byte, MaxSize+1), 0o600)
	_, c, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if _, err := os.Stat(filepath.Join(dir, File+".1")); err != nil {
		t.Fatal("oversized log should rotate")
	}
	if fi, _ := os.Stat(filepath.Join(dir, File)); fi.Size() > 100 {
		t.Fatal("new log should start small")
	}
}

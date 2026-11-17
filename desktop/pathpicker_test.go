package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPickerStartDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	keys := filepath.Join(home, ".ssh")
	if err := os.Mkdir(keys, 0700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(keys, "test key")
	if err := os.WriteFile(key, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{"codex", ""},
		{"relative/path", ""},
		{"~", home},
		{" ~/.ssh/test key ", keys},
		{keys, keys},
		{key, keys},
		{filepath.Join(keys, "missing", "nested"), keys},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := pickerStartDirectory(tc.input); got != tc.want {
				t.Errorf("pickerStartDirectory(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

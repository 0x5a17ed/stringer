package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/golden"
)

func TestGolden(t *testing.T) {
	tt := []struct {
		name        string
		trimPrefix  string
		lineComment bool
		bitFlags    bool
	}{
		{name: "day", bitFlags: true},
		{name: "gap", bitFlags: true},
		{name: "zero", bitFlags: true},
		{name: "compound", bitFlags: true},
		{name: "multirun", bitFlags: true},
		{name: "trimmed", bitFlags: true, trimPrefix: "Trimmed"},
	}

	dir := t.TempDir()
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			input := golden.Get(t, tc.name+".inp.go")

			absFile := filepath.Join(dir, tc.name+".go")
			err := os.WriteFile(absFile, input, 0644)
			if err != nil {
				t.Fatal(err)
			}

			g := Generator{}
			if err := g.parsePackage([]string{absFile}, nil); err != nil {
				t.Fatal(err)
			}
			if g.pkgs == nil {
				t.Fatalf("got 0 parsed packages but expected 1")
			}

			// Extract the name and type of the constant from the first line.
			tokens := strings.Fields(string(input))
			if len(tokens) < 4 {
				t.Fatalf("%s: need type declaration after package declaration", tc.name)
			}

			g.parsePackage([]string{absFile}, nil)
			g.generateStart(tc.bitFlags)
			k := Enum
			if tc.bitFlags {
				k = Flag
			}
			g.generate(tokens[3], k, tc.trimPrefix, tc.lineComment)
			got := string(g.format())

			golden.Assert(t, got, tc.name+".out.go")
		})
	}
}

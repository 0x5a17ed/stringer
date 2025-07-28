package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

			g := Generator{
				bitFlags: tc.bitFlags,
			}
			g.parsePackage([]string{absFile}, nil)
			if g.pkg == nil {
				t.Fatalf("got 0 parsed packages but expected 1")
			}

			// Extract the name and type of the constant from the first line.
			tokens := strings.Fields(string(input))
			if len(tokens) < 4 {
				t.Fatalf("%s: need type declaration after package declaration", tc.name)
			}

			g.parsePackage([]string{absFile}, nil)
			g.generateStart()
			g.generate(tokens[3])
			got := string(g.format())

			assert.Equal(t, string(golden.Get(t, tc.name+".out.go")), got)
		})
	}
}

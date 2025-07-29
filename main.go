// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Stringer is a tool to automate the creation of methods that satisfy the fmt.Stringer
// interface. Given the name of a (signed or unsigned) integer type T that has constants
// defined, stringer will create a new self-contained Go source file implementing
//
//	func (t T) String() string
//
// The file is created in the same package and directory as the package that defines T.
// It has helpful defaults designed for use with go generate.
//
// Stringer works best with constants that are consecutive values such as created using iota,
// but creates good code regardless. In the future it might also provide custom support for
// constant sets that are bit patterns.
//
// For example, given this snippet,
//
//	package painkiller
//
//	type Pill int
//
//	const (
//		Placebo Pill = iota
//		Aspirin
//		Ibuprofen
//		Paracetamol
//		Acetaminophen = Paracetamol
//	)
//
// running this command
//
//	stringer -type=Pill
//
// in the same directory will create the file pill_string.go, in package painkiller,
// containing a definition of
//
//	func (Pill) String() string
//
// That method will translate the value of a Pill constant to the string representation
// of the respective constant name, so that the call fmt.Print(painkiller.Aspirin) will
// print the string "Aspirin".
//
// Typically this process would be run using go generate, like this:
//
//	//go:generate stringer -type=Pill
//
// If multiple constants have the same value, the lexically first matching name will
// be used (in the example, Acetaminophen will print as "Paracetamol").
//
// With no arguments, it processes the package in the current directory.
// Otherwise, the arguments must name a single directory holding a Go package
// or a set of Go source files that represent a single Go package.
//
// The -type flag accepts a comma-separated list of types so a single run can
// generate methods for multiple types. The default output file is t_string.go,
// where t is the lower-cased name of the first type listed. It can be overridden
// with the -output flag.
//
// The -lineComment flag tells stringer to generate the text of any line comment, trimmed
// of leading spaces, instead of the constant name. For instance, if the constants above had a
// Pill prefix, one could write
//
//	PillAspirin // Aspirin
//
// to suppress it in the output.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

type typeOptions struct {
	kind        Kind
	name        string
	trimPrefix  string
	lineComment bool
}

func parseOption(kind Kind, inp string) (*typeOptions, error) {
	name, options, _ := strings.Cut(inp, "=")

	out := &typeOptions{
		kind: kind,
		name: name,
	}

	if options != "" {
		for _, opt := range strings.Split(options, ";") {
			k, v, _ := strings.Cut(opt, ":")

			switch k {
			case "lineComment":
				out.lineComment = true
			case "trimPrefix":
				out.trimPrefix = v
			case "trimType":
				out.trimPrefix = name
			default:
				return nil, fmt.Errorf("unknown option %q", k)
			}
		}

	}

	return out, nil
}

var (
	output      = flag.String("output", "", "output file name; default srcdir/<type>_string.go")
	trimPrefix  = flag.Bool("trimPrefix", false, "trim the type name `prefix` from the generated constant names")
	lineComment = flag.Bool("lineComment", false, "use line comment text as printed text when present")
	buildTags   = flag.String("tags", "", "comma-separated list of build tags to apply")

	enumTypesStrFlag = flag.String("enums", "", "comma-separated list of enum types")
	flagTypesStrFlag = flag.String("flags", "", "comma-separated list of flag types")
)

// Usage is a replacement usage function for the flags package.
func Usage() {
	_, _ = fmt.Fprintf(os.Stderr, `usage:
	stringer [flags] [directory]
	stringer [flags] files...

flags:
`)
	flag.PrintDefaults()
}

func run() error {
	log.SetFlags(0)
	log.SetPrefix("stringer: ")
	flag.Usage = Usage
	flag.Parse()

	outputName := *output
	if outputName == "" {
		flag.Usage()
		os.Exit(2)
	}

	var hasFlags bool
	var types []typeOptions
	for _, inp := range strings.Split(*enumTypesStrFlag, ",") {
		typeOpt, err := parseOption(Enum, inp)
		if err != nil {
			return err
		}
		types = append(types, *typeOpt)
	}
	for _, inp := range strings.Split(*flagTypesStrFlag, ",") {
		typeOpt, err := parseOption(Flag, inp)
		if err != nil {
			return err
		}
		types = append(types, *typeOpt)

		hasFlags = true
	}

	if len(types) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	var tags []string
	if len(*buildTags) > 0 {
		tags = strings.Split(*buildTags, ",")
	}

	// We accept either one directory or a list of files. Which do we have?
	args := flag.Args()
	if len(args) == 0 {
		// Default: process whole package in current directory.
		args = []string{"."}
	}

	// Parse the package once.
	g := Generator{}
	if err := g.parsePackage(args, tags); err != nil {
		log.Fatal(err)
	}

	// Print the header and package clause.
	g.generateStart(hasFlags)
	for _, typeOpt := range types {
		g.generate(typeOpt.name, typeOpt.kind, typeOpt.trimPrefix, typeOpt.lineComment)
	}

	// Format the output.
	src := g.format()

	// Write to the given output file.
	err := os.WriteFile(outputName, src, 0644)
	if err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	fmt.Fprintf(os.Stderr, "wrote output to %s\n", outputName)

	return nil
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(2)
	}
}

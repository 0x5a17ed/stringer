package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/token"
	"go/types"
	"log"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

type Kind int

const (
	Flag Kind = iota
	Enum
)

// isPow2 returns true of v is a power-of-two value.
func isPow2(x uint64) bool {
	return (x & (x - 1)) == 0
}

// usize returns the number of bits of the smallest unsigned integer
// type that will hold n. Used to create the smallest possible slice of
// integers to use as indexes into the concatenated strings.
func usize(n int) int {
	switch {
	case n < 1<<8:
		return 8
	case n < 1<<16:
		return 16
	default:
		// 2^32 is enough constants for anyone.
		return 32
	}
}

// File holds a single parsed file and associated data.
type File struct {
	pkg  *Package  // Package to which this file belongs.
	file *ast.File // Parsed AST.

	// These fields are reset for each type being generated.
	kind     Kind   // Type of the constant type, either enum or flag.
	typeName string // Name of the constant type we're currently looking for.

	values      []Value // Accumulator for constant values of that type.
	trimPrefix  string  // prefix to be trimmed from value names.
	lineComment bool    // use line comment as flag name.
}

type Package struct {
	name  string
	fset  *token.FileSet
	defs  map[*ast.Ident]types.Object
	files []*File
	dir   string
}

// Generator holds the state of the analysis. Primarily used to buffer
// the output for format.Source.
type Generator struct {
	buf  bytes.Buffer // Accumulated output.
	pkgs []*Package
}

func (g *Generator) Printf(format string, args ...interface{}) {
	fmt.Fprintf(&g.buf, format, args...)
}

// parsePackage analyzes the single package constructed from the patterns and tags.
// parsePackage exits if there is an error.
func (g *Generator) parsePackage(patterns []string, tags []string) error {
	cfg := &packages.Config{
		Mode: packages.NeedSyntax | packages.NeedTypesInfo |
			packages.NeedTypes | packages.NeedTypesSizes |
			packages.NeedImports | packages.NeedName |
			packages.NeedFiles | packages.NeedCompiledGoFiles,
		// TODO: Need to think about constants in test files. Maybe write type_string_test.go
		// in a separate pass? For later.
		Tests:      false,
		BuildFlags: []string{fmt.Sprintf("-tags=%s", strings.Join(tags, " "))},
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return err
	}

	if len(pkgs) == 0 {
		log.Fatalf("error: no packages matching %v", strings.Join(patterns, " "))
	}

	out := make([]*Package, len(pkgs))
	for i, pkg := range pkgs {

		p := &Package{
			name:  pkg.Name,
			dir:   pkg.Dir,
			fset:  pkg.Fset,
			defs:  pkg.TypesInfo.Defs,
			files: make([]*File, len(pkg.Syntax)),
		}

		for j, file := range pkg.Syntax {
			p.files[j] = &File{
				file: file,
				pkg:  p,
			}
		}

		out[i] = p
	}
	g.pkgs = out

	return nil
}

// generateStart produces the start of a Go source code file.
func (g *Generator) generateStart(usesFlags bool) {
	g.Printf("package %s", g.pkgs[0].name)
	g.Printf("\n")
	g.Printf("import (\n")

	if usesFlags {
		g.Printf("	\"math/bits\"\n")
	}
	g.Printf("	\"strconv\"\n")
	g.Printf("	\"strings\"\n")
	g.Printf(")\n\n")
}

// generate produces the String method for the named type.
func (g *Generator) generate(typeName string, kind Kind, trimPrefix string, lineComment bool) {
	values := make([]Value, 0, 100)

	for _, pkg := range g.pkgs {
		for _, file := range pkg.files {
			// Set the state for this run of the walker.
			file.values = nil

			file.kind = kind
			file.typeName = typeName
			file.trimPrefix = trimPrefix
			file.lineComment = lineComment
			if file.file != nil {
				ast.Inspect(file.file, file.genDecl)
				values = append(values, file.values...)
			}
		}
	}

	if len(values) == 0 {
		log.Fatalf("no values defined for type %s", typeName)
	}

	// Generate code that will fail if the constants change value.
	g.Printf("func _() {\n")
	g.Printf("\t// An \"invalid array index\" compiler error signifies that the constant values have changed.\n")
	g.Printf("\t// Re-run the stringer command to generate them again.\n")
	g.Printf("\tvar x [1]struct{}\n")
	for _, v := range values {
		g.Printf("\t_ = x[%s - %s]\n", v.originalName, v.str)
	}
	g.Printf("}\n")
	runs := splitIntoRuns(values, kind)

	switch {
	case len(runs) == 1 && kind == Enum:
		g.buildOneRun(runs, typeName)
	case len(runs) <= 8:
		if kind == Flag {
			g.buildFlagsMultipleRuns(typeName, runs)
			g.buildFlagStringMethod(typeName)
		} else {
			g.buildMultipleRuns(runs, typeName)
		}
	default:
		g.buildMap(typeName, kind, runs)
		g.buildFlagStringMethod(typeName)
	}
}

// splitIntoRuns breaks the values into runs of contiguous sequences.
// For example, given 1,2,3,5,6,7 it returns {1,2,3},{5,6,7}.
// The input slice is known to be non-empty.
func splitIntoRuns(values []Value, kind Kind) [][]Value {
	// We use stable sort so the lexically first name is chosen for equal elements.
	sort.Stable(byValue(values))
	// Remove duplicates. Stable sort has put the one we want to print first,
	// so use that one. The String method won't care about which named constant
	// was the argument, so the first name for the given value is the only one to keep.
	// We need to do this because identical values would cause the switch or map
	// to fail to compile.
	j := 1
	for i := 1; i < len(values); i++ {
		if values[i].value != values[i-1].value {
			values[j] = values[i]
			j++
		}
	}
	values = values[:j]
	runs := make([][]Value, 0, 10)
	for len(values) > 0 {
		// One contiguous sequence per outer loop.
		i := 1
		if kind == Flag {
			for i < len(values) && (values[i-1].value == 0 || values[i].value == values[i-1].value<<1) {
				i++
			}
		} else {
			for i < len(values) && values[i].value == values[i-1].value+1 {
				i++
			}
		}
		runs = append(runs, values[:i])
		values = values[i:]
	}
	return runs
}

// format returns the gofmt-ed contents of the Generator's buffer.
func (g *Generator) format() []byte {
	src, err := format.Source(g.buf.Bytes())
	if err != nil {
		// Should never happen, but can arise when developing this code.
		// The user can compile the output to see the error.
		log.Printf("warning: internal error: invalid Go generated: %s", err)
		log.Printf("warning: compile the package to analyze the error")
		return g.buf.Bytes()
	}
	return src
}

// Value represents a declared constant.
type Value struct {
	originalName string // The name of the constant.
	name         string // The name with trimmed prefix.
	// The value is stored as a bit pattern alone. The boolean tells us
	// whether to interpret it as an int64 or a uint64; the only place
	// this matters is when sorting.
	// Much of the time the str field is all we need; it is printed
	// by Value.String.
	value  uint64 // Will be converted to int64 when needed.
	signed bool   // Whether the constant is a signed type.
	str    string // The string representation given by the "go/constant" package.
}

func (v *Value) String() string {
	return v.str
}

// byValue lets us sort the constants into increasing order.
// We take care in the Less method to sort in signed or unsigned order,
// as appropriate.
type byValue []Value

func (b byValue) Len() int      { return len(b) }
func (b byValue) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b byValue) Less(i, j int) bool {
	if b[i].signed {
		return int64(b[i].value) < int64(b[j].value)
	}
	return b[i].value < b[j].value
}

// genDecl processes one declaration clause.
func (f *File) genDecl(node ast.Node) bool {
	decl, ok := node.(*ast.GenDecl)
	if !ok || decl.Tok != token.CONST {
		// We only care about const declarations.
		return true
	}
	// The name of the type of the constants we are declaring.
	// Can change if this is a multi-element declaration.
	typ := ""
	// Loop over the elements of the declaration. Each element is a ValueSpec:
	// a list of names possibly followed by a type, possibly followed by values.
	// If the type and value are both missing, we carry down the type (and value,
	// but the "go/types" package takes care of that).
	for _, spec := range decl.Specs {
		vspec := spec.(*ast.ValueSpec) // Guaranteed to succeed as this is CONST.
		if vspec.Type == nil && len(vspec.Values) > 0 {
			// "X = 1". With no type but a value. If the constant is untyped,
			// skip this vspec and reset the remembered type.
			typ = ""

			// If this is a simple type conversion, remember the type.
			// We don't mind if this is actually a call; a qualified call won't
			// be matched (that will be SelectorExpr, not Ident), and only unusual
			// situations will result in a function call that appears to be
			// a type conversion.
			ce, ok := vspec.Values[0].(*ast.CallExpr)
			if !ok {
				continue
			}
			id, ok := ce.Fun.(*ast.Ident)
			if !ok {
				continue
			}
			typ = id.Name
		}
		if vspec.Type != nil {
			// "X T". We have a type. Remember it.
			ident, ok := vspec.Type.(*ast.Ident)
			if !ok {
				continue
			}
			typ = ident.Name
		}
		if typ != f.typeName {
			// This is not the type we're looking for.
			continue
		}
		// We now have a list of names (from one line of source code) all being
		// declared with the desired type.
		// Grab their names and actual values and store them in f.values.
		for _, name := range vspec.Names {
			if name.Name == "_" {
				continue
			}
			// This dance lets the type checker find the values for us. It's a
			// bit tricky: look up the object declared by the name, find its
			// types.Const, and extract its value.
			obj, ok := f.pkg.defs[name]
			if !ok {
				log.Fatalf("no value for constant %s", name)
			}
			info := obj.Type().Underlying().(*types.Basic).Info()
			if info&types.IsInteger == 0 {
				log.Fatalf("can't handle non-integer constant type %s", typ)
			}
			value := obj.(*types.Const).Val() // Guaranteed to succeed as this is CONST.
			if value.Kind() != constant.Int {
				log.Fatalf("can't happen: constant is not an integer %s", name)
			}
			i64, isInt := constant.Int64Val(value)
			u64, isUint := constant.Uint64Val(value)
			if !isInt && !isUint {
				log.Fatalf("internal error: value of %s is not an integer: %s", name, value.String())
			}
			if !isInt {
				u64 = uint64(i64)
			}
			if !isPow2(u64) && f.kind == Flag {
				continue
			}
			v := Value{
				originalName: name.Name,
				value:        u64,
				signed:       info&types.IsUnsigned == 0,
				str:          value.String(),
			}
			if c := vspec.Comment; f.lineComment && c != nil && len(c.List) == 1 {
				v.name = strings.TrimSpace(c.Text())
			} else {
				v.name = strings.TrimPrefix(v.originalName, f.trimPrefix)
			}
			f.values = append(f.values, v)
		}
	}
	return false
}

// declareIndexAndNameVars declares the index slices and concatenated names
// strings representing the runs of values.
func (g *Generator) declareIndexAndNameVars(runs [][]Value, typeName string) {
	var indexes, names []string
	for i, run := range runs {
		index, name := g.createIndexAndNameDecl(run, typeName, fmt.Sprintf("_%d", i))
		if len(run) != 1 {
			indexes = append(indexes, index)
		}
		names = append(names, name)
	}
	g.Printf("const (\n")
	for _, name := range names {
		g.Printf("\t%s\n", name)
	}
	g.Printf(")\n\n")

	if len(indexes) > 0 {
		g.Printf("var (")
		for _, index := range indexes {
			g.Printf("\t%s\n", index)
		}
		g.Printf(")\n\n")
	}
}

// declareIndexAndNameVar is the single-run version of declareIndexAndNameVars
func (g *Generator) declareIndexAndNameVar(run []Value, typeName string) {
	index, name := g.createIndexAndNameDecl(run, typeName, "")
	g.Printf("const %s\n", name)
	g.Printf("var %s\n", index)
}

// createIndexAndNameDecl returns the pair of declarations for the run. The caller will add "const" and "var".
func (g *Generator) createIndexAndNameDecl(run []Value, typeName string, suffix string) (string, string) {
	b := new(bytes.Buffer)
	indexes := make([]int, len(run))
	for i := range run {
		b.WriteString(run[i].name)
		indexes[i] = b.Len()
	}
	nameConst := fmt.Sprintf("_%s_name%s = %q", typeName, suffix, b.String())
	nameLen := b.Len()
	b.Reset()
	fmt.Fprintf(b, "_%s_index%s = [...]uint%d{0, ", typeName, suffix, usize(nameLen))
	for i, v := range indexes {
		if i > 0 {
			fmt.Fprintf(b, ", ")
		}
		fmt.Fprintf(b, "%d", v)
	}
	fmt.Fprintf(b, "}")
	return b.String(), nameConst
}

// declareNameVars declares the concatenated names string representing all the values in the runs.
func (g *Generator) declareNameVars(runs [][]Value, typeName string, suffix string) {
	g.Printf("const _%s_name%s = \"", typeName, suffix)
	for _, run := range runs {
		for i := range run {
			g.Printf("%s", run[i].name)
		}
	}
	g.Printf("\"\n")
}

// Arguments to format are:
//
//	[1]: type name
//	[2]: size of index element (8 for uint8 etc.)
//	[3]: less than zero check (for signed types)
const stringOneRun = `func (i %[1]s) String() string {
	if %[3]si >= %[1]s(len(_%[1]s_index)-1) {
		return "%[1]s(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _%[1]s_name[_%[1]s_index[i]:_%[1]s_index[i+1]]
}
`

// Arguments to format are:
//
//	[1]: type name
//	[2]: lowest defined value for type, as a string
//	[3]: size of index element (8 for uint8 etc.)
//	[4]: less than zero check (for signed types)
const stringOneRunWithOffset = `func (i %[1]s) String() string {
	i -= %[2]s
	if %[4]si >= %[1]s(len(_%[1]s_index)-1) {
		return "%[1]s(" + strconv.FormatInt(int64(i + %[2]s), 10) + ")"
	}
	return _%[1]s_name[_%[1]s_index[i] : _%[1]s_index[i+1]]
}
`

// buildOneRun generates the variables and String method for a single run of contiguous values.
func (g *Generator) buildOneRun(runs [][]Value, typeName string) {
	values := runs[0]
	g.Printf("\n")
	g.declareIndexAndNameVar(values, typeName)
	// The generated code is simple enough to write as a Printf format.
	lessThanZero := ""
	if values[0].signed {
		lessThanZero = "i < 0 || "
	}

	if values[0].value == 0 { // Signed or unsigned, 0 is still 0.
		g.Printf(stringOneRun, typeName, usize(len(values)), lessThanZero)
	} else {
		g.Printf(stringOneRunWithOffset, typeName, values[0].String(), usize(len(values)), lessThanZero)
	}
}

// buildFlagsMultipleRuns generates the variables and String method for multiple runs of contiguous values.
// For this pattern, a single Printf format won't do.
func (g *Generator) buildFlagsMultipleRuns(typeName string, runs [][]Value) {
	g.Printf("\n")
	g.declareIndexAndNameVars(runs, typeName)

	g.buildFlagActiveFlagsMethodStart(typeName, runs)

	for i, values := range runs {
		if len(values) == 1 {
			if values[0].value == 0 {
				continue
			}

			g.Printf("if i&%s != 0 {\n", &values[0])
			g.Printf("	i, s = i &^ %s, append(s, _%s_name_%d)\n", &values[0], typeName, i)
			g.Printf("}\n")

			continue
		}

		for j, v := range values {
			if v.value == 0 {
				continue
			}

			g.Printf("if i&%s != 0 {\n", &v)
			g.Printf("	i, s = i&^ %[5]s, append(s, _%[1]s_name_%[2]d[_%[1]s_index_%[2]d[%[3]d]:_%[1]s_index_%[2]d[%[4]d]])\n",
				typeName, i, j, j+1, &v)
			g.Printf("}\n")
		}
	}

	g.buildFlagActiveFlagsMethodEnd(typeName)
}

func (g *Generator) buildFlagStringMethod(typeName string) {
	g.Printf("\n")
	g.Printf("func (i %s) String() string {\n", typeName)
	g.Printf("	return strings.Join(i.ActiveFlags(), \"+\")\n")
	g.Printf("}\n")
}

func (g *Generator) buildFlagActiveFlagsMethodStart(typeName string, runs [][]Value) {
	g.Printf("func (i %s) ActiveFlags() []string {\n", typeName)

	// Check if any of the runs contains a zero value and return it.
outer:
	for i, values := range runs {
		// Shortcut for single value runs that contains the 0 value.
		if len(values) == 1 && values[0].value == 0 {
			g.Printf("if i == 0 {\n")
			g.Printf("	return []string{_%s_name_%d}\n", typeName, i)
			g.Printf("}\n\n")
			break
		}

		for j, v := range values {
			if v.value == 0 {
				g.Printf("if i == 0 {\n")
				g.Printf("return []string{_%[1]s_name_%[2]d[_%[1]s_index_%[2]d[%[3]d]:_%[1]s_index_%[2]d[%[4]d]]}\n",
					typeName,
					i,
					j,
					j+1)
				g.Printf("}\n\n")

				break outer
			}
		}
	}

	g.Printf("s := make([]string, 0, bits.OnesCount64(uint64(i)))\n")
}

const stringActiveFlagsEnd = `
	if i != 0 {
		s = append(s, "%[1]s(" + strconv.FormatInt(int64(i), 10) + ")")
	}
	return s
}
`

func (g *Generator) buildFlagActiveFlagsMethodEnd(typeName string) {
	g.Printf(stringActiveFlagsEnd[1:], typeName)
}

// buildMultipleRuns generates the variables and String method for multiple runs of contiguous values.
// For this pattern, a single Printf format won't do.
func (g *Generator) buildMultipleRuns(runs [][]Value, typeName string) {
	g.Printf("\n")
	g.declareIndexAndNameVars(runs, typeName)
	g.Printf("func (i %s) String() string {\n", typeName)
	g.Printf("\tswitch {\n")
	for i, values := range runs {
		if len(values) == 1 {
			g.Printf("\tcase i == %s:\n", &values[0])
			g.Printf("\t\treturn _%s_name_%d\n", typeName, i)
			continue
		}
		if values[0].value == 0 && !values[0].signed {
			// For an unsigned lower bound of 0, "0 <= i" would be redundant.
			g.Printf("\tcase i <= %s:\n", &values[len(values)-1])
		} else {
			g.Printf("\tcase %s <= i && i <= %s:\n", &values[0], &values[len(values)-1])
		}
		if values[0].value != 0 {
			g.Printf("\t\ti -= %s\n", &values[0])
		}
		g.Printf("\t\treturn _%s_name_%d[_%s_index_%d[i]:_%s_index_%d[i+1]]\n",
			typeName, i, typeName, i, typeName, i)
	}
	g.Printf("\tdefault:\n")
	g.Printf("\t\treturn \"%s(\" + strconv.FormatInt(int64(i), 10) + \")\"\n", typeName)
	g.Printf("\t}\n")
	g.Printf("}\n")
}

// Argument to format is the type name.
const stringMap = `func (i %[1]s) String() string {
	if str, ok := _%[1]s_map[i]; ok {
		return str
	}
	return "%[1]s(" + strconv.FormatInt(int64(i), 10) + ")"
}
`

// Argument to format is the type name.
const stringFlagMap = `for k, v := range _%[1]s_map {
		if k&i != 0 {
			i, s = i&^k, append(s, v)
		}
	}
`

// buildMap handles the case where the space is so sparse a map is a reasonable fallback.
// It's a rare situation but has simple code.
func (g *Generator) buildMap(typeName string, kind Kind, runs [][]Value) {
	g.Printf("\n")
	g.declareNameVars(runs, typeName, "")

	// Generate the flag to name mapping.
	g.Printf("\nvar _%s_map = map[%s]string{\n", typeName, typeName)
	n := 0
	for _, values := range runs {
		for _, value := range values {
			g.Printf("\t%s: _%s_name[%d:%d],\n", &value, typeName, n, n+len(value.name))
			n += len(value.name)
		}
	}
	g.Printf("}\n\n")

	if kind == Flag {
		g.buildFlagActiveFlagsMethodStart(typeName, runs)
		g.Printf(stringFlagMap, typeName)
		g.buildFlagActiveFlagsMethodEnd(typeName)
	} else {
		g.Printf(stringMap, typeName)
	}
}

func (g *Generator) findTypeDeclarationFile(typeName string) (string, error) {
	for _, pkg := range g.pkgs {
		for ident, obj := range pkg.defs {
			if ident.Name == typeName {
				if _, ok := obj.(*types.TypeName); ok {
					pos := obj.Pos()
					position := pkg.fset.Position(pos)
					return position.Filename, nil
				}
			}
		}
	}

	return "", fmt.Errorf("type %q not found in loaded packages", typeName)
}

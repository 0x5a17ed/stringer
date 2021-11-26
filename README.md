## stringer

This program is a drop-in replacement for Go's commonly used [stringer][1] tool.
In addition to generating `String() string` implementations for individual
constants, this version also works with bit flag sets.

[1]: https://golang.org/x/tools/cmd/stringer

For instance:

```go
    type T uint

    const (
        Foo T = 1 << iota
        Bar
        Baz
    )
```

When invoking `Foo.String()`, we should get `"Foo"`.
But when invoking `(Foo|Bar).String()` the old stringer tool will only print a numeric value: `"T(3)"`. In our case, we will get the expected: `"Foo, Bar"`.
Unknown values in a bit flag set will still be presented in the `"T(3)"` form.

```go
    (T(1<<12) | Baz).String() == "Baz, T(4096)"
```


## Usage

    $ go get -u github.com/hexaflex/stringer

In the go source file, use a `go:generate` statement to have the tool generate
the required code. For the existing behaviour of Go's stringer tool, use:

    //go:generate stringer -type=MyType

In order to treat a type as a bit flag set, use:

    //go:generate stringer -flags -type=MyType


## License

Copyright 2014 The Go Authors. All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.

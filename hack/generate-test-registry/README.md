# generate-test-registry

This tool automatically generates test registration code from Go test functions.

## Usage

```bash
go run generate-test-registry/main.go [flags]
```

### Flags

- `-input` - Directory to scan for .go files (default: ".")
- `-output` - Output file for generated test registry (default: "generated_tests.go")
- `-package` - Package name (defaults to directory name)
- `-prefix` - Prefix for test names (e.g. "mypackage/")

### Example

```bash
go run generate-test-registry/main.go \
  -input ./test/mypackage \
  -output ./test/mypackage/generated_tests.go \
  -package mypackage \
  -prefix mypackage/
```

Or use with `go generate`:

```go
//go:generate go run ../../hack/generate-test-registry/main.go -input . -output generated_tests.go -package mypackage -prefix mypackage/
```

Then run:

```bash
go generate ./test/mypackage
```

## How It Works

1. Scans all `.go` files in the input directory (skips `generated_tests.go`)
2. Finds functions matching: `func Test*(t gt.TestContext)` or `func Test*(t gotest.TestContext)`
3. Generates a `Tests` slice with all discovered test functions
4. Sets default lifecycle to `Blocking` and empty labels

## Customizing Generated Tests

After generation, you can manually edit the generated file to:
- Add labels
- Change lifecycle to `Informing`
- Add descriptions

Or create a separate file that wraps/modifies the generated tests before passing to BuildExtensionTestSpecsFromGoTests().


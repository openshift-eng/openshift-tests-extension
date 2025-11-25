# Go Test Example

This directory demonstrates how to use the Go test wrapper with automatic test discovery via `go generate`.

## Files

- `basic.go` - Test functions (using `gt.TestContext`)
- `generated_tests.go` - Auto-generated test registry (DO NOT EDIT MANUALLY)

## Adding New Tests

1. Add test functions to `basic.go` or create new `.go` files:

```go
func TestMyNewFeature(t gt.TestContext) {
    // Test implementation
}
```

2. Regenerate the test registry:

```bash
go generate
```

3. Rebuild the binary:

```bash
make gotest-example
```

## Customizing Tests

After running `go generate`, you can manually edit `generated_tests.go` to:

- Add labels: `Labels: []string{"sig-testing", "slow"}`
- Change lifecycle: `Lifecycle: ext.LifecycleInforming`
- Add descriptions: `Description: "Tests my feature"`

**Note**: Manual edits will be overwritten when you run `go generate` again.

For permanent customizations, consider creating a separate file that modifies the `Tests` slice before passing it to `BuildExtensionTestSpecsFromGoTests()`.


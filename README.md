# openshift-tests-extension

openshift-tests-extension is a framework that allows external
repositories to contribute tests to openshift-tests' suites with
extension binaries. It provides a standardized interface for test
discovery, execution, and result aggregation, allowing decentralized
test contributions while maintaining centralized orchestration.

It is part of the implementation for [this
enhancement](https://github.com/openshift/enhancements/pull/1676).

## Test Framework Wrappers

openshift-tests-extension provides wrappers for multiple test frameworks:

- **Ginkgo**: For BDD-style tests using the [OpenShift fork of Ginkgo v2](https://github.com/openshift/onsi-ginkgo)
  - **Note**: You must use the OpenShift fork via the replace directive in go.mod
  - See [pkg/ginkgo](pkg/ginkgo) for the wrapper implementation
  - See [cmd/example-tests](cmd/example-tests/main.go) for usage example
  
- **Go Test**: For standard Go tests using the `testing` package
  - See [pkg/gotest](pkg/gotest) for the wrapper implementation and documentation
  - See [cmd/gotest-example](cmd/gotest-example/main.go) for usage example
  
- **Cypress**: For JavaScript/UI tests using [Cypress](https://www.cypress.io/)
  - See [pkg/cypress](pkg/cypress) for the wrapper implementation

## Getting Started

See [cmd/example-tests](cmd/example-tests/main.go) for an example of
how to integrate this with your project using Ginkgo. Note that when using
Ginkgo, you must include the replace directive in your go.mod:

```go
replace github.com/onsi/ginkgo/v2 => github.com/openshift/onsi-ginkgo/v2 v2.6.1-0.20241205171354-8006f302fd12
```

See [cmd/gotest-example](cmd/gotest-example/main.go) for an example of
using standard Go tests (no special dependencies required).

![Sequence diagram](docs/sequence.png)

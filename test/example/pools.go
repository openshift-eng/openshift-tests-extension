package example

import (
	"context"
	"fmt"
	"time"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"
)

// PoolTestSpecs returns manually-built test specs that exercise resource pool scheduling.
// Specs are labeled "POOL-TEST" for suite qualification. The mix includes:
//   - 8 worker tests demanding 1 unit each of pool "workers"
//   - 2 exclusive tests demanding 2 units each (consuming full capacity)
//   - 3 non-pool tests with no resource demands (should run unconstrained)
func PoolTestSpecs() et.ExtensionTestSpecs {
	var specs et.ExtensionTestSpecs

	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("[sig-testing] pool-scheduling worker-test-%d", i)
		specs = append(specs, &et.ExtensionTestSpec{
			Name:      name,
			Labels:    sets.New[string]("POOL-TEST"),
			Lifecycle: et.LifecycleBlocking,
			Resources: et.Resources{
				ResourcePools: map[string]int{"workers": 1},
			},
			Run: func(ctx context.Context) *et.ExtensionTestResult {
				time.Sleep(50 * time.Millisecond)
				return &et.ExtensionTestResult{
					Name:   name,
					Result: et.ResultPassed,
				}
			},
		})
	}

	for i := 0; i < 2; i++ {
		name := fmt.Sprintf("[sig-testing] pool-scheduling exclusive-test-%d", i)
		specs = append(specs, &et.ExtensionTestSpec{
			Name:      name,
			Labels:    sets.New[string]("POOL-TEST"),
			Lifecycle: et.LifecycleBlocking,
			Resources: et.Resources{
				ResourcePools: map[string]int{"workers": 2},
			},
			Run: func(ctx context.Context) *et.ExtensionTestResult {
				time.Sleep(50 * time.Millisecond)
				return &et.ExtensionTestResult{
					Name:   name,
					Result: et.ResultPassed,
				}
			},
		})
	}

	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("[sig-testing] pool-scheduling no-pool-test-%d", i)
		specs = append(specs, &et.ExtensionTestSpec{
			Name:      name,
			Labels:    sets.New[string]("POOL-TEST"),
			Lifecycle: et.LifecycleBlocking,
			Run: func(ctx context.Context) *et.ExtensionTestResult {
				time.Sleep(50 * time.Millisecond)
				return &et.ExtensionTestResult{
					Name:   name,
					Result: et.ResultPassed,
				}
			},
		})
	}

	return specs
}

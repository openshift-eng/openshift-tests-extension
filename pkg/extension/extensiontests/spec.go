package extensiontests

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/google/cel-go/common/types"

	t "github.com/openshift-eng/openshift-tests-extension/pkg/time"
)

func (specs ExtensionTestSpecs) Walk(walkFn func(*ExtensionTestSpec)) ExtensionTestSpecs {
	for i := range specs {
		walkFn(specs[i])
	}

	return specs
}

func (specs ExtensionTestSpecs) OtherNames() []string {
	var names []string
	for _, spec := range specs {
		for other := range spec.OtherNames {
			names = append(names, other)
		}
	}
	return names
}

func (specs ExtensionTestSpecs) Names() []string {
	var names []string
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

func (specs ExtensionTestSpecs) Run(w ResultWriter, maxConcurrent int) error {
	queue := make(chan *ExtensionTestSpec)
	failures := atomic.Int64{}

	// Feed the queue
	go func() {
		specs.Walk(func(spec *ExtensionTestSpec) {
			queue <- spec
		})
		close(queue)
	}()

	// Start consumers
	var wg sync.WaitGroup
	for i := 0; i < maxConcurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for spec := range queue {
				res := runSpec(spec)
				if res.Result == ResultFailed {
					failures.Add(1)
				}
				w.Write(res)
			}
		}()
	}

	// Wait for all consumers to finish
	wg.Wait()

	failCount := failures.Load()
	if failCount > 0 {
		return fmt.Errorf("%d tests failed", failCount)
	}
	return nil
}

func (specs ExtensionTestSpecs) MustFilter(celExprs []string) ExtensionTestSpecs {
	specs, err := specs.Filter(celExprs)
	if err != nil {
		panic(fmt.Sprintf("filter did not succeed: %s", err.Error()))
	}

	return specs
}

func (specs ExtensionTestSpecs) Filter(celExprs []string) (ExtensionTestSpecs, error) {
	var filteredSpecs ExtensionTestSpecs

	// Empty filters returns all
	if len(celExprs) == 0 {
		return specs, nil
	}

	env, err := cel.NewEnv(
		cel.Declarations(
			decls.NewVar("source", decls.String),
			decls.NewVar("name", decls.String),
			decls.NewVar("other_names", decls.NewListType(decls.String)),
			decls.NewVar("labels", decls.NewListType(decls.String)),
			decls.NewVar("tags", decls.NewMapType(decls.String, decls.String)),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	// OR all expressions together
	for _, spec := range specs {
		include := false
		for _, celExpr := range celExprs {
			// Parse CEL expression
			ast, iss := env.Parse(celExpr)
			if iss.Err() != nil {
				return nil, fmt.Errorf("error parsing CEL expression '%s': %v", celExpr, iss.Err())
			}

			// Check the AST
			checked, iss := env.Check(ast)
			if iss.Err() != nil {
				return nil, fmt.Errorf("error checking CEL expression '%s': %v", celExpr, iss.Err())
			}

			// Create a CEL program from the checked AST
			prg, err := env.Program(checked)
			if err != nil {
				return nil, fmt.Errorf("error creating CEL program: %v", err)
			}

			out, _, err := prg.Eval(map[string]interface{}{
				"name":        spec.Name,
				"source":      spec.Source,
				"other_names": spec.OtherNames,
				"labels":      spec.Labels.UnsortedList(),
				"tags":        spec.Tags,
			})
			if err != nil {
				return nil, fmt.Errorf("error evaluating CEL expression: %v", err)
			}

			// If any CEL expression evaluates to true, include the TestSpec
			if out == types.True {
				include = true
				break
			}
		}
		if include {
			filteredSpecs = append(filteredSpecs, spec)
		}
	}

	return filteredSpecs, nil
}

func (specs ExtensionTestSpecs) AddLabel(labels ...string) ExtensionTestSpecs {
	for i := range specs {
		specs[i].Labels.Insert(labels...)
	}

	return specs
}

func (specs ExtensionTestSpecs) RemoveLabel(labels ...string) ExtensionTestSpecs {
	for i := range specs {
		specs[i].Labels.Delete(labels...)
	}

	return specs
}

func (specs ExtensionTestSpecs) SetTag(key, value string) ExtensionTestSpecs {
	for i := range specs {
		specs[i].Tags[key] = value
	}

	return specs
}

func (specs ExtensionTestSpecs) UnsetTag(key string) ExtensionTestSpecs {
	for i := range specs {
		delete(specs[i].Tags, key)
	}

	return specs
}

func runSpec(spec *ExtensionTestSpec) *ExtensionTestResult {
	startTime := time.Now().UTC()
	res := spec.Run()
	duration := time.Since(startTime)
	endTime := startTime.Add(duration).UTC()
	if res == nil {
		// this shouldn't happen
		panic(fmt.Sprintf("test produced no result: %s", spec.Name))
	}

	res.Lifecycle = spec.Lifecycle

	// If the runner doesn't populate this info, we should set it
	if res.StartTime == nil {
		dbTime := t.DBTime(startTime)
		res.StartTime = &dbTime
	}
	if res.EndTime == nil {
		dbTime := t.DBTime(endTime)
		res.EndTime = &dbTime
	}
	if res.Duration == 0 {
		res.Duration = duration.Milliseconds()
	}

	return res
}

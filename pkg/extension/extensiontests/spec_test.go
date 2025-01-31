package extensiontests

import (
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift-eng/openshift-tests-extension/pkg/flags"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift-eng/openshift-tests-extension/pkg/dbtime"
)

func TestExtensionTestSpecs_Walk(t *testing.T) {
	specs := ExtensionTestSpecs{
		{Name: "test1"},
		{Name: "test2"},
	}

	var walkedNames []string
	specs.Walk(func(spec *ExtensionTestSpec) {
		walkedNames = append(walkedNames, spec.Name)
	})

	assert.Equal(t, []string{"test1", "test2"}, walkedNames)
}

func TestExtensionTestSpecs_MustFilter(t *testing.T) {
	specs := ExtensionTestSpecs{
		{Name: "test1"},
	}

	defer func() {
		if r := recover(); r != nil {
			assert.Contains(t, r.(string), "filter did not succeed")
		}
	}()

	// CEL expression that should fail
	specs.MustFilter([]string{"invalid_expr"})
	t.Errorf("Expected panic, but code continued")
}

func TestExtensionTestSpecs_Filter(t *testing.T) {
	tests := []struct {
		name     string
		specs    ExtensionTestSpecs
		celExprs []string
		want     ExtensionTestSpecs
		wantErr  bool
	}{
		{
			name: "simple filter on name",
			specs: ExtensionTestSpecs{
				{
					Name: "test1",
				},
				{
					Name: "test2",
				},
			},
			celExprs: []string{`name == "test1"`},
			want: ExtensionTestSpecs{
				{
					Name: "test1",
				},
			},
		},
		{
			name: "filter on tags",
			specs: ExtensionTestSpecs{
				{Name: "test1", Tags: map[string]string{"env": "prod"}},
				{Name: "test2", Tags: map[string]string{"env": "dev"}},
			},
			celExprs: []string{"tags['env'] == 'prod'"},
			want: ExtensionTestSpecs{
				{Name: "test1", Tags: map[string]string{"env": "prod"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.specs.Filter(tt.celExprs)
			if (err != nil) != tt.wantErr {
				t.Errorf("Filter() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Filter() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtensionTestSpecs_AddLabel(t *testing.T) {
	specs := ExtensionTestSpecs{
		{Name: "test1", Labels: sets.New[string]()},
	}

	specs = specs.AddLabel("critical")
	assert.True(t, specs[0].Labels.Has("critical"))
}

func TestExtensionTestSpecs_RemoveLabel(t *testing.T) {
	specs := ExtensionTestSpecs{
		{Name: "test1", Labels: sets.New[string]("to_remove")},
	}
	specs = specs.RemoveLabel("to_remove")
	assert.False(t, specs[0].Labels.Has("to_remove"))
}

func TestExtensionTestSpecs_SetTag(t *testing.T) {
	specs := ExtensionTestSpecs{
		{Name: "test1", Tags: make(map[string]string)},
	}

	specs = specs.SetTag("priority", "high")
	assert.Equal(t, "high", specs[0].Tags["priority"])
}

func TestExtensionTestSpecs_UnsetTag(t *testing.T) {
	specs := ExtensionTestSpecs{
		{Name: "test1", Tags: map[string]string{"priority": "high"}},
	}

	specs = specs.UnsetTag("priority")
	_, exists := specs[0].Tags["priority"]
	assert.False(t, exists)
}

func produceTestResult(name string, duration time.Duration) *ExtensionTestResult {
	return &ExtensionTestResult{
		Name:      name,
		Duration:  duration.Milliseconds(),
		StartTime: dbtime.Ptr(time.Now().UTC().Add(-duration)),
		EndTime:   dbtime.Ptr(time.Now()),
		Result:    ResultPassed,
	}
}

func TestExtensionTestSpecs_HookExecution(t *testing.T) {
	testCases := []struct {
		name               string
		expectedBeforeAll  int32
		expectedBeforeEach int32
		expectedAfterEach  int32
		expectedAfterAll   int32
		numSpecs           int
		numSpecSets        int
	}{
		{
			name:               "all hooks run - high test count",
			expectedBeforeAll:  1,
			expectedBeforeEach: 10000,
			expectedAfterEach:  10000,
			expectedAfterAll:   1,
			numSpecs:           10000,
		},
		{
			name:               "no AddBeforeAll",
			expectedBeforeAll:  0,
			expectedBeforeEach: 2,
			expectedAfterEach:  2,
			expectedAfterAll:   1,
			numSpecs:           2,
		},
		{
			name:               "no AddAfterEach",
			expectedBeforeAll:  1,
			expectedBeforeEach: 2,
			expectedAfterEach:  0,
			expectedAfterAll:   1,
			numSpecs:           2,
		},
		{
			name:               "only AddAfterAll",
			expectedBeforeAll:  0,
			expectedBeforeEach: 0,
			expectedAfterEach:  0,
			expectedAfterAll:   1,
			numSpecs:           2,
		},
		{
			name:               "beforeEach only",
			expectedBeforeAll:  0,
			expectedBeforeEach: 2,
			expectedAfterEach:  0,
			expectedAfterAll:   0,
			numSpecs:           2,
		},
		{
			name:               "beforeAll and afterAll only",
			expectedBeforeAll:  1,
			expectedBeforeEach: 0,
			expectedAfterEach:  0,
			expectedAfterAll:   1,
			numSpecs:           2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			specs := ExtensionTestSpecs{}
			for i := 0; i < tc.numSpecs; i++ {
				specs = append(specs, &ExtensionTestSpec{
					Name: fmt.Sprintf("test spec %d", i+1),
					Run: func() *ExtensionTestResult {
						return produceTestResult(fmt.Sprintf("test result %d", i+1), 20*time.Second)
					},
				})
			}

			// Hook invocation counters
			var beforeAllCount, beforeEachCount, afterEachCount, afterAllCount atomic.Int32

			// Set up hooks based on the expected test case
			if tc.expectedBeforeAll > 0 {
				specs.AddBeforeAll(func() {
					beforeAllCount.Add(1)
				})
			}
			if tc.expectedBeforeEach > 0 {
				specs.AddBeforeEach(func(_ ExtensionTestSpec) {
					beforeEachCount.Add(1)
				})
			}
			if tc.expectedAfterEach > 0 {
				specs.AddAfterEach(func(_ *ExtensionTestResult) {
					afterEachCount.Add(1)
				})
			}
			if tc.expectedAfterAll > 0 {
				specs.AddAfterAll(func() {
					afterAllCount.Add(1)
				})
			}

			// Run the test specs
			err := specs.Run(NullResultWriter{}, 10)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Verify the hook invocation counts
			if beforeAllCount.Load() != tc.expectedBeforeAll {
				t.Errorf("Expected BeforeAll to run %d times, but ran %d times", tc.expectedBeforeAll,
					beforeAllCount.Load())
			}
			if beforeEachCount.Load() != tc.expectedBeforeEach {
				t.Errorf("Expected BeforeEach to run %d times, but ran %d times", tc.expectedBeforeEach,
					beforeEachCount.Load())
			}
			if afterEachCount.Load() != tc.expectedAfterEach {
				t.Errorf("Expected AfterEach to run %d times, but ran %d times", tc.expectedAfterEach,
					afterEachCount.Load())
			}
			if afterAllCount.Load() != tc.expectedAfterAll {
				t.Errorf("Expected AfterAll to run %d times, but ran %d times", tc.expectedAfterAll,
					afterAllCount.Load())
			}
		})
	}
}

func TestExtensionTestSpec_Include(t *testing.T) {
	testCases := []struct {
		name string
		cel  string
		spec *ExtensionTestSpec
	}{
		{
			name: "simple OR expression",
			cel:  Or(PlatformEquals("aws"), NetworkEquals("ovn")),
			spec: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Include: `(platform=="aws" || network=="ovn")`},
			},
		},
		{
			name: "simple AND expression",
			cel:  And(UpgradeEquals("minor"), TopologyEquals("microshift"), ArchitectureEquals("amd64")),
			spec: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Include: `(upgrade=="minor" && topology=="microshift" && architecture=="amd64")`},
			},
		},
		{
			name: "complex expression with AND and OR",
			cel:  And(Or(PlatformEquals("aws"), NetworkEquals("ovn")), And(UpgradeEquals("minor"), TopologyEquals("microshift"), ArchitectureEquals("amd64"))),
			spec: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Include: `((platform=="aws" || network=="ovn") && (upgrade=="minor" && topology=="microshift" && architecture=="amd64"))`},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &ExtensionTestSpec{}
			resultingSpec := spec.Include(tc.cel)
			if diff := cmp.Diff(tc.spec, resultingSpec, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("Include returned unexpected resulting spec (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExtensionTestSpec_Exclude(t *testing.T) {
	testCases := []struct {
		name string
		cel  string
		spec *ExtensionTestSpec
	}{
		{
			name: "simple OR expression",
			cel:  Or(InstallerEquals("upi"), VersionEquals("4.19")),
			spec: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Exclude: `(installer=="upi" || version=="4.19")`},
			},
		},
		{
			name: "complex expression utilizing facts",
			cel:  And(FactEquals("cool.component", "absolutely"), FactEquals("simple.to.use", "true")),
			spec: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Exclude: `((fact_keys.exists(k, k=="cool.component") && facts["cool.component"].matches("absolutely")) && (fact_keys.exists(k, k=="simple.to.use") && facts["simple.to.use"].matches("true")))`},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &ExtensionTestSpec{}
			resultingSpec := spec.Exclude(tc.cel)
			if diff := cmp.Diff(tc.spec, resultingSpec, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("Include returned unexpected resulting spec (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExtensionTestSpecs_FilterByEnvironment(t *testing.T) {
	testCases := []struct {
		name     string
		specs    ExtensionTestSpecs
		envFlags flags.EnvironmentalFlags
		want     ExtensionTestSpecs
		wantErr  error
	}{
		{
			name: "no environment info",
			specs: ExtensionTestSpecs{
				{
					Name: "spec1",
				},
				{
					Name: "spec2",
				},
			},
			envFlags: flags.EnvironmentalFlags{Platform: "aws"},
			want: ExtensionTestSpecs{
				{
					Name: "spec1",
				},
				{
					Name: "spec2",
				},
			},
		},
		{
			name: "filter on single include expression",
			specs: ExtensionTestSpecs{
				{
					Name: "spec-aws-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: PlatformEquals("aws"),
					},
				},
				{
					Name: "spec-gcp-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: PlatformEquals("gcp"),
					},
				},
			},
			envFlags: flags.EnvironmentalFlags{Platform: "aws"},
			want: ExtensionTestSpecs{
				{
					Name: "spec-aws-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: PlatformEquals("aws"),
					},
				},
			},
		},
		{
			name: "filter on single exclude expression",
			specs: ExtensionTestSpecs{
				{
					Name: "spec-non-aws-only",
					EnvironmentSelector: EnvironmentSelector{
						Exclude: PlatformEquals("aws"),
					},
				},
				{
					Name: "spec-non-gcp-only",
					EnvironmentSelector: EnvironmentSelector{
						Exclude: PlatformEquals("gcp"),
					},
				},
			},
			envFlags: flags.EnvironmentalFlags{Platform: "aws"},
			want: ExtensionTestSpecs{
				{
					Name: "spec-non-gcp-only",
					EnvironmentSelector: EnvironmentSelector{
						Exclude: PlatformEquals("gcp"),
					},
				},
			},
		},
		{
			name: "filter on complex expressions",
			specs: ExtensionTestSpecs{
				{
					Name: "complex-spec-included",
					EnvironmentSelector: EnvironmentSelector{
						Include: And(
							Or(
								PlatformEquals("aws"), NetworkEquals("ovn"), NetworkStackEquals("ipv6"), ExternalConnectivityEquals("Disconnected")),
							And(
								UpgradeEquals("minor"), TopologyEquals("microshift"), ArchitectureEquals("amd64"),
							),
						),
					},
				},
				{
					Name: "complex-spec-excluded",
					EnvironmentSelector: EnvironmentSelector{
						Exclude: And(
							Or(
								PlatformEquals("aws"), NetworkEquals("ovn"), NetworkStackEquals("ipv6")),
							And(
								UpgradeEquals("minor"), TopologyEquals("microshift"), ArchitectureEquals("amd64"),
							),
						),
					},
				},
			},
			envFlags: flags.EnvironmentalFlags{
				Platform:             "aws",
				Network:              "sdn",
				NetworkStack:         "ipv6",
				Upgrade:              "minor",
				Topology:             "microshift",
				Architecture:         "amd64",
				Version:              "4.18",
				ExternalConnectivity: "Disconnected",
			},
			want: ExtensionTestSpecs{
				{
					Name: "complex-spec-included",
					EnvironmentSelector: EnvironmentSelector{
						Include: And(
							Or(
								PlatformEquals("aws"), NetworkEquals("ovn"), NetworkStackEquals("ipv6"), ExternalConnectivityEquals("Disconnected")),
							And(
								UpgradeEquals("minor"), TopologyEquals("microshift"), ArchitectureEquals("amd64"),
							),
						),
					},
				},
			},
		},
		{
			name: "exclude takes priority over conflicting include",
			specs: ExtensionTestSpecs{
				{
					Name: "spec-aws-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: PlatformEquals("aws"),
						Exclude: PlatformEquals("aws"),
					},
				},
			},
			envFlags: flags.EnvironmentalFlags{Platform: "aws"},
		},
		{
			name: "include based on facts",
			specs: ExtensionTestSpecs{
				{
					Name: "only-when-cool",
					EnvironmentSelector: EnvironmentSelector{
						Include: And(FactEquals("cool.component", "absolutely")),
					},
				},
				{
					Name: "only-when-super-cool",
					EnvironmentSelector: EnvironmentSelector{
						Include: And(FactEquals("super.cool.component", "absolutely")),
					},
				},
			},
			envFlags: flags.EnvironmentalFlags{Facts: map[string]string{"cool.component": "absolutely"}},
			want: ExtensionTestSpecs{
				{
					Name: "only-when-cool",
					EnvironmentSelector: EnvironmentSelector{
						Include: And(FactEquals("cool.component", "absolutely")),
					},
				},
			},
		},
		{
			name: "include based on optional capabilities",
			specs: ExtensionTestSpecs{
				{
					Name: "spec-baremetal-build",
					EnvironmentSelector: EnvironmentSelector{
						Include: OptionalCapabilitiesIncludeAny("baremetal", "build"),
					},
				},
				{
					Name: "spec-baremetal-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: OptionalCapabilitiesIncludeAll("baremetal"),
					},
				},
				{
					Name: "spec-build-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: OptionalCapabilitiesIncludeAll("build"),
					},
				},
			},
			envFlags: flags.EnvironmentalFlags{OptionalCapabilities: []string{"baremetal"}},
			want: ExtensionTestSpecs{
				{
					Name: "spec-baremetal-build",
					EnvironmentSelector: EnvironmentSelector{
						Include: OptionalCapabilitiesIncludeAny("baremetal", "build"),
					},
				},
				{
					Name: "spec-baremetal-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: OptionalCapabilitiesIncludeAll("baremetal"),
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.specs.FilterByEnvironment(tc.envFlags)
			if diff := cmp.Diff(tc.wantErr, err, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("FilterByEnvironment returned unexpected error (-want +got): %s", diff)
			}
			if diff := cmp.Diff(tc.want, result, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("FilterByEnvironment returned unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSelect(t *testing.T) {
	testCases := []struct {
		name     string
		specs    ExtensionTestSpecs
		selectFn SelectFunction
		want     ExtensionTestSpecs
	}{
		{
			name: "name contains",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "gcp-only",
				},
			},
			selectFn: NameContains("aws"),
			want: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
			},
		},
		{
			name: "can return multiple",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "gcp-only",
				},
				{
					Name: "another-aws-test",
				},
			},
			selectFn: NameContains("aws"),
			want: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "another-aws-test",
				},
			},
		},
		{
			name: "has label",
			specs: ExtensionTestSpecs{
				{
					Name:   "aws",
					Labels: sets.New("aws-test"),
				},
				{
					Name:   "gcp",
					Labels: sets.New("gcp-test"),
				},
			},
			selectFn: HasLabel("aws-test"),
			want: ExtensionTestSpecs{
				{
					Name:   "aws",
					Labels: sets.New("aws-test"),
				},
			},
		},
		{
			name: "has tag with value",
			specs: ExtensionTestSpecs{
				{
					Name: "aws",
					Tags: map[string]string{
						"tag-a": "val",
					},
				},
				{
					Name: "gcp",
					Tags: map[string]string{
						"tag-a": "another-val",
					},
				},
			},
			selectFn: HasTagWithValue("tag-a", "another-val"),
			want: ExtensionTestSpecs{
				{
					Name: "gcp",
					Tags: map[string]string{
						"tag-a": "another-val",
					},
				},
			},
		},
		{
			name: "with lifecycle",
			specs: ExtensionTestSpecs{
				{
					Name:      "aws",
					Lifecycle: LifecycleBlocking,
				},
				{
					Name:      "gcp",
					Lifecycle: LifecycleInforming,
				},
			},
			selectFn: WithLifecycle(LifecycleBlocking),
			want: ExtensionTestSpecs{
				{
					Name:      "aws",
					Lifecycle: LifecycleBlocking,
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.specs.Select(tc.selectFn)
			if diff := cmp.Diff(tc.want, result, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("Select returned unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSelectAny(t *testing.T) {
	testCases := []struct {
		name      string
		specs     ExtensionTestSpecs
		selectFns []SelectFunction
		want      ExtensionTestSpecs
	}{
		{
			name: "name contains",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "azure-only",
				},
				{
					Name: "gcp-only",
				},
			},
			selectFns: []SelectFunction{NameContains("aws"), NameContains("gcp")},
			want: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "gcp-only",
				},
			},
		},
		{
			name: "has label or tag with value",
			specs: ExtensionTestSpecs{
				{
					Name:   "aws",
					Labels: sets.New("aws-test"),
					Tags: map[string]string{
						"tag-a": "val",
					},
				},
				{
					Name:   "excluded",
					Labels: sets.New("gcp-test"),
					Tags: map[string]string{
						"tag-a": "val",
					},
				},
				{
					Name:   "gcp",
					Labels: sets.New("gcp-test"),
					Tags: map[string]string{
						"tag-a": "another-val",
					},
				},
			},
			selectFns: []SelectFunction{HasLabel("aws-test"), HasTagWithValue("tag-a", "another-val")},
			want: ExtensionTestSpecs{
				{
					Name:   "aws",
					Labels: sets.New("aws-test"),
					Tags: map[string]string{
						"tag-a": "val",
					},
				},
				{
					Name:   "gcp",
					Labels: sets.New("gcp-test"),
					Tags: map[string]string{
						"tag-a": "another-val",
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.specs.SelectAny(tc.selectFns)
			if diff := cmp.Diff(tc.want, result, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("SelectAny returned unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSelectAll(t *testing.T) {
	testCases := []struct {
		name      string
		specs     ExtensionTestSpecs
		selectFns []SelectFunction
		want      ExtensionTestSpecs
	}{
		{
			name: "name contains",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "azure-only",
				},
				{
					Name: "aws-test",
				},
			},
			selectFns: []SelectFunction{NameContains("aws"), NameContains("test")},
			want: ExtensionTestSpecs{
				{
					Name: "aws-test",
				},
			},
		},
		{
			name: "has label and tag with value",
			specs: ExtensionTestSpecs{
				{
					Name:   "aws",
					Labels: sets.New("aws-test"),
					Tags: map[string]string{
						"tag-a": "good-val",
					},
				},
				{
					Name:   "excluded",
					Labels: sets.New("aws-test"),
					Tags: map[string]string{
						"tag-a": "val",
					},
				},
				{
					Name:   "gcp",
					Labels: sets.New("gcp-test"),
					Tags: map[string]string{
						"tag-a": "good-val",
					},
				},
			},
			selectFns: []SelectFunction{HasLabel("aws-test"), HasTagWithValue("tag-a", "good-val")},
			want: ExtensionTestSpecs{
				{
					Name:   "aws",
					Labels: sets.New("aws-test"),
					Tags: map[string]string{
						"tag-a": "good-val",
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.specs.SelectAll(tc.selectFns)
			if diff := cmp.Diff(tc.want, result, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("SelectAny returned unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

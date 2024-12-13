package flags

import "github.com/spf13/pflag"

type EnvironmentalFlags struct {
	Platform     string
	Network      string
	Upgrade      string
	Topology     string
	Architecture string
	Installer    string
	Config       []string
	Facts        map[string]string
	Version      string
}

func NewEnvironmentalFlags() *EnvironmentalFlags {
	return &EnvironmentalFlags{}
}

func (f *EnvironmentalFlags) BindFlags(fs *pflag.FlagSet) {
	fs.StringVar(&f.Platform,
		"platform",
		"",
		"The hardware or cloud platform (\"aws\", \"gcp\", \"metal\", ...). Since: v1.0")
	fs.StringVar(&f.Network,
		"network",
		"",
		"The network of the target cluster (\"ovn\", \"sdn\"). Since: v1.0")
	fs.StringVar(&f.Upgrade,
		"upgrade",
		"",
		"The upgrade that was performed prior to the test run (\"micro\", \"minor\"). Since: v1.0")
	fs.StringVar(&f.Topology,
		"topology",
		"",
		"The target cluster topology (\"ha\", \"microshift\", ...). Since: v1.0")
	fs.StringVar(&f.Architecture,
		"architecture",
		"",
		"The CPU architecture of the target cluster (\"amd64\", \"arm64\"). Since: v1.0")
	fs.StringVar(&f.Installer,
		"installer",
		"",
		"The installer used to create the cluster (\"ipi\", \"upi\", \"assisted\", ...). Since: v1.0")
	fs.StringSliceVarP(&f.Config,
		"config",
		"",
		[]string{},
		"Multiple. Non-default component configuration in the environment. Since: v1.0")
	fs.StringToStringVar(&f.Facts,
		"fact",
		make(map[string]string),
		"Facts advertised by cluster components. Since: v1.0")
	fs.StringVar(&f.Version,
		"version",
		"",
		"\"major.minor\" version of target cluster. Since: v1.0")
}

func (f *EnvironmentalFlags) IsEmpty() bool {
	return f.Platform == "" &&
		f.Network == "" &&
		f.Upgrade == "" &&
		f.Topology == "" &&
		f.Architecture == "" &&
		f.Installer == "" &&
		len(f.Config) == 0 &&
		len(f.Facts) == 0 &&
		f.Version == ""
}
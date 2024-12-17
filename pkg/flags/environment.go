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
		"The hardware or cloud platform (\"aws\", \"gcp\", \"metal\", ...).")
	fs.StringVar(&f.Network,
		"network",
		"",
		"The network of the target cluster (\"ovn\", \"sdn\").")
	fs.StringVar(&f.Upgrade,
		"upgrade",
		"",
		"The upgrade that was performed prior to the test run (\"micro\", \"minor\").")
	fs.StringVar(&f.Topology,
		"topology",
		"",
		"The target cluster topology (\"ha\", \"microshift\", ...).")
	fs.StringVar(&f.Architecture,
		"architecture",
		"",
		"The CPU architecture of the target cluster (\"amd64\", \"arm64\").")
	fs.StringVar(&f.Installer,
		"installer",
		"",
		"The installer used to create the cluster (\"ipi\", \"upi\", \"assisted\", ...).")
	fs.StringSliceVarP(&f.Config,
		"config",
		"",
		[]string{},
		"Multiple. Non-default component configuration in the environment.")
	fs.StringToStringVar(&f.Facts,
		"fact",
		make(map[string]string),
		"Facts advertised by cluster components.")
	fs.StringVar(&f.Version,
		"version",
		"",
		"\"major.minor\" version of target cluster.")
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

type flagVersion string

const v1dot0 = flagVersion("v1.0")

type FlagVersion struct {
	Flag  string      `json:"flag"`
	Since flagVersion `json:"since"`
}

var EnvironmentFlagsForVersion = []FlagVersion{
	{
		Flag:  "platform",
		Since: v1dot0,
	},
	{
		Flag:  "network",
		Since: v1dot0,
	},
	{
		Flag:  "upgrade",
		Since: v1dot0,
	},
	{
		Flag:  "topology",
		Since: v1dot0,
	},
	{
		Flag:  "architecture",
		Since: v1dot0,
	},
	{
		Flag:  "installer",
		Since: v1dot0,
	},
	{
		Flag:  "config",
		Since: v1dot0,
	},
	{
		Flag:  "fact",
		Since: v1dot0,
	},
	{
		Flag:  "version",
		Since: v1dot0,
	},
}

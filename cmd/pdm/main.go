// Command pdm is a CLI for spatial querying of Portuguese municipal PDM/IGT
// planning data: given a coordinate or polygon, it reports the planning/zoning
// rules and constraints that apply at that exact location.
package main

import (
	"os"

	"github.com/bernardosimoes/pdm/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}

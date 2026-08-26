package joshbot

import "flag"

var updateGoldenFiles = flag.Bool(
	"update",
	false,
	"update golden files",
)

func checkGoldenUsingFlag(goldenPath string, actual []byte) error {
	return checkGolden(goldenPath, actual, *updateGoldenFiles)
}

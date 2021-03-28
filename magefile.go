//+build mage

package main

import (
	"bytes"
	"fmt"
	"github.com/magefile/mage/sh"
	"io/ioutil"
	"strings"
	"time"
)

var Default = Build

func buildlog(s string, v ...interface{}) {
	fmt.Printf(s+"\n", v...)
}

// https://github.com/magefile/mage/blob/4cf3cfcc82bab35b01ab1657c2aa2d0faeadac49/sh/cmd.go#L79
func silentOutput(cmd string, args ...string) (string, error) {
	buf := &bytes.Buffer{}
	_, err := sh.Exec(nil, buf, ioutil.Discard, cmd, args...)
	return strings.TrimSuffix(buf.String(), "\n"), err
}

func Build() error {
	env := map[string]string{
		"CGO_ENABLED": "0",
		"GOARCH":      "amd64",
	}
	// Read version string from git
	var versionString string
	var err error
	var gitOutput string
	gitOutput, err = silentOutput("git", "describe", "--tags", "--exact-match")
	buildlog("- detecting program version...")
	// We're at an exact tag
	if err != nil {
		// We've got commits after a tag
		buildlog("  - no exact git tag, fallback to tag+commit")
		gitOutput, err = silentOutput("git", "describe", "--tags")
		// Unable to locate git tag, fallback to git commit
		if err != nil {
			buildlog("  - no git tag+commit, fallback to commit")
			var out string
			out, err = silentOutput("git", "describe", "--always")
			if err != nil {
				buildlog("  - no version could be detected from git")
				return err
			}

			gitOutput = fmt.Sprintf("DEV %s", out)
		}
	}
	versionString = strings.TrimSpace(gitOutput)
	buildlog("- version is %s, start building...", versionString)

	compiledTimeString := time.Now().UTC().Format(time.RFC3339)
	for _, os := range []string{"darwin", "linux"} {
		ldFlags := []string{
			"-s",
			"-w",
			fmt.Sprintf("-X 'bililive-downloader/version.VersionString=%s'", versionString),
			fmt.Sprintf("-X 'bililive-downloader/version.CompiledTimeString=%s'", compiledTimeString),
		}
		env["GOOS"] = os
		buildlog("  - building for %s %s", env["GOOS"], env["GOARCH"])
		outputBinFile := fmt.Sprintf("bililive-downloader-%s-%s", env["GOOS"], env["GOARCH"])
		if err := sh.RunWith(env, "go", "build", "-ldflags", strings.Join(ldFlags, " "), "-o", outputBinFile); err != nil {
			buildlog("    - failed: %v", err)
			return err
		}

		buildlog("    - built: %s", outputBinFile)
	}

	buildlog("- build finished")
	return nil
}

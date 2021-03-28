package version

import "time"

var VersionString string
var CompiledTimeString string
var CompiledTime time.Time

func init() {
	CompiledTime, _ = time.Parse(time.RFC3339, CompiledTimeString)
}

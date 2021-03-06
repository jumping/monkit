// Copyright (C) 2016 Space Monkey, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package environment

import (
	"runtime"

	"gopkg.in/spacemonkeygo/monkit.v2"
)

// Runtime returns a StatSource that includes information gathered from the
// Go runtime, including the number of goroutines currently running, and
// other live memory data. Not expected to be called directly, as this
// StatSource is added by Register.
func Runtime() monkit.StatSource {
	return monkit.StatSourceFunc(func(cb func(name string, val float64)) {
		cb("goroutines", float64(runtime.NumGoroutine()))

		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		monkit.Prefix("memory.", monkit.StatSourceFromStruct(stats)).Stats(cb)
	})
}

func init() {
	registrations["runtime"] = Runtime()
}

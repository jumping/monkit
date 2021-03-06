// Copyright (C) 2015 Space Monkey, Inc.
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

package present

import (
	"bufio"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/spacemonkeygo/errors"
	"github.com/spacemonkeygo/errors/errhttp"
	"gopkg.in/spacemonkeygo/monkit.v2"
)

var (
	BadRequest = errors.NewClass("Bad Request", errhttp.SetStatusCode(400))
	NotFound   = errors.NewClass("Not Found", errhttp.SetStatusCode(404))
)

// Result writes the expected data to io.Writer and returns any errors if
// found.
type Result func(io.Writer) error

func curry(reg *monkit.Registry,
	f func(*monkit.Registry, io.Writer) error) func(io.Writer) error {
	return func(w io.Writer) error {
		return f(reg, w)
	}
}

// FromRequest takes a registry (usually the Default registry), an incoming
// path, and optional query parameters, and returns a Result if possible.
//
// FromRequest understands the following paths:
//  * /ps, /ps/text       - returns the result of SpansText
//  * /ps/dot             - returns the result of SpansDot
//  * /ps/json            - returns the result of SpansJSON
//  * /funcs, /funcs/text - returns the result of FuncsText
//  * /funcs/dot          - returns the result of FuncsDot
//  * /funcs/json         - returns the result of FuncsJSON
//  * /stats, /stats/text - returns the result of StatsText
//  * /stats/json         - returns the result of StatsJSON
//  * /trace/svg          - returns the result of TraceQuerySVG
//  * /trace/json         - returns the result of TraceQueryJSON
//
// The last two paths are worth discussing in more detail, as they take
// query parameters. All trace endpoints require at least one of the following
// two query parameters:
//  * regex    - If provided, the very next Span that crosses a Func that has
//               a name that matches this regex will start a trace until that
//               triggering Span ends, provided the trace_id matches.
//  * trace_id - If provided, the very next Span on a trace with the given
//               trace id will start a trace until the triggering Span ends,
//               provided the regex matches. NOTE: the trace_id will be parsed
//               in hex.
// By default, regular expressions are matched ahead of time against all known
// Funcs, but perhaps the Func you want to trace hasn't been observed by the
// process yet, in which case the regex will fail to match anything. You can
// turn off this preselection behavior by providing preselect=false as an
// additional query param. Be advised that until a trace completes, whether
// or not it has started, it adds a small amount of overhead (a comparison or
// two) to every monitored function.
func FromRequest(reg *monkit.Registry, path string, query url.Values) (
	f Result, contentType string, err error) {

	defer func() {
		if err != nil {
			return
		}
		// wrap all functions with buffering
		unbuffered := f
		f = func(w io.Writer) (err error) {
			buf := bufio.NewWriter(w)
			err = unbuffered(buf)
			if err != nil {
				return err
			}
			err = buf.Flush()
			return err
		}
	}()

	first, rest := shift(path)
	second, _ := shift(rest)
	switch first {
	case "ps":
		switch second {
		case "", "text":
			return curry(reg, SpansText), "text/plain; charset=utf-8", nil
		case "dot":
			return curry(reg, SpansDot), "text/plain; charset=utf-8", nil
		case "json":
			return curry(reg, SpansJSON), "application/json; charset=utf-8", nil
		}

	case "funcs":
		switch second {
		case "", "text":
			return curry(reg, FuncsText), "text/plain; charset=utf-8", nil
		case "dot":
			return curry(reg, FuncsDot), "text/plain; charset=utf-8", nil
		case "json":
			return curry(reg, FuncsJSON), "application/json; charset=utf-8", nil
		}

	case "stats":
		prefix := query.Get("prefix")
		switch second {
		case "", "text":
			return func(w io.Writer) error {
				return FilteredStatsText(reg, w, prefix)
			}, "text/plain; charset=utf-8", nil
		case "json":
			return func(w io.Writer) error {
				return FilteredStatsJSON(reg, w, prefix)
			}, "application/json; charset=utf-8", nil
		}

	case "trace":
		regexStr := query.Get("regex")
		traceIdStr := query.Get("trace_id")
		if regexStr == "" && traceIdStr == "" {
			return nil, "", BadRequest.New("at least one of 'regex' or 'trace_id' " +
				"query parameters required")
		}
		fnMatcher := func(*monkit.Func) bool { return true }

		if regexStr != "" {
			re, err := regexp.Compile(regexStr)
			if err != nil {
				return nil, "", BadRequest.New("invalid regex %#v: %v",
					regexStr, err)
			}
			fnMatcher = func(f *monkit.Func) bool {
				return re.MatchString(f.FullName())
			}

			preselect := true
			if query.Get("preselect") != "" {
				preselect, err = strconv.ParseBool(query.Get("preselect"))
				if err != nil {
					return nil, "", BadRequest.New("invalid preselect %#v: %v",
						query.Get("preselect"), err)
				}
			}
			if preselect {
				funcs := map[*monkit.Func]bool{}
				reg.Funcs(func(f *monkit.Func) {
					if fnMatcher(f) {
						funcs[f] = true
					}
				})
				if len(funcs) <= 0 {
					return nil, "", BadRequest.New("regex preselect matches 0 functions")
				}

				fnMatcher = func(f *monkit.Func) bool { return funcs[f] }
			}
		}

		spanMatcher := func(s *monkit.Span) bool { return fnMatcher(s.Func()) }

		if traceIdStr != "" {
			traceId, err := strconv.ParseUint(traceIdStr, 16, 64)
			if err != nil {
				return nil, "", BadRequest.New(
					"trace_id expected to be hex unsigned 64 bit number: %#v", traceIdStr)
			}
			spanMatcher = func(s *monkit.Span) bool {
				return s.Trace().Id() == int64(traceId) && fnMatcher(s.Func())
			}
		}

		switch second {
		case "svg":
			return func(w io.Writer) error {
				return TraceQuerySVG(reg, w, spanMatcher)
			}, "image/svg+xml; charset=utf-8", nil
		case "json":
			return func(w io.Writer) error {
				return TraceQueryJSON(reg, w, spanMatcher)
			}, "application/json; charset=utf-8", nil
		}
	}
	return nil, "", NotFound.New("path not found: %s", path)
}

func shift(path string) (dir, left string) {
	path = strings.TrimLeft(path, "/")
	split := strings.Index(path, "/")
	if split == -1 {
		return path, ""
	}
	return path[:split], path[split:]
}

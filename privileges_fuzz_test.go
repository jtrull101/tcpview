package main

import (
	"strings"
	"testing"
)

// FuzzParseCapEff feeds arbitrary text through the /proc/self/status
// capability-bitmask parser. Real-world input here is file content, not
// strictly user-controlled, but it's still "data this process didn't
// generate itself" and worth hardening the same way.
func FuzzParseCapEff(f *testing.F) {
	f.Add("CapEff:\t0000000000002000\n")
	f.Add("CapEff:\tffffffffffffffff\n")
	f.Add("")
	f.Add("CapEff:\n")
	f.Add("CapEff:\tnot-hex\n")
	f.Add("Name:\ttcpview\nState:\tR (running)\n")
	f.Add(strings.Repeat("x", 10000))

	f.Fuzz(func(t *testing.T, s string) {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("parseCapEff panicked on %q: %v", s, rec)
			}
		}()
		_, _ = parseCapEff(strings.NewReader(s), capNetRaw)
	})
}

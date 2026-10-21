package remote

import (
	"strings"
	"testing"
)

// FuzzParseTcpdumpList feeds arbitrary bytes - standing in for whatever a
// remote host's `tcpdump -D` (or a misbehaving one) prints - through the
// interface-list parser: it must never panic, and every interface it does
// return must have a usable name.
func FuzzParseTcpdumpList(f *testing.F) {
	f.Add([]byte(modernTcpdumpD))
	f.Add([]byte("1.eth0\n2.x ( [\n3.] ["))
	f.Fuzz(func(t *testing.T, in []byte) {
		for _, iface := range parseTcpdumpList(in) {
			if iface.Name == "" || strings.ContainsAny(iface.Name, " \n") {
				t.Fatalf("parsed invalid interface name %q", iface.Name)
			}
		}
	})
}

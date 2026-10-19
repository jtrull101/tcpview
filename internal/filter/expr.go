package filter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jtrull101/tcpview/internal/store"
)

// ParseExpression parses the simple "key=value key=value ..." syntax the
// UI's filter bar accepts into a Filter. Recognized keys: iface, src, dst,
// sport, dport, proto. An empty or whitespace-only expression parses to the
// zero Filter (matches everything) rather than an error - that's the
// "cleared filter" state, not a mistake.
func ParseExpression(expr string) (Filter, error) {
	var f Filter
	for _, tok := range strings.Fields(expr) {
		key, val, ok := strings.Cut(tok, "=")
		if !ok {
			return Filter{}, fmt.Errorf("filter: invalid term %q, expected key=value", tok)
		}
		if val == "" {
			return Filter{}, fmt.Errorf("filter: %q has no value", tok)
		}

		switch strings.ToLower(key) {
		case "iface":
			f.Iface = val
		case "src":
			p, err := ParseIPOrCIDR(val)
			if err != nil {
				return Filter{}, err
			}
			f.SrcIP = p
		case "dst":
			p, err := ParseIPOrCIDR(val)
			if err != nil {
				return Filter{}, err
			}
			f.DstIP = p
		case "sport":
			port, err := parsePort(val)
			if err != nil {
				return Filter{}, fmt.Errorf("filter: invalid sport %q: %w", val, err)
			}
			f.SrcPort = port
		case "dport":
			port, err := parsePort(val)
			if err != nil {
				return Filter{}, fmt.Errorf("filter: invalid dport %q: %w", val, err)
			}
			f.DstPort = port
		case "proto":
			f.Proto = store.Proto(strings.ToUpper(val))
		default:
			return Filter{}, fmt.Errorf("filter: unknown key %q (want iface, src, dst, sport, dport, or proto)", key)
		}
	}
	return f, nil
}

func parsePort(s string) (uint16, error) {
	port, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, err
	}
	if port == 0 {
		return 0, fmt.Errorf("port 0 means \"any\" and can't be filtered on explicitly")
	}
	return uint16(port), nil
}

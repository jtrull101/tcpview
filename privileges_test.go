package main

import (
	"strings"
	"testing"
)

// --- positive ---

func TestParseCapEff_BitSet(t *testing.T) {
	t.Parallel()
	// 0x2000 = 1<<13 = CAP_NET_RAW only.
	r := strings.NewReader("CapInh:\t0000000000000000\nCapEff:\t0000000000002000\n")
	ok, err := parseCapEff(r, capNetRaw)
	if err != nil {
		t.Fatalf("parseCapEff error: %v", err)
	}
	if !ok {
		t.Fatal("parseCapEff = false, want true for a mask with CAP_NET_RAW set")
	}
}

func TestParseCapEff_FullCapabilitySet(t *testing.T) {
	t.Parallel()
	r := strings.NewReader("CapEff:\tffffffffffffffff\n")
	ok, err := parseCapEff(r, capNetRaw)
	if err != nil {
		t.Fatalf("parseCapEff error: %v", err)
	}
	if !ok {
		t.Fatal("parseCapEff = false for an all-ones mask, want true")
	}
}

// --- negative ---

func TestParseCapEff_BitNotSet(t *testing.T) {
	t.Parallel()
	r := strings.NewReader("CapEff:\t0000000000000000\n")
	ok, err := parseCapEff(r, capNetRaw)
	if err != nil {
		t.Fatalf("parseCapEff error: %v", err)
	}
	if ok {
		t.Fatal("parseCapEff = true for an all-zero mask, want false")
	}
}

func TestParseCapEff_MissingLineErrors(t *testing.T) {
	t.Parallel()
	r := strings.NewReader("Name:\ttcpview\nState:\tR (running)\n")
	if _, err := parseCapEff(r, capNetRaw); err == nil {
		t.Fatal("parseCapEff with no CapEff line returned no error")
	}
}

func TestParseCapEff_MalformedHexErrors(t *testing.T) {
	t.Parallel()
	r := strings.NewReader("CapEff:\tnot-hex\n")
	if _, err := parseCapEff(r, capNetRaw); err == nil {
		t.Fatal("parseCapEff with malformed hex returned no error")
	}
}

func TestParseCapEff_WrongFieldCountErrors(t *testing.T) {
	t.Parallel()
	r := strings.NewReader("CapEff:\n")
	if _, err := parseCapEff(r, capNetRaw); err == nil {
		t.Fatal("parseCapEff with a CapEff line missing its value returned no error")
	}
}

func TestParseCapEff_EmptyInputErrors(t *testing.T) {
	t.Parallel()
	r := strings.NewReader("")
	if _, err := parseCapEff(r, capNetRaw); err == nil {
		t.Fatal("parseCapEff on empty input returned no error")
	}
}

// --- chaos ---

func TestParseCapEff_RandomLinesNeverPanic(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"\x00\x01\x02",
		"CapEff:",
		"CapEff:\t\t\t",
		strings.Repeat("CapEff:\tdeadbeef\n", 1000),
		"CapEff:\t-1\n",
		"CapEff:\t0x2000\n", // 0x prefix not valid for base-16 ParseUint without stripping
	}
	for i, in := range inputs {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("parseCapEff panicked on input %d (%q): %v", i, in, rec)
				}
			}()
			_, _ = parseCapEff(strings.NewReader(in), capNetRaw)
		}()
	}
}

// --- load ---

func TestParseCapEff_Load_ManyBitPositions(t *testing.T) {
	t.Parallel()
	r := func() string { return "CapEff:\tffffffffffffffff\n" }
	for bit := uint(0); bit < 64; bit++ {
		ok, err := parseCapEff(strings.NewReader(r()), bit)
		if err != nil {
			t.Fatalf("parseCapEff(bit=%d) error: %v", bit, err)
		}
		if !ok {
			t.Fatalf("parseCapEff(bit=%d) = false for an all-ones mask, want true", bit)
		}
	}
}

package main

import "testing"

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"8108398025":      "918108398025",
		"+91 81083 98025": "918108398025",
		"091-8108398025":  "918108398025",
		"918108398025":    "918108398025",
		"  8108398025  ":  "918108398025",
		"081083 98025":    "918108398025",
		"08108398025":     "918108398025",
	}

	for input, want := range cases {
		got, err := normalizePhone(input)
		if err != nil {
			t.Fatalf("normalizePhone(%q) returned error %v", input, err)
		}
		if got != want {
			t.Errorf("normalizePhone(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizePhoneRejectsBadInput(t *testing.T) {
	for _, input := range []string{"", "12345", "abcdefghij", "919181038980251234"} {
		if _, err := normalizePhone(input); err == nil {
			t.Errorf("normalizePhone(%q) should have failed", input)
		}
	}
}

func TestPrettyPhone(t *testing.T) {
	got := prettyPhone("918108398025")
	want := "+91 81083 98025"
	if got != want {
		t.Errorf("prettyPhone = %q, want %q", got, want)
	}
}

func TestSamePhone(t *testing.T) {
	if !samePhone("8108398025", "+91 81083 98025") {
		t.Error("samePhone should treat both formats as equal")
	}
	if samePhone("8108398025", "9999999999") {
		t.Error("samePhone should not match different numbers")
	}
}

package main

import "testing"

func TestFormatINR(t *testing.T) {
	cases := map[float64]string{
		0:       "₹0.00",
		199:     "₹199.00",
		1250.5:  "₹1,250.50",
		123456:  "₹1,23,456.00",
		1234567: "₹12,34,567.00",
		99.999:  "₹100.00",
		-60:     "-₹60.00",
	}
	for input, want := range cases {
		if got := formatINR(input); got != want {
			t.Errorf("formatINR(%v) = %q, want %q", input, got, want)
		}
	}
}

func TestFormatWeight(t *testing.T) {
	cases := map[int]string{200: "200 g", 500: "500 g", 1000: "1 kg", 1500: "1500 g"}
	for input, want := range cases {
		if got := formatWeight(input); got != want {
			t.Errorf("formatWeight(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestGroupIndianDigits(t *testing.T) {
	cases := map[string]string{
		"1":      "1",
		"123":    "123",
		"1234":   "1,234",
		"12345":  "12,345",
		"123456": "1,23,456",
	}
	for input, want := range cases {
		if got := groupIndianDigits(input); got != want {
			t.Errorf("groupIndianDigits(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSplitForWhatsAppKeepsShortMessagesWhole(t *testing.T) {
	if got := splitForWhatsApp("hello"); len(got) != 1 || got[0] != "hello" {
		t.Errorf("short message should stay whole, got %v", got)
	}
}

func TestSplitForWhatsAppBreaksLongMessages(t *testing.T) {
	body := ""
	for range 900 {
		body += "NuttyWonders granola bars are freshly baked. "
	}
	parts := splitForWhatsApp(body)
	if len(parts) < 2 {
		t.Fatalf("long message should be split, got %d part(s)", len(parts))
	}
	for i, part := range parts {
		if len(part) > 3600 {
			t.Errorf("part %d is %d bytes, over the limit", i, len(part))
		}
	}
}

func TestTruncateKeepsWholeWordsWhenItCan(t *testing.T) {
	got := truncate("NuttyWorders granola bars are baked fresh daily", 30)
	if got[len(got)-3:] != "…" {
		t.Errorf("truncate should mark the cut, got %q", got)
	}
	if got != "NuttyWorders granola bars are…" {
		t.Errorf("truncate = %q", got)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Ragi Brownie":        "ragi-brownie",
		"  Orange  Chocolate": "orange-chocolate",
		"Millet Bar 60g":      "millet-bar-60g",
		"":                    "granola",
	}
	for input, want := range cases {
		if got := slugify("", orDefault(input, "granola")); got != want {
			t.Errorf("slugify(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestClamp(t *testing.T) {
	if got := clamp(0, 1, 50); got != 1 {
		t.Errorf("clamp below range = %d, want 1", got)
	}
	if got := clamp(999, 1, 50); got != 50 {
		t.Errorf("clamp above range = %d, want 50", got)
	}
	if got := clamp(10, 1, 50); got != 10 {
		t.Errorf("clamp inside range = %d, want 10", got)
	}
}

func TestParseMoney(t *testing.T) {
	if got := parseMoney("799", 60); got != 799 {
		t.Errorf("parseMoney = %v, want 799", got)
	}
	if got := parseMoney("not a number", 60); got != 60 {
		t.Errorf("parseMoney should fall back, got %v", got)
	}
}

func TestFirstName(t *testing.T) {
	cases := map[string]string{
		"Kavita Sharma": "Kavita",
		"Kavita":        "Kavita",
		"":              "there",
	}
	for input, want := range cases {
		if got := firstName(input); got != want {
			t.Errorf("firstName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGreetReplyUsesTheCustomerName(t *testing.T) {
	got := greetReply("Kavita", "https://app.nuttywondrs.com")
	if got == "" || !contains(got, "Kavita") {
		t.Errorf("greeting should greet Kavita, got %q", got)
	}
	if greetReply("", "https://x") == greetReply("Kavita", "https://x") {
		t.Error("anonymous greeting should differ from a named one")
	}
}

func TestFriendlyConstraintMentionsPhone(t *testing.T) {
	got := friendlyConstraint("invalid phone number")
	if !contains(got, "10-digit") {
		t.Errorf("expected a helpful phone message, got %q", got)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

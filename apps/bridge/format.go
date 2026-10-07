package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// formatINR renders rupees the way an Indian customer expects to read them:
// ₹1,23,456.50 rather than 123,456.50.
func formatINR(amount float64) string {
	sign := ""
	if amount < 0 {
		sign = "-"
		amount = -amount
	}

	whole := int64(math.Floor(amount + 1e-9))
	paise := int64(math.Round((amount - float64(whole)) * 100))
	if paise == 100 {
		whole++
		paise = 0
	}

	return sign + "₹" + groupIndianDigits(strconv.FormatInt(whole, 10)) + "." + fmt.Sprintf("%02d", paises(paise))
}

func paises(p int64) int { return int(p) }

func groupIndianDigits(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	last3 := digits[len(digits)-3:]
	rest := digits[:len(digits)-3]

	var parts []string
	for len(rest) > 2 {
		parts = append([]string{rest[len(rest)-2:]}, parts...)
		rest = rest[:len(rest)-2]
	}
	if rest != "" {
		parts = append([]string{rest}, parts...)
	}
	return strings.Join(parts, ",") + "," + last3
}

// formatWeight renders grams the way the catalogue does: 200 g, 1.2 kg.
func formatWeight(grams int) string {
	if grams >= 1000 && grams%1000 == 0 {
		return strconv.Itoa(grams/1000) + " kg"
	}
	return strconv.Itoa(grams) + " g"
}

func itoa(n int) string { return strconv.Itoa(n) }

func clamp(n, lo, hi int) int {
	return min(max(n, lo), hi)
}

// truncate shortens a WhatsApp message body without cutting mid-word when it
// can avoid it, because the bot replies have a hard length limit.
func truncate(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := s[:limit]
	if idx := strings.LastIndex(cut, " "); idx > limit/2 {
		cut = cut[:idx]
	}
	return strings.TrimSpace(cut) + "…"
}

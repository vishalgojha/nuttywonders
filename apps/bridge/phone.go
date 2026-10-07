package main

import (
	"errors"
	"strings"
)

var errBadPhone = errors.New("enter a valid 10-digit Indian mobile number")

const defaultCountryCode = "91"

// normalizePhone turns whatever a customer typed into a digits-only E.164
// number. Indian mobiles are the common case, so a bare 10-digit number gains
// the +91 prefix and a leading 0 is dropped. Numbers that already carry a
// country code are passed through as-is.
func normalizePhone(raw string) (string, error) {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, raw)

	switch {
	case len(digits) == 10:
		return defaultCountryCode + digits, nil
	case len(digits) == 11 && strings.HasPrefix(digits, defaultCountryCode):
		return digits, nil
	case len(digits) == 12 && strings.HasPrefix(digits, defaultCountryCode):
		return digits, nil
	case len(digits) == 13 && strings.HasPrefix(digits, "0"+defaultCountryCode):
		return digits[1:], nil
	case len(digits) == 11 && strings.HasPrefix(digits, "0"):
		return defaultCountryCode + digits[1:], nil
	default:
		return "", errBadPhone
	}
}

// prettyPhone formats a stored E.164 number for humans.
func prettyPhone(e164 string) string {
	if len(e164) == 12 && strings.HasPrefix(e164, defaultCountryCode) {
		return "+91 " + e164[2:7] + " " + e164[7:12]
	}
	return "+" + e164
}

// samePhone compares two numbers after normalising both.
func samePhone(a, b string) bool {
	na, errA := normalizePhone(a)
	nb, errB := normalizePhone(b)
	if errA != nil || errB != nil {
		return false
	}
	return na == nb
}

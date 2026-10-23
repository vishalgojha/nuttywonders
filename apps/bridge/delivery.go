package main

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type deliveryRate struct {
	Pincode   string  `json:"pincode"`
	Area      string  `json:"area"`
	ChargeINR float64 `json:"charge_inr"`
	Active    bool    `json:"active"`
}

func (s *Server) handleDeliveryRate(w http.ResponseWriter, r *http.Request) {
	pincode := strings.TrimSpace(r.URL.Query().Get("pincode"))
	if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(pincode) {
		writeError(w, http.StatusBadRequest, "enter a valid 6-digit pincode")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var rows []deliveryRate
	query := url.Values{}
	query.Set("pincode", "eq."+pincode)
	query.Set("active", "eq.true")
	query.Set("select", "pincode,area,charge_inr,active")
	query.Set("limit", "1")
	if err := s.db.Select(ctx, "delivery_pincode_rates", query, &rows); err != nil {
		s.writeUpstreamError(w, err)
		return
	}

	if len(rows) == 0 {
		// Keep the legacy fallback for uncovered areas until the rate table is
		// fully populated. The order RPC applies the same fallback server-side.
		writeJSON(w, http.StatusOK, map[string]any{
			"pincode": pincode, "area": "Other serviceable area",
			"charge_inr": 120, "configured": false,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pincode": rows[0].Pincode, "area": rows[0].Area,
		"charge_inr": rows[0].ChargeINR, "configured": true,
	})
}

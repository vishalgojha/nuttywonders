package main

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	state := s.wa.state()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"whatsapp_status": state.Status,
		"whatsapp_jid":    state.JID,
		"time":            time.Now().UTC(),
	})
}

// publicSettings are the handful of facts a shopper needs to see before
// checkout. Anything sensitive stays admin-only.
var publicSettings = []string{
	"upi_vpa", "upi_payee_name", "delivery_fee_inr", "free_shipping_over_inr",
	"payment_instructions", "support_email", "business_hours", "announcement",
	"instagram_url", "app_base_url",
}

func (s *Server) handlePublicSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	settings := make(map[string]any, len(publicSettings))
	for _, key := range publicSettings {
		settings[key] = s.setting(ctx, key, "")
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

func (s *Server) handleListProducts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var products []Product
	query := url.Values{}
	query.Set("is_active", "eq.true")
	query.Set("select", "id,slug,name,tagline,description,category,image_path,pack_size_g,shelf_life_days,ingredients,allergens,price_inr,stock_packs,sort_order")
	query.Set("order", "sort_order.asc,id.asc")

	if err := s.db.Select(ctx, "products", query, &products); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if products == nil {
		products = []Product{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": products})
}

func (s *Server) handleAdminListProducts(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var products []Product
	query := url.Values{}
	query.Set("select", "id,slug,name,tagline,description,category,image_path,pack_size_g,shelf_life_days,ingredients,allergens,price_inr,cost_ingredient_inr,cost_packaging_inr,cost_labour_inr,cost_overhead_inr,stock_packs,is_active,sort_order")
	query.Set("order", "sort_order.asc,id.asc")

	if err := s.db.Select(ctx, "products", query, &products); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if products == nil {
		products = []Product{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": products})
}

type productInput struct {
	Name              string   `json:"name"`
	Slug              string   `json:"slug"`
	Tagline           *string  `json:"tagline"`
	Description       *string  `json:"description"`
	Category          string   `json:"category"`
	ImagePath         *string  `json:"image_path"`
	PackSizeG         int      `json:"pack_size_g"`
	ShelfLifeDays     int      `json:"shelf_life_days"`
	Ingredients       []string `json:"ingredients"`
	Allergens         []string `json:"allergens"`
	PriceINR          float64  `json:"price_inr"`
	CostIngredientINR float64  `json:"cost_ingredient_inr"`
	CostPackagingINR  float64  `json:"cost_packaging_inr"`
	CostLabourINR     float64  `json:"cost_labour_inr"`
	CostOverheadINR   float64  `json:"cost_overhead_inr"`
	StockPacks        int      `json:"stock_packs"`
	IsActive          *bool    `json:"is_active"`
	SortOrder         int      `json:"sort_order"`
}

func (s *Server) handleAdminSaveProduct(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	var input productInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Name) == "" {
		writeError(w, http.StatusBadRequest, "product name is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	slug := slugify(input.Slug, input.Name)
	body := map[string]any{
		"slug":                slug,
		"name":                strings.TrimSpace(input.Name),
		"category":            orDefault(input.Category, "bars"),
		"pack_size_g":         orDefaultInt(input.PackSizeG, 200),
		"shelf_life_days":     orDefaultInt(input.ShelfLifeDays, 20),
		"ingredients":         orEmptySlice(input.Ingredients),
		"allergens":           orEmptySlice(input.Allergens),
		"price_inr":           input.PriceINR,
		"cost_ingredient_inr": input.CostIngredientINR,
		"cost_packaging_inr":  input.CostPackagingINR,
		"cost_labour_inr":     input.CostLabourINR,
		"cost_overhead_inr":   input.CostOverheadINR,
		"stock_packs":         max(input.StockPacks, 0),
		"sort_order":          input.SortOrder,
	}
	if input.Tagline != nil {
		body["tagline"] = strings.TrimSpace(*input.Tagline)
	}
	if input.Description != nil {
		body["description"] = strings.TrimSpace(*input.Description)
	}
	if input.ImagePath != nil {
		body["image_path"] = strings.TrimSpace(*input.ImagePath)
	}
	if input.IsActive != nil {
		body["is_active"] = *input.IsActive
	}

	var saved []Product
	if err := s.db.Upsert(ctx, "products", body, &saved); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if len(saved) == 0 {
		writeError(w, http.StatusBadGateway, "product was not saved")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"product": saved[0]})
}

func slugify(preferred, fallback string) string {
	base := strings.TrimSpace(preferred)
	if base == "" {
		base = fallback
	}
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func orDefaultInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func orEmptySlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func parseMoney(raw string, fallback float64) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fallback
	}
	return value
}

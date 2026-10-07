package main

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// handleInboundWhatsApp turns a customer message into a helpful reply. It is
// intentionally keyword-driven: customers type "menu", "price", "track" and
// expect an answer, and anything else falls back to handing them the shop link.
func (s *Server) handleInboundWhatsApp(ctx context.Context, phone, body string) {
	if !s.cfg.AutoReply {
		return
	}
	if !s.limit.allowKey("whatsapp", phone, 12, time.Minute) {
		return
	}

	reply, err := s.botReply(withPhone(ctx, phone), body)
	if err != nil {
		s.log.Warn("bot reply failed", "phone", phone, "error", err)
		return
	}
	if strings.TrimSpace(reply) == "" {
		return
	}

	sendCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := s.wa.SendText(sendCtx, phone, reply); err != nil {
		s.log.Warn("could not reply", "phone", phone, "error", err)
	}
}

var (
	orderCodePattern = regexp.MustCompile(`\bNW-?\d{6}-?\d{3,4}\b`)
	phonePattern     = regexp.MustCompile(`(\+?\d[\d\s\-]{8,16}\d)`)
	stopWords        = map[string]bool{"stop": true, "unsubscribe": true, "optout": true, "opt out": true, "end": true, "quit": true, "leave": true}
)

func (s *Server) botReply(ctx context.Context, body string) (string, error) {
	text := strings.TrimSpace(body)
	lower := strings.ToLower(text)

	for word := range stopWords {
		if lower == word || strings.HasPrefix(lower, word+" ") {
			if err := s.optOut(ctx, s.currentPhone(ctx)); err != nil {
				return "", err
			}
			return "You will not get any more offers from NuttyWonders. Reply MENU any time to see our products.", nil
		}
	}

	switch {
	case matchesAny(lower, "hi", "hello", "hey", "hii", "namaste"):
		return s.greetingReply(ctx)
	case matchesAny(lower, "menu", "products", "catalogue", "catalog", "kya hai", "what do you sell", "items"):
		return s.menuReply(ctx)
	case matchesAny(lower, "price", "cost", "rate", "mrp", "kitna"):
		return s.priceReply(ctx)
	case matchesAny(lower, "track", "status", "order status", "where is my", "kahan"):
		return s.trackReply(ctx, text)
	case matchesAny(lower, "order", "buy", "book", "want to order", "chahiye", "mangwana", "lena hai"):
		return s.orderReply(ctx)
	case matchesAny(lower, "offer", "discount", "coupon", "sale", "deal"):
		return s.offerReply(ctx)
	case matchesAny(lower, "payment", "upi", "pay", "gpay", "phonepe", "how to pay"):
		return s.paymentReply(ctx)
	case matchesAny(lower, "delivery", "shipping", "time", "kitne din", "dispatch"):
		return s.deliveryReply(ctx)
	case matchesAny(lower, "help", "?"):
		return s.helpReply(ctx)
	case lower == "yes" || lower == "y":
		return s.orderReply(ctx)
	}

	if code := orderCodePattern.FindString(strings.ToUpper(text)); code != "" {
		return s.trackReply(ctx, code)
	}
	if matchesAny(lower, "shelf", "expiry", "expire", "best before", "storage") {
		return s.shelfLifeReply(ctx)
	}

	return s.fallbackReply(ctx, text)
}

func matchesAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func (s *Server) greetingReply(ctx context.Context) (string, error) {
	name := ""
	if customer, err := s.findCustomer(ctx, s.currentPhone(ctx)); err == nil && customer.Name != nil {
		name = firstName(*customer.Name)
	}
	return greetReply(name, s.shopLink(ctx, "")), nil
}

func greetReply(name, link string) string {
	if name != "" {
		return "Namaste " + name + "! Welcome to NuttyWonders.\n\n" + link + "\n\nReply MENU for our products."
	}
	return "Namaste! Welcome to NuttyWonders.\n\n" + link + "\n\nReply MENU for our products."
}

func (s *Server) menuReply(ctx context.Context) (string, error) {
	products, err := s.activeProducts(ctx)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("*NuttyWonders menu*\n\n")
	for _, product := range products {
		fmt.Fprintf(&b, "%s — %s (%s)\n", product.Name, formatINR(product.PriceINR), product.packLabel())
		if product.Tagline != nil && *product.Tagline != "" {
			b.WriteString("  " + truncate(*product.Tagline, 70) + "\n")
		}
	}
	b.WriteString("\nOrder here: " + s.shopLink(ctx, "") + "\n")
	b.WriteString("Reply PRICE for a detailed list or MENU again to repeat this.")
	return b.String(), nil
}

func (s *Server) priceReply(ctx context.Context) (string, error) {
	products, err := s.activeProducts(ctx)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("*Our prices*\n\n")
	for _, product := range products {
		fmt.Fprintf(&b, "%s\n  %s for %s\n", product.Name, formatINR(product.PriceINR), product.packLabel())
		if len(product.Ingredients) > 0 {
			fmt.Fprintf(&b, "  Made with %s\n", truncate(strings.Join(product.Ingredients, ", "), 90))
		}
		if len(product.Allergens) > 0 {
			fmt.Fprintf(&b, "  Contains: %s\n", truncate(strings.Join(product.Allergens, ", "), 60))
		}
		b.WriteString("\n")
	}
	b.WriteString("Order: " + s.shopLink(ctx, ""))
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) orderReply(ctx context.Context) (string, error) {
	var b strings.Builder
	b.WriteString("To place your order:\n\n")
	fmt.Fprintf(&b, "1. Add items to the cart here: %s\n", s.shopLink(ctx, ""))
	b.WriteString("2. Fill your name, WhatsApp number and address.\n")
	b.WriteString("3. Pay by UPI and share the screenshot.\n\n")

	if vpa := s.setting(ctx, "upi_vpa", ""); vpa != "" {
		fmt.Fprintf(&b, "Our UPI id: %s\n\n", vpa)
	} else {
		b.WriteString("We will share the UPI id with your order confirmation.\n\n")
	}
	if support := s.setting(ctx, "support_email", ""); support != "" {
		fmt.Fprintf(&b, "Questions? %s\n", support)
	}
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) offerReply(ctx context.Context) (string, error) {
	if announcement := s.setting(ctx, "announcement", ""); announcement != "" {
		return announcement + "\n\n" + s.shopLink(ctx, ""), nil
	}
	threshold := s.setting(ctx, "free_shipping_over_inr", "799")
	return "Right now you get free delivery on orders above " + formatINR(parseMoney(threshold, 799)) +
		".\n\n" + s.shopLink(ctx, ""), nil
}

func (s *Server) paymentReply(ctx context.Context) (string, error) {
	vpa := s.setting(ctx, "upi_vpa", "")
	payee := s.setting(ctx, "upi_payee_name", "NuttyWonders")

	var b strings.Builder
	b.WriteString("We accept UPI (GPay, PhonePe, Paytm, BHIM).\n\n")
	if vpa != "" {
		fmt.Fprintf(&b, "UPI id: %s\nPayee: %s\n\n", vpa, payee)
	} else {
		b.WriteString("We share the UPI id in your order confirmation message.\n\n")
	}
	if instructions := s.setting(ctx, "payment_instructions", ""); instructions != "" {
		b.WriteString(instructions + "\n\n")
	}
	b.WriteString("Once you have paid, send us the screenshot on this chat and we will start packing right away.")
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) deliveryReply(ctx context.Context) (string, error) {
	var b strings.Builder
	if hours := s.setting(ctx, "business_hours", ""); hours != "" {
		b.WriteString(hours + "\n\n")
	}
	b.WriteString("We ship across India. Delivery usually takes 3 to 6 working days.\n")
	if fee := s.setting(ctx, "delivery_fee_inr", "60"); fee != "" {
		fmt.Fprintf(&b, "Delivery charge: %s\n", formatINR(parseMoney(fee, 60)))
	}
	if threshold := s.setting(ctx, "free_shipping_over_inr", "799"); threshold != "" {
		fmt.Fprintf(&b, "Free delivery above %s.\n", formatINR(parseMoney(threshold, 799)))
	}
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) shelfLifeReply(ctx context.Context) (string, error) {
	products, err := s.activeProducts(ctx)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("*Shelf life*\n\n")
	for _, product := range products {
		fmt.Fprintf(&b, "%s — %d days\n", product.Name, product.ShelfLifeDays)
	}
	b.WriteString("\nAll our bars are freshly baked, no palm oil and no artificial colour.")
	return b.String(), nil
}

func (s *Server) helpReply(ctx context.Context) (string, error) {
	return strings.Join([]string{
		"I can help with:",
		"MENU — our products and prices",
		"PRICE — full price and ingredient list",
		"ORDER — how to place an order",
		"TRACK NW-YYMMDD-0001 — order status",
		"PAYMENT — how to pay by UPI",
		"DELIVERY — shipping charges and time",
		"STOP — stop offers",
		"",
		s.shopLink(ctx, ""),
	}, "\n"), nil
}

func (s *Server) trackReply(ctx context.Context, text string) (string, error) {
	code := orderCodePattern.FindString(strings.ToUpper(text))
	if code == "" {
		return "Please send your order code, for example TRACK NW-251001-0001.", nil
	}
	code = strings.ToUpper(strings.ReplaceAll(code, "-", "-"))
	if !strings.Contains(code, "-") {
		return "Please send your order code, for example TRACK NW-251001-0001.", nil
	}

	phone := s.currentPhone(ctx)
	order, err := s.findOrder(ctx, code, phone)
	if err != nil {
		return "We could not find order " + code + " on this number. Please check the code, or contact us and we will look it up.", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "*%s*\nStatus: %s\nPayment: %s\n\n%s\nTotal: %s",
		order.Code, order.Status, order.PaymentStatus, orderSummary(order), formatINR(order.TotalINR))

	if order.PaymentStatus == "pending" {
		if vpa := s.setting(ctx, "upi_vpa", ""); vpa != "" {
			fmt.Fprintf(&b, "\n\nPayment pending. UPI id: %s", vpa)
		}
	}
	if base := s.setting(ctx, "app_base_url", ""); base != "" {
		fmt.Fprintf(&b, "\n\n%s/order?code=%s", base, order.Code)
	}
	return b.String(), nil
}

func (s *Server) fallbackReply(ctx context.Context, text string) (string, error) {
	// Numbers that are not an order code are usually a new customer's number.
	if match := phonePattern.FindString(text); match != "" && len(digitsOnly(match)) >= 10 {
		return "Thanks! We have your number. Our team will message you here shortly, or you can order now: " +
			s.shopLink(ctx, ""), nil
	}

	return "Thanks for messaging NuttyWonders! Reply MENU to see our products, PRICE for prices, or HELP for what I can do.\n\n" +
		s.shopLink(ctx, ""), nil
}

func (s *Server) shopLink(ctx context.Context, path string) string {
	base := s.setting(ctx, "app_base_url", s.cfg.PublicBaseURL)
	if path == "" {
		return base
	}
	return base + path
}

// currentPhone threads the sender's number through the context so the bot
// helpers can query per-customer data.
func (s *Server) currentPhone(ctx context.Context) string {
	if value, ok := ctx.Value(phoneContextKey{}).(string); ok {
		return value
	}
	return ""
}

type phoneContextKey struct{}

func withPhone(ctx context.Context, phone string) context.Context {
	return context.WithValue(ctx, phoneContextKey{}, phone)
}

// optOut honours a STOP even from someone who has never ordered before. There
// is no customer row to update in that case, so create a placeholder: the
// phone_e164 column is unique, which makes this an upsert.
func (s *Server) optOut(ctx context.Context, phone string) error {
	if strings.TrimSpace(phone) == "" {
		return fmt.Errorf("no phone in context")
	}
	return s.db.Upsert(ctx, "customers", map[string]any{
		"phone_e164":       phone,
		"marketing_opt_in": false,
		"unsubscribed":     true,
	}, nil)
}

func (s *Server) findCustomer(ctx context.Context, phone string) (*Customer, error) {
	if phone == "" {
		return nil, fmt.Errorf("no phone in context")
	}
	var customers []Customer
	query := url.Values{}
	query.Set("phone_e164", "eq."+phone)
	query.Set("select", "id,phone_e164,name,email,city,pincode,marketing_opt_in,unsubscribed,created_at")
	query.Set("limit", "1")

	if err := s.db.Select(ctx, "customers", query, &customers); err != nil {
		return nil, err
	}
	if len(customers) == 0 {
		return nil, fmt.Errorf("customer not found")
	}
	return &customers[0], nil
}

func (s *Server) activeProducts(ctx context.Context) ([]Product, error) {
	query := url.Values{}
	query.Set("is_active", "eq.true")
	query.Set("select", "id,name,tagline,pack_size_g,shelf_life_days,ingredients,allergens,price_inr,sort_order")
	query.Set("order", "sort_order.asc,id.asc")

	var products []Product
	if err := s.db.Select(ctx, "products", query, &products); err != nil {
		return nil, err
	}
	return products, nil
}

func digitsOnly(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

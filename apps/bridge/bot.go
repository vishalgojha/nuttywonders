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
	if isAdminSelfChat(ctx) {
		s.handleAdminSelfChat(ctx, body)
		return
	}
	if s.customerInHumanMode(ctx, phone) {
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

type adminSelfChatKey struct{}

func withAdminSelfChat(ctx context.Context) context.Context {
	return context.WithValue(ctx, adminSelfChatKey{}, true)
}

func isAdminSelfChat(ctx context.Context) bool {
	value, _ := ctx.Value(adminSelfChatKey{}).(bool)
	return value
}

// handleAdminSelfChat is deliberately command-based. The self-chat can see
// private operational data, so it must not be routed through the customer AI
// prompt or expose data based on an ambiguous natural-language request.
func (s *Server) handleAdminSelfChat(ctx context.Context, body string) {
	if !s.limit.allowKey("admin-self-chat", s.currentPhone(ctx), 30, time.Minute) {
		return
	}

	text := strings.TrimSpace(body)
	lower := strings.ToLower(text)
	var reply string
	var err error
	switch {
	case lower == "help" || lower == "admin" || lower == "commands":
		reply = "*Admin self-chat commands*\n\n" +
			"ORDERS — recent active orders\n" +
			"PENDING — unpaid orders\n" +
			"ORDER NW-YYMMDD-0001 — order details\n" +
			"STOCK — inventory snapshot\n" +
			"CUSTOMER 918619244233 — customer lookup\n" +
			"HELP — show this list"
	case lower == "orders" || lower == "new orders" || lower == "recent orders":
		reply, err = s.adminRecentOrders(ctx)
	case lower == "pending" || lower == "pending payments" || lower == "unpaid":
		reply, err = s.adminPendingOrders(ctx)
	case strings.HasPrefix(lower, "order ") || orderCodePattern.MatchString(strings.ToUpper(text)):
		code := orderCodePattern.FindString(strings.ToUpper(text))
		reply, err = s.adminOrderLookup(ctx, code)
	case strings.HasPrefix(lower, "customer "):
		reply, err = s.adminCustomerLookup(ctx, strings.TrimSpace(text[len("customer "):]))
	case lower == "stock" || lower == "inventory":
		reply, err = s.adminInventory(ctx)
	default:
		reply = "I’m in admin mode. Reply HELP for commands."
	}
	if err != nil {
		s.log.Warn("admin self-chat command failed", "command", text, "error", err)
		reply = "I could not fetch that right now. Please try again or use the admin dashboard."
	}
	if strings.TrimSpace(reply) == "" {
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := s.wa.SendText(sendCtx, s.currentPhone(ctx), reply); err != nil {
		s.log.Warn("could not reply to admin self-chat", "error", err)
	}
}

func (s *Server) adminRecentOrders(ctx context.Context) (string, error) {
	orders, err := s.queryOrders(ctx, url.Values{
		"status": {"in.(new,confirmed,packed,shipped)"},
		"limit":  {"10"},
	})
	if err != nil {
		return "", err
	}
	if len(orders) == 0 {
		return "No active orders.", nil
	}
	var b strings.Builder
	b.WriteString("*Recent active orders*\n\n")
	for _, order := range orders {
		fmt.Fprintf(&b, "%s — %s — %s — %s\n", order.Code, order.CustomerName, order.PaymentStatus, formatINR(order.TotalINR))
	}
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) adminPendingOrders(ctx context.Context) (string, error) {
	orders, err := s.queryOrders(ctx, url.Values{
		"payment_status": {"eq.pending"},
		"status":         {"neq.cancelled"},
		"limit":          {"20"},
	})
	if err != nil {
		return "", err
	}
	if len(orders) == 0 {
		return "No unpaid orders.", nil
	}
	var b strings.Builder
	b.WriteString("*Pending payments*\n\n")
	for _, order := range orders {
		fmt.Fprintf(&b, "%s — %s — %s — %s\n", order.Code, order.CustomerName, order.PhoneE164, formatINR(order.TotalINR))
	}
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) adminOrderLookup(ctx context.Context, code string) (string, error) {
	if code == "" {
		return "Send an order code, for example ORDER NW-261024-0001.", nil
	}
	orders, err := s.queryOrders(ctx, url.Values{"code": {"eq." + code}, "limit": {"1"}})
	if err != nil {
		return "", err
	}
	if len(orders) == 0 {
		return "Order " + code + " was not found.", nil
	}
	order := orders[0]
	var b strings.Builder
	fmt.Fprintf(&b, "*%s*\nCustomer: %s (%s)\nStatus: %s\nPayment: %s\nTotal: %s\n", order.Code, order.CustomerName, order.PhoneE164, order.Status, order.PaymentStatus, formatINR(order.TotalINR))
	if order.Pincode != nil {
		fmt.Fprintf(&b, "Pincode: %s\n", *order.Pincode)
	}
	if len(order.Items) > 0 {
		b.WriteString("\nItems:\n")
		for _, item := range order.Items {
			fmt.Fprintf(&b, "- %s ×%d\n", item.ProductName, item.Quantity)
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) adminCustomerLookup(ctx context.Context, term string) (string, error) {
	digits := digitsOnly(term)
	query := url.Values{}
	query.Set("select", "phone_e164,name,email,city,pincode,support_mode,created_at")
	query.Set("limit", "5")
	if digits != "" {
		phone, err := normalizePhone(digits)
		if err == nil {
			query.Set("phone_e164", "eq."+phone)
		}
	} else {
		query.Set("name", "ilike.*"+escapePostgrestPattern(term)+"*")
	}
	var rows []Customer
	if err := s.db.Select(ctx, "customers", query, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "Customer not found.", nil
	}
	var b strings.Builder
	b.WriteString("*Customer lookup*\n\n")
	for _, customer := range rows {
		fmt.Fprintf(&b, "%s — %s", valueOrEmpty(customer.Name), customer.PhoneE164)
		if customer.City != nil {
			fmt.Fprintf(&b, " — %s", *customer.City)
		}
		fmt.Fprintf(&b, "\nSupport mode: %s\n", customer.SupportMode)
	}
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) adminInventory(ctx context.Context) (string, error) {
	products, err := s.activeProducts(ctx)
	if err != nil {
		return "", err
	}
	if len(products) == 0 {
		return "No active products.", nil
	}
	var b strings.Builder
	b.WriteString("*Active products*\n\n")
	for _, product := range products {
		fmt.Fprintf(&b, "%s — %s — %d packs\n", product.Name, formatINR(product.PriceINR), product.StockPacks)
	}
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) customerInHumanMode(ctx context.Context, phone string) bool {
	query := url.Values{}
	query.Set("phone_e164", "eq."+phone)
	query.Set("select", "support_mode")
	query.Set("limit", "1")
	var rows []struct {
		SupportMode string `json:"support_mode"`
	}
	if err := s.db.Select(ctx, "customers", query, &rows); err != nil {
		return false
	}
	return len(rows) > 0 && rows[0].SupportMode == "human"
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
	if matchesAny(lower, "human", "agent", "team member", "person", "representative") {
		if err := s.setSupportMode(ctx, s.currentPhone(ctx), "human"); err != nil {
			return "", err
		}
		return "A team member will take over this chat shortly. Reply AI if you want the assistant to resume.", nil
	}
	if strings.TrimSpace(lower) == "ai" {
		if err := s.setSupportMode(ctx, s.currentPhone(ctx), "ai"); err != nil {
			return "", err
		}
		return "The AI assistant is back on. How can we help?", nil
	}

	if s.cfg.AIBotEnabled && strings.TrimSpace(s.cfg.OpenRouterAPIKey) != "" {
		if reply, err := s.openRouterReply(ctx, text); err == nil && strings.TrimSpace(reply) != "" {
			return reply, nil
		} else if err != nil {
			s.log.Warn("AI reply failed; using the built-in reply", "error", err)
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

func (s *Server) setSupportMode(ctx context.Context, phone, mode string) error {
	query := url.Values{}
	query.Set("phone_e164", "eq."+phone)
	return s.db.Update(ctx, "customers", query, map[string]any{"support_mode": mode}, nil)
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
	threshold := s.setting(ctx, "free_shipping_over_inr", "1000")
	return "Right now you get free delivery on orders above " + formatINR(parseMoney(threshold, 1000)) +
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
	if fee := s.setting(ctx, "delivery_fee_inr", "120"); fee != "" {
		fmt.Fprintf(&b, "Delivery charge: %s\n", formatINR(parseMoney(fee, 120)))
	}
	if threshold := s.setting(ctx, "free_shipping_over_inr", "1000"); threshold != "" {
		fmt.Fprintf(&b, "Free delivery above %s.\n", formatINR(parseMoney(threshold, 1000)))
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
	query.Set("select", "id,name,tagline,pack_size_g,shelf_life_days,ingredients,allergens,price_inr,stock_packs,sort_order")
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

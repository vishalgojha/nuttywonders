package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type checkoutLine struct {
	ProductID int64 `json:"product_id"`
	Quantity  int   `json:"quantity"`
}

type checkoutAddress struct {
	AddressLine1 string `json:"address_line1"`
	AddressLine2 string `json:"address_line2"`
	City         string `json:"city"`
	Pincode      string `json:"pincode"`
	Landmark     string `json:"landmark"`
}

type checkoutRequest struct {
	Name        string          `json:"name"`
	Phone       string          `json:"phone"`
	Email       string          `json:"email"`
	Address     checkoutAddress `json:"address"`
	Items       []checkoutLine  `json:"items"`
	Note        string          `json:"note"`
	Channel     string          `json:"channel"`
	NotifyOwner *bool           `json:"notify_owner"`
}

func (s *Server) handleCheckout(w http.ResponseWriter, r *http.Request) {
	var req checkoutRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	phone, err := normalizePhone(req.Phone)
	if err != nil {
		writeError(w, http.StatusBadRequest, errBadPhone.Error())
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "your cart is empty")
		return
	}

	lines := make([]map[string]any, 0, len(req.Items))
	for _, line := range req.Items {
		if line.ProductID <= 0 {
			writeError(w, http.StatusBadRequest, "cart contains an unknown product")
			return
		}
		if line.Quantity < 1 {
			writeError(w, http.StatusBadRequest, "quantity must be at least 1")
			return
		}
		lines = append(lines, map[string]any{
			"product_id": line.ProductID,
			"quantity":   clamp(line.Quantity, 1, 50),
		})
	}

	channel := req.Channel
	if channel != "whatsapp" && channel != "manual" {
		channel = "web"
	}

	notifyOwner := true
	if req.NotifyOwner != nil {
		notifyOwner = *req.NotifyOwner
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	params := map[string]any{
		"p_phone":          phone,
		"p_name":           strings.TrimSpace(req.Name),
		"p_items":          lines,
		"p_address":        req.Address,
		"p_channel":        channel,
		"p_delivery_note":  strings.TrimSpace(req.Note),
		"p_reply_to_owner": notifyOwner,
	}

	var result placeOrderResult
	if err := s.db.RPC(ctx, "place_order", params, &result); err != nil {
		s.writeUpstreamError(w, err)
		return
	}

	if req.Email != "" {
		s.saveCustomerEmail(ctx, result.Order.CustomerID, strings.TrimSpace(req.Email))
	}

	s.log.Info("order placed", "code", result.Order.Code, "channel", channel)

	payment := map[string]any{
		"vpa":          s.setting(ctx, "upi_vpa", ""),
		"payee_name":   s.setting(ctx, "upi_payee_name", "NuttyWonders"),
		"instructions": s.setting(ctx, "payment_instructions", "Pay by UPI and share the screenshot on WhatsApp."),
		"track_url":    s.setting(ctx, "app_base_url", s.cfg.PublicBaseURL) + "/order?code=" + result.Order.Code,
	}
	if rzOrderID, amountPaise, ok := s.attachRazorpayOrder(ctx, &result.Order); ok {
		payment["method"] = "razorpay"
		payment["razorpay"] = map[string]any{
			"key_id":       s.cfg.RazorpayKeyID,
			"order_id":     rzOrderID,
			"amount_paise": amountPaise,
			"currency":     "INR",
		}
	} else {
		payment["method"] = "upi"
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"order":   result.Order,
		"items":   result.Items,
		"payment": payment,
	})
}

func (s *Server) saveCustomerEmail(ctx context.Context, customerID *int64, email string) {
	if customerID == nil || email == "" {
		return
	}
	query := url.Values{}
	query.Set("id", "eq."+strconv.FormatInt(*customerID, 10))
	_ = s.db.Update(ctx, "customers", query, map[string]any{"email": email}, nil)
}

func (s *Server) handleTrackOrder(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("code")))
	phoneRaw := strings.TrimSpace(r.URL.Query().Get("phone"))
	if code == "" || phoneRaw == "" {
		writeError(w, http.StatusBadRequest, "enter your order code and phone number")
		return
	}

	phone, err := normalizePhone(phoneRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, errBadPhone.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	order, err := s.findOrder(ctx, code, phone)
	if err != nil {
		// A wrong code is a normal thing for a customer to do, not an outage.
		if errors.Is(err, errOrderNotFound) {
			writeError(w, http.StatusNotFound, errOrderNotFound.Error())
			return
		}
		s.writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": order})
}

func (s *Server) findOrder(ctx context.Context, code, phone string) (*Order, error) {
	var orders []Order
	query := url.Values{}
	query.Set("code", "eq."+code)
	query.Set("phone_e164", "eq."+phone)
	query.Set("select", orderSelect)
	query.Set("limit", "1")

	if err := s.db.Select(ctx, "orders", query, &orders); err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, errOrderNotFound
	}
	if err := s.loadOrderItems(ctx, orders); err != nil {
		return nil, err
	}
	return &orders[0], nil
}

var errOrderNotFound = errors.New("no order matched that code and phone number")

const orderSelect = "id,code,customer_id,phone_e164,customer_name,channel,status,payment_status," +
	"subtotal_inr,discount_inr,shipping_inr,total_inr,address_line1,address_line2,city,pincode,landmark," +
	"delivery_note,internal_note,payment_gateway,payment_ref,payment_transaction_id,created_at"

func (s *Server) loadOrderItems(ctx context.Context, orders []Order) error {
	if len(orders) == 0 {
		return nil
	}

	ids := make([]string, 0, len(orders))
	for _, order := range orders {
		ids = append(ids, strconv.FormatInt(order.ID, 10))
	}

	var items []OrderItem
	query := url.Values{}
	query.Set("order_id", "in.("+strings.Join(ids, ",")+")")
	query.Set("select", "id,order_id,product_id,product_name,pack_size_g,quantity,unit_price_inr,line_total_inr")
	query.Set("order", "id.asc")

	if err := s.db.Select(ctx, "order_items", query, &items); err != nil {
		return err
	}

	byOrder := map[int64][]OrderItem{}
	for _, item := range items {
		byOrder[item.OrderID] = append(byOrder[item.OrderID], item)
	}
	for i := range orders {
		orders[i].Items = byOrder[orders[i].ID]
		if orders[i].Items == nil {
			orders[i].Items = []OrderItem{}
		}
	}
	return nil
}

func (s *Server) handleAdminListOrders(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	query := url.Values{}
	query.Set("select", orderSelect)
	query.Set("order", "created_at.desc")
	query.Set("limit", "200")

	if status := r.URL.Query().Get("status"); status != "" && status != "all" {
		query.Set("status", "eq."+status)
	}
	if payment := r.URL.Query().Get("payment_status"); payment != "" && payment != "all" {
		query.Set("payment_status", "eq."+payment)
	}
	if search := strings.TrimSpace(r.URL.Query().Get("q")); search != "" {
		like := "ilike.*" + escapePostgrestPattern(search) + "*"
		query.Set("or", "(code."+like+",customer_name."+like+",phone_e164."+like+")")
	}

	var orders []Order
	if err := s.db.Select(ctx, "orders", query, &orders); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if err := s.loadOrderItems(ctx, orders); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if orders == nil {
		orders = []Order{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders})
}

func escapePostgrestPattern(value string) string {
	replacer := strings.NewReplacer(",", "", "(", "", ")", "", "*", "")
	return replacer.Replace(value)
}

func (s *Server) handleAdminGetOrder(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	orders, err := s.queryOrders(ctx, url.Values{"id": {"eq." + strconv.FormatInt(id, 10)}})
	if err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if len(orders) == 0 {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": orders[0]})
}

func (s *Server) queryOrders(ctx context.Context, extra url.Values) ([]Order, error) {
	query := url.Values{}
	for key, values := range extra {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	query.Set("select", orderSelect)
	query.Set("order", "created_at.desc")

	var orders []Order
	if err := s.db.Select(ctx, "orders", query, &orders); err != nil {
		return nil, err
	}
	return orders, s.loadOrderItems(ctx, orders)
}

var validOrderStatuses = map[string]bool{
	"new": true, "confirmed": true, "packed": true, "shipped": true,
	"delivered": true, "cancelled": true,
}

var validPaymentStatuses = map[string]bool{
	"pending": true, "paid": true, "cod": true, "failed": true, "refunded": true,
}

type orderUpdateRequest struct {
	Status         *string `json:"status"`
	PaymentStatus  *string `json:"payment_status"`
	InternalNote   *string `json:"internal_note"`
	NotifyCustomer *bool   `json:"notify_customer"`
}

func (s *Server) handleAdminUpdateOrder(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req orderUpdateRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	body := map[string]any{}
	if req.Status != nil {
		if !validOrderStatuses[*req.Status] {
			writeError(w, http.StatusBadRequest, "unknown order status "+*req.Status)
			return
		}
		body["status"] = *req.Status
	}
	if req.PaymentStatus != nil {
		if !validPaymentStatuses[*req.PaymentStatus] {
			writeError(w, http.StatusBadRequest, "unknown payment status "+*req.PaymentStatus)
			return
		}
		body["payment_status"] = *req.PaymentStatus
	}
	if req.InternalNote != nil {
		body["internal_note"] = strings.TrimSpace(*req.InternalNote)
	}

	// Asking for a notification is a request on its own: the Studio sends
	// notify_customer without any other change when it re-sends a receipt.
	notify := req.NotifyCustomer != nil && *req.NotifyCustomer
	if len(body) == 0 && !notify {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	idFilter := url.Values{"id": []string{"eq." + strconv.FormatInt(id, 10)}}

	var order Order
	switch {
	case len(body) > 0:
		var updated []Order
		query := url.Values{}
		query.Set("id", "eq."+strconv.FormatInt(id, 10))
		if err := s.db.Update(ctx, "orders", query, body, &updated); err != nil {
			s.writeUpstreamError(w, err)
			return
		}
		if len(updated) == 0 {
			writeError(w, http.StatusNotFound, "order not found")
			return
		}
		order = updated[0]
	default:
		existing, err := s.queryOrders(ctx, idFilter)
		if err != nil {
			s.writeUpstreamError(w, err)
			return
		}
		if len(existing) == 0 {
			writeError(w, http.StatusNotFound, "order not found")
			return
		}
		order = existing[0]
	}

	queued := false
	if notify {
		s.queueOrderUpdate(ctx, &order, "status: "+order.Status+", payment: "+order.PaymentStatus)
		queued = true
	}

	writeJSON(w, http.StatusOK, map[string]any{"order": order, "notified": queued})
}

func (s *Server) queueOrderUpdate(ctx context.Context, order *Order, note string) {
	body := strings.Builder{}
	body.WriteString("NuttyWonders update for order " + order.Code + ": " + note + ".")
	if order.PaymentStatus == "pending" {
		body.WriteString("\n\nPlease share the UPI payment screenshot here: " + s.setting(ctx, "upi_vpa", ""))
	}
	_ = s.db.Insert(ctx, "whatsapp_jobs", map[string]any{
		"to_phone": order.PhoneE164,
		"body":     body.String(),
		"kind":     "order_update",
		"order_id": order.ID,
		"idempotency_key": "order-update-" + strconv.FormatInt(order.ID, 10) + "-" +
			note + "-" + time.Now().UTC().Format("20060102150405"),
	}, nil)
}

func (s *Server) handleAdminResendOrder(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	orders, err := s.queryOrders(ctx, url.Values{"id": {"eq." + strconv.FormatInt(id, 10)}})
	if err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if len(orders) == 0 {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}

	order := orders[0]
	err = s.db.Insert(ctx, "whatsapp_jobs", map[string]any{
		"to_phone": order.PhoneE164,
		"body":     orderConfirmationMessage(ctx, s, &order),
		"kind":     "order_confirmation",
		"order_id": order.ID,
	}, nil)
	if err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true, "order_code": order.Code})
}

func (s *Server) handleAdminCreateOrder(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	var req checkoutRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	phone, err := normalizePhone(req.Phone)
	if err != nil {
		writeError(w, http.StatusBadRequest, errBadPhone.Error())
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "add at least one product")
		return
	}

	lines := make([]map[string]any, 0, len(req.Items))
	for _, line := range req.Items {
		if line.ProductID <= 0 || line.Quantity < 1 {
			writeError(w, http.StatusBadRequest, "each line needs a product and a quantity")
			return
		}
		lines = append(lines, map[string]any{
			"product_id": line.ProductID,
			"quantity":   clamp(line.Quantity, 1, 50),
		})
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	var result placeOrderResult
	params := map[string]any{
		"p_phone":          phone,
		"p_name":           strings.TrimSpace(req.Name),
		"p_items":          lines,
		"p_address":        req.Address,
		"p_channel":        "manual",
		"p_delivery_note":  strings.TrimSpace(req.Note),
		"p_reply_to_owner": false,
	}
	if err := s.db.RPC(ctx, "place_order", params, &result); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"order": result.Order, "items": result.Items})
}

func (s *Server) handleAdminListCustomers(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	query := url.Values{}
	query.Set("select", "id,phone_e164,name,email,city,pincode,marketing_opt_in,unsubscribed,created_at")
	query.Set("order", "created_at.desc")
	query.Set("limit", "500")

	var customers []Customer
	if err := s.db.Select(ctx, "customers", query, &customers); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if customers == nil {
		customers = []Customer{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"customers": customers})
}

func (s *Server) handleAdminUpdateCustomer(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req struct {
		MarketingOptIn *bool   `json:"marketing_opt_in"`
		Unsubscribed   *bool   `json:"unsubscribed"`
		Name           *string `json:"name"`
		Email          *string `json:"email"`
		City           *string `json:"city"`
		Pincode        *string `json:"pincode"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	body := map[string]any{}
	if req.MarketingOptIn != nil {
		body["marketing_opt_in"] = *req.MarketingOptIn
		if *req.MarketingOptIn {
			body["unsubscribed"] = false
		}
	}
	if req.Unsubscribed != nil {
		body["unsubscribed"] = *req.Unsubscribed
	}
	if req.Name != nil {
		body["name"] = strings.TrimSpace(*req.Name)
	}
	if req.Email != nil {
		body["email"] = strings.TrimSpace(*req.Email)
	}
	if req.City != nil {
		body["city"] = strings.TrimSpace(*req.City)
	}
	if req.Pincode != nil {
		body["pincode"] = strings.TrimSpace(*req.Pincode)
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	query := url.Values{}
	query.Set("id", "eq."+strconv.FormatInt(id, 10))
	var updated []Customer
	if err := s.db.Update(ctx, "customers", query, body, &updated); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if len(updated) == 0 {
		writeError(w, http.StatusNotFound, "customer not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"customer": updated[0]})
}

func (s *Server) handleAdminListJobs(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	query := url.Values{}
	query.Set("select", "id,to_phone,body,kind,status,order_id,broadcast_id,attempts,last_error,claimed_by,created_at,sent_at")
	query.Set("order", "created_at.desc")
	query.Set("limit", "200")

	if status := r.URL.Query().Get("status"); status != "" && status != "all" {
		query.Set("status", "eq."+status)
	}

	var jobs []WhatsAppJob
	if err := s.db.Select(ctx, "whatsapp_jobs", query, &jobs); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if jobs == nil {
		jobs = []WhatsAppJob{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) handleAdminConversations(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	phoneRaw := strings.TrimSpace(r.URL.Query().Get("phone"))
	query := url.Values{}
	query.Set("select", "id,direction,phone_e164,body,status,order_id,created_at")
	query.Set("order", "created_at.desc")

	if phoneRaw != "" {
		phone, err := normalizePhone(phoneRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, errBadPhone.Error())
			return
		}
		query.Set("phone_e164", "eq."+phone)
		query.Set("limit", "100")
	} else {
		query.Set("limit", "50")
	}

	var messages []WhatsAppMessage
	if err := s.db.Select(ctx, "whatsapp_messages", query, &messages); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if messages == nil {
		messages = []WhatsAppMessage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
}

func (s *Server) handleRunRetention(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	results := map[string]any{}
	for name, fn := range map[string]func(context.Context) (int, error){
		"expired_jobs":  func(ctx context.Context) (int, error) { return s.cancelExpiredJobs(ctx) },
		"stale_sending": func(ctx context.Context) (int, error) { return s.releaseStaleJobs(ctx) },
		"old_messages":  func(ctx context.Context) (int, error) { return s.pruneMessages(ctx) },
	} {
		count, err := fn(ctx)
		if err != nil {
			results[name] = map[string]any{"error": err.Error()}
			continue
		}
		results[name] = count
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) setting(ctx context.Context, key, fallback string) string {
	var rows []Setting
	query := url.Values{}
	query.Set("key", "eq."+key)
	query.Set("select", "key,value")
	query.Set("limit", "1")

	if err := s.db.Select(ctx, "app_settings", query, &rows); err != nil || len(rows) == 0 {
		return fallback
	}

	var text string
	if err := json.Unmarshal(rows[0].Value, &text); err != nil {
		return fallback
	}
	if strings.TrimSpace(text) == "" {
		return fallback
	}
	return strings.TrimSpace(text)
}

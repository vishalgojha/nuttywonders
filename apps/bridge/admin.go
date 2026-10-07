package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type messageInput struct {
	Phone   string `json:"phone"`
	Body    string `json:"body"`
	OrderID *int64 `json:"order_id"`
}

func (s *Server) handleWhatsAppStatus(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	state := s.wa.state()

	var rows []Connection
	query := url.Values{}
	query.Set("select", "id,jid,status,qr_code,last_error,updated_at")
	query.Set("id", "eq.1")

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	_ = s.db.Select(ctx, "whatsapp_connection", query, &rows)

	var pendingJobs int
	jobsQuery := url.Values{}
	jobsQuery.Set("status", "eq.queued")
	jobsQuery.Set("select", "id")
	_ = s.db.Select(ctx, "whatsapp_jobs", jobsQuery, &pendingJobs)

	payload := map[string]any{
		"status":        state.Status,
		"jid":           state.JID,
		"qr_code":       state.QRCode,
		"last_error":    state.LastErr,
		"queue_pending": pendingJobs,
		"number":        s.setting(ctx, "owner_phone_e164", ""),
		"push_name":     pushName,
	}
	if len(rows) > 0 {
		payload["updated_at"] = rows[0].UpdatedAt
		if rows[0].LastError != nil && state.LastErr == "" {
			payload["last_error"] = *rows[0].LastError
		}
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleWhatsAppPairCode(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	var req struct {
		Phone string `json:"phone"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Phone == "" {
		req.Phone = s.setting(r.Context(), "owner_phone_e164", "")
	}
	if req.Phone == "" {
		writeError(w, http.StatusBadRequest, "provide the phone number to link")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	code, err := s.wa.RequestPairingCode(ctx, req.Phone)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not request a pairing code: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code})
}

func (s *Server) handleWhatsAppLogout(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	if err := s.wa.Logout(ctx); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logged_out": true})
}

func (s *Server) handleWhatsAppRename(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if err := s.wa.SetPushName(ctx, truncate(name, 24)); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name})
}

func (s *Server) handleWhatsAppSend(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	var req messageInput
	if !decodeJSON(w, r, &req) {
		return
	}

	phone, err := normalizePhone(req.Phone)
	if err != nil {
		writeError(w, http.StatusBadRequest, errBadPhone.Error())
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		writeError(w, http.StatusBadRequest, "message body is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	if err := s.wa.SendText(ctx, phone, body); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "phone": phone})
}

func (s *Server) handleAdminCreateBroadcast(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	var req struct {
		Title   string `json:"title"`
		Body    string `json:"body"`
		Segment string `json:"segment"`
		SendNow bool   `json:"send_now"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Body) == "" {
		writeError(w, http.StatusBadRequest, "a title and message are required")
		return
	}

	segment := req.Segment
	if segment == "" {
		segment = "past_customers"
	}
	if !validBroadcastSegments[segment] {
		writeError(w, http.StatusBadRequest, "unknown audience "+segment)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	recipients, err := s.broadcastAudience(ctx, segment)
	if err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if len(recipients) == 0 {
		writeError(w, http.StatusBadRequest, "no customers match that audience yet")
		return
	}

	status := "draft"
	if req.SendNow {
		status = "queued"
	}

	var created []Broadcast
	insert := map[string]any{
		"title":          strings.TrimSpace(req.Title),
		"body":           strings.TrimSpace(req.Body),
		"segment":        segment,
		"status":         status,
		"audience_count": len(recipients),
	}
	if err := s.db.Insert(ctx, "broadcasts", insert, &created); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if len(created) == 0 {
		writeError(w, http.StatusBadGateway, "broadcast was not created")
		return
	}
	broadcast := created[0]

	if req.SendNow {
		if err := s.queueBroadcast(ctx, &broadcast, recipients); err != nil {
			s.writeUpstreamError(w, err)
			return
		}
		broadcast.Status = "queued"
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"broadcast": broadcast,
		"queued":    req.SendNow,
	})
}

var validBroadcastSegments = map[string]bool{
	"all_customers": true, "opt_in": true, "past_customers": true, "pending_orders": true,
}

func (s *Server) broadcastAudience(ctx context.Context, segment string) ([]string, error) {
	query := url.Values{}
	query.Set("select", "phone_e164")
	query.Set("unsubscribed", "eq.false")

	switch segment {
	case "all_customers":
		query.Set("limit", "5000")
	case "opt_in":
		query.Set("marketing_opt_in", "eq.true")
		query.Set("limit", "5000")
	default:
		// past_customers and pending_orders both start from the full list and
		// are narrowed down against the orders table below.
		query.Set("limit", "5000")
	}

	var customers []Customer
	if err := s.db.Select(ctx, "customers", query, &customers); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	phones := make([]string, 0, len(customers))
	for _, customer := range customers {
		if customer.PhoneE164 == "" || seen[customer.PhoneE164] {
			continue
		}
		seen[customer.PhoneE164] = true
		phones = append(phones, customer.PhoneE164)
	}

	if segment == "past_customers" || segment == "pending_orders" {
		orderQuery := url.Values{}
		orderQuery.Set("select", "phone_e164")
		orderQuery.Set("limit", "5000")
		if segment == "pending_orders" {
			// Anyone still waiting to pay or waiting on their parcel.
			orderQuery.Set("payment_status", "eq.pending")
			orderQuery.Set("status", "in.(new,confirmed,packed,shipped)")
		}

		var orders []struct {
			PhoneE164 string `json:"phone_e164"`
		}
		if err := s.db.Select(ctx, "orders", orderQuery, &orders); err != nil {
			return nil, err
		}
		wanted := map[string]bool{}
		for _, order := range orders {
			wanted[order.PhoneE164] = true
		}

		filtered := make([]string, 0, len(phones))
		for _, phone := range phones {
			if wanted[phone] {
				filtered = append(filtered, phone)
			}
		}
		phones = filtered
	}

	return phones, nil
}

func (s *Server) queueBroadcast(ctx context.Context, broadcast *Broadcast, recipients []string) error {
	for _, phone := range recipients {
		body := map[string]any{
			"to_phone":        phone,
			"body":            broadcast.Body,
			"kind":            "broadcast",
			"broadcast_id":    broadcast.ID,
			"idempotency_key": "broadcast-" + strconv.FormatInt(broadcast.ID, 10) + "-" + phone,
		}
		if err := s.db.Insert(ctx, "whatsapp_jobs", body, nil); err != nil {
			return err
		}
	}
	query := url.Values{}
	query.Set("id", "eq."+strconv.FormatInt(broadcast.ID, 10))
	return s.db.Update(ctx, "broadcasts", query, map[string]any{"status": "queued"}, nil)
}

func (s *Server) handleAdminListBroadcasts(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	query := url.Values{}
	query.Set("select", "id,title,body,segment,status,audience_count,sent_count,created_at,sent_at")
	query.Set("order", "created_at.desc")
	query.Set("limit", "50")

	var broadcasts []Broadcast
	if err := s.db.Select(ctx, "broadcasts", query, &broadcasts); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if broadcasts == nil {
		broadcasts = []Broadcast{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"broadcasts": broadcasts})
}

func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var rows []Setting
	query := url.Values{}
	query.Set("select", "key,value")
	query.Set("order", "key.asc")

	if err := s.db.Select(ctx, "app_settings", query, &rows); err != nil {
		s.writeUpstreamError(w, err)
		return
	}

	settings := map[string]any{}
	for _, row := range rows {
		var text string
		if err := json.Unmarshal(row.Value, &text); err != nil {
			continue
		}
		settings[row.Key] = text
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

var editableSettings = map[string]bool{
	"upi_vpa": true, "upi_payee_name": true, "owner_phone_e164": true,
	"app_base_url": true, "delivery_fee_inr": true, "free_shipping_over_inr": true,
	"payment_instructions": true, "support_email": true, "business_hours": true,
	"announcement": true, "instagram_url": true,
}

func (s *Server) handleAdminSaveSetting(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	key := r.PathValue("key")
	if !editableSettings[key] {
		writeError(w, http.StatusBadRequest, "unknown setting "+key)
		return
	}

	var req struct {
		Value string `json:"value"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	value := strings.TrimSpace(req.Value)

	if key == "owner_phone_e164" && value != "" {
		phone, err := normalizePhone(value)
		if err != nil {
			writeError(w, http.StatusBadRequest, errBadPhone.Error())
			return
		}
		value = phone
	}
	if key == "upi_vpa" && value != "" && !strings.Contains(value, "@") {
		writeError(w, http.StatusBadRequest, "a UPI id looks like name@bank")
		return
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save that setting")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var saved []Setting
	if err := s.db.Upsert(ctx, "app_settings", map[string]any{"key": key, "value": encoded}, &saved); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": value})
}

func (s *Server) handleAdminMe(w http.ResponseWriter, r *http.Request, identity adminIdentity) {
	writeJSON(w, http.StatusOK, map[string]any{
		"email":   identity.Email,
		"user_id": identity.UserID,
		"role":    "admin",
	})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	head := url.Values{}
	head.Set("select", "id,code,customer_name,status,payment_status,total_inr,channel,created_at")
	head.Set("order", "created_at.desc")
	head.Set("limit", "8")

	var recent []Order
	if err := s.db.Select(ctx, "orders", head, &recent); err != nil {
		s.writeUpstreamError(w, err)
		return
	}

	counts := map[string]any{}
	for _, name := range []string{"orders", "customers", "products"} {
		query := url.Values{}
		n, err := s.db.Count(ctx, name, query)
		if err != nil {
			s.log.Warn("could not count table", "table", name, "error", err)
			counts[name] = nil
			continue
		}
		counts[name] = n
	}

	byStatus := map[string]int{}
	for _, status := range []string{"new", "confirmed", "packed", "shipped", "delivered", "cancelled"} {
		query := url.Values{}
		query.Set("status", "eq."+status)
		n, err := s.db.Count(ctx, "orders", query)
		if err != nil {
			s.log.Warn("could not count orders by status", "status", status, "error", err)
			continue
		}
		byStatus[status] = n
	}

	pendingJobs, err := s.db.Count(ctx, "whatsapp_jobs", url.Values{"status": []string{"eq.queued"}})
	if err != nil {
		s.log.Warn("could not count queued whatsapp jobs", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"recent_orders":    recent,
		"counts":           counts,
		"orders_by_status": byStatus,
		"queue_pending":    pendingJobs,
		"whatsapp":         s.wa.state(),
		"pending_payments": s.pendingPaymentCount(ctx),
	})
}

func (s *Server) pendingPaymentCount(ctx context.Context) int {
	query := url.Values{}
	query.Set("payment_status", "eq.pending")
	query.Set("status", "neq.cancelled")
	n, err := s.db.Count(ctx, "orders", query)
	if err != nil {
		s.log.Warn("could not count pending payments", "error", err)
		return 0
	}
	return n
}

// The maintenance functions return a single integer, which PostgREST sends as a
// bare JSON number rather than a row, so they cannot share the row decoding
// path.
func (s *Server) cancelExpiredJobs(ctx context.Context) (int, error) {
	return s.db.RPCScalar(ctx, "cleanup_expired_jobs", map[string]any{})
}

func (s *Server) releaseStaleJobs(ctx context.Context) (int, error) {
	return s.db.RPCScalar(ctx, "release_stale_jobs", map[string]any{"p_older_than": "PT10M"})
}

func (s *Server) pruneMessages(ctx context.Context) (int, error) {
	return s.db.RPCScalar(ctx, "prune_whatsapp_messages", map[string]any{"p_keep_days": 120})
}

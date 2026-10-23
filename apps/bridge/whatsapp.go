package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	// Registers the "sqlite3" database/sql driver that the WhatsApp session
	// store uses. It is cgo-based, which is why the bridge image sets CGO_ENABLED=1.
	_ "github.com/mattn/go-sqlite3"
)

const pushName = "NuttyWonders"

// WhatsApp validates this separately from the push name. PairPhone expects a
// browser/OS label in the form "Browser (OS)".
const pairClientDisplayName = "Chrome (Linux)"

type inboundHandler func(ctx context.Context, phone, body string)

type whatsapp struct {
	db     *Client
	log    waLog.Logger
	config Config

	store   *sqlstore.Container
	inbound inboundHandler

	mu      sync.RWMutex
	client  *whatsmeow.Client
	status  string
	jid     string
	qr      string
	// pairingReady is set only after WhatsMeow has emitted the first QR code.
	// PairPhone must not be called before that point: the pre-login websocket
	// may exist but still be unable to accept the pairing IQ request.
	pairingReady bool
	lastErr string
}

func newWhatsApp(cfg Config, db *Client, log waLog.Logger) (*whatsapp, error) {
	container, err := sqlstore.New(context.Background(), cfg.WhatsAppDialect, cfg.WhatsAppDB, log)
	if err != nil {
		return nil, fmt.Errorf("open whatsmeow store: %w", err)
	}
	return &whatsapp{db: db, log: log, config: cfg, store: container, status: "disconnected"}, nil
}

func (w *whatsapp) cli() *whatsmeow.Client {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.client
}

type whatsappState struct {
	Status  string `json:"status"`
	JID     string `json:"jid"`
	QRCode  string `json:"qr_code"`
	LastErr string `json:"last_error"`
}

func (w *whatsapp) state() whatsappState {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return whatsappState{Status: w.status, JID: w.jid, QRCode: w.qr, LastErr: w.lastErr}
}

func (w *whatsapp) setState(status, jid, qrCode string, cause error) {
	w.mu.Lock()
	w.status = status
	w.jid = jid
	w.qr = qrCode
	switch {
	case cause != nil:
		w.lastErr = cause.Error()
	case status == "connected":
		w.lastErr = ""
	}
	w.mu.Unlock()

	payload := map[string]any{
		"id":         1,
		"status":     status,
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if jid != "" {
		payload["jid"] = jid
	}
	if qrCode != "" {
		payload["qr_code"] = qrCode
	}
	if cause != nil {
		payload["last_error"] = cause.Error()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.db.Upsert(ctx, "whatsapp_connection", payload, nil); err != nil {
		w.log.Warnf("could not persist whatsapp state: %v", err)
	}
}

// Start resumes the paired session, or starts pairing when there is none.
func (w *whatsapp) Start(ctx context.Context) error {
	device, err := w.store.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("load whatsapp device: %w", err)
	}

	// Store.ID is the correct signal for "already paired". IsLoggedIn() only
	// turns true after a live connection, so it must not be used here.
	if device.ID == nil {
		return w.pairLoop(ctx)
	}

	cli := whatsmeow.NewClient(device, w.log)
	cli.AutoTrustIdentity = true
	cli.EnableAutoReconnect = true
	cli.AddEventHandler(w.handleEvent)

	w.mu.Lock()
	w.client = cli
	w.mu.Unlock()

	jid := device.ID.String()
	// The connection row only allows disconnected/pairing/connected/error, so
	// report the linked-but-not-yet-verified device as "pairing" until the
	// Connected event lands.
	w.setState("pairing", jid, "", nil)
	if err := cli.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	w.log.Infof("whatsapp connected as %s", jid)
	return nil
}

// pairLoop keeps a fresh QR code available until the phone is paired.
//
// WhatsMeow closes the QR channel on the first socket drop, so a single
// GetQRChannel call leaves Studio showing an expired code forever. Every time
// the channel closes without pairing we build a new client and try again.
func (w *whatsapp) pairLoop(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		device, err := w.store.GetFirstDevice(ctx)
		if err != nil {
			return fmt.Errorf("load whatsapp device: %w", err)
		}
		// Pairing may have completed between iterations.
		if device.ID != nil {
			cli := whatsmeow.NewClient(device, w.log)
			cli.AutoTrustIdentity = true
			cli.EnableAutoReconnect = true
			cli.AddEventHandler(w.handleEvent)
			w.mu.Lock()
			w.client = cli
			w.mu.Unlock()

			jid := device.ID.String()
			w.setState("pairing", jid, "", nil)
			if err := cli.Connect(); err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			w.log.Infof("whatsapp connected as %s", jid)
			return nil
		}

		cli := whatsmeow.NewClient(device, w.log)
		cli.AutoTrustIdentity = true
		cli.EnableAutoReconnect = true
		cli.AddEventHandler(w.handleEvent)

		w.mu.Lock()
		w.client = cli
		w.pairingReady = false
		w.mu.Unlock()

		qrChan, err := cli.GetQRChannel(ctx)
		if err != nil {
			cli.Disconnect()
			w.log.Warnf("could not open whatsapp qr channel: %v", err)
			if !sleepCtx(ctx, 2*time.Second) {
				return ctx.Err()
			}
			continue
		}

		w.setState("pairing", "", "", nil)
		if err := cli.Connect(); err != nil {
			cli.Disconnect()
			w.log.Warnf("whatsapp pairing connect failed: %v", err)
			if !sleepCtx(ctx, 3*time.Second) {
				return ctx.Err()
			}
			continue
		}
		w.log.Infof("whatsapp pairing started, scan the QR code from Studio > WhatsApp")

		paired := false
		hardError := false
		for item := range qrChan {
			switch item.Event {
			case "code":
				w.mu.Lock()
				w.pairingReady = true
				w.mu.Unlock()
				w.setState("pairing", "", item.Code, nil)
			case "success":
				paired = true
				w.mu.Lock()
				w.pairingReady = false
				w.mu.Unlock()
				jid := ""
				if cli.Store.ID != nil {
					jid = cli.Store.ID.String()
				}
				w.setState("connected", jid, "", nil)
			case "timeout":
				w.mu.Lock()
				w.pairingReady = false
				w.mu.Unlock()
				w.log.Warnf("qr code expired, requesting a fresh one")
				w.setState("pairing", "", "", nil)
			case "error":
				hardError = true
				w.mu.Lock()
				w.pairingReady = false
				w.mu.Unlock()
				w.setState("error", "", "", item.Error)
			}
		}

		if paired {
			w.log.Infof("whatsapp paired successfully")
			return nil
		}

		cli.Disconnect()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A dropped socket is normal and recovers immediately; a protocol
		// error deserves a short backoff so we do not hammer WhatsApp.
		delay := 1500 * time.Millisecond
		if hardError {
			delay = 5 * time.Second
		}
		if !sleepCtx(ctx, delay) {
			return ctx.Err()
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (w *whatsapp) handleEvent(raw any) {
	switch evt := raw.(type) {
	case *events.Connected:
		jid := ""
		if cli := w.cli(); cli != nil && cli.Store.ID != nil {
			jid = cli.Store.ID.String()
		}
		w.log.Infof("whatsapp socket connected")
		w.setState("connected", jid, "", nil)

	case *events.Disconnected:
		w.mu.Lock()
		w.pairingReady = false
		w.mu.Unlock()
		if cli := w.cli(); cli != nil && !cli.Store.Deleted {
			w.setState("disconnected", "", "", nil)
		}

	case *events.LoggedOut:
		w.setState("disconnected", "", "", errors.New("session logged out, scan a new QR code"))

	case *events.StreamReplaced:
		w.setState("disconnected", "", "", errors.New("replaced by another connection, is a second bridge running?"))

	case *events.PairSuccess:
		w.mu.Lock()
		w.pairingReady = false
		w.mu.Unlock()
		w.log.Infof("whatsapp paired as %s", evt.ID.String())
		w.setState("connected", evt.ID.String(), "", nil)

	case *events.Message:
		w.handleInbound(evt)
	}
}

func (w *whatsapp) handleInbound(evt *events.Message) {
	if evt.Info.Chat.Server == types.HiddenUserServer {
		return
	}

	cli := w.cli()
	var own *types.JID
	if cli != nil {
		own = cli.Store.ID
	}
	selfChat := isSelfChat(evt.Info.Chat, own)

	// Messages the owner sends to customers from the paired phone are the
	// owner's own replies and must never be answered by the bot. The only
	// exception is the self-chat ("Message yourself"), which the admin can use
	// as a private session with the bot.
	if evt.Info.IsFromMe && !selfChat {
		return
	}
	if !selfChat && own != nil && evt.Info.Sender.User == own.User {
		return
	}

	body := evt.Message.GetConversation()
	if strings.TrimSpace(body) == "" {
		return
	}

	phone := phoneFromJID(evt.Info.Sender)
	if selfChat && phone == "" && own != nil {
		phone = phoneFromJID(*own)
	}
	if phone == "" {
		return
	}

	// Self-chat is admin-only: if the paired account is not the configured
	// admin number, treat the message as owner-to-customer noise and drop it.
	if selfChat && (w.config.AdminPhone == "" || phone != w.config.AdminPhone) {
		return
	}
	messageType := "text"
	paymentReviewStatus := ""
	if strings.TrimSpace(body) == "" {
		if evt.Message.GetImageMessage() == nil {
			return
		}
		messageType = "image"
		body = "[Image received — review this as possible payment proof]"
		paymentReviewStatus = "pending"
	} else if containsPaymentProofLanguage(body) {
		paymentReviewStatus = "pending"
	}

	logCtx, cancelLog := context.WithTimeout(context.Background(), 15*time.Second)
	message := map[string]any{
		"direction":   "in",
		"phone_e164":  phone,
		"body":        body,
		"message_key": evt.Info.ID,
		"status":      "received",
		"message_type": messageType,
	}
	if paymentReviewStatus != "" {
		message["payment_review_status"] = paymentReviewStatus
	}
	_ = w.db.Insert(logCtx, "whatsapp_messages", message, nil)
	cancelLog()

	if cli != nil {
		readCtx, cancelRead := context.WithTimeout(context.Background(), 10*time.Second)
		_ = cli.MarkRead(readCtx, []types.MessageID{evt.Info.ID}, evt.Info.Timestamp, evt.Info.Chat, evt.Info.Sender)
		cancelRead()
	}

	if w.inbound == nil {
		return
	}
	replyCtx, cancelReply := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReply()
	w.inbound(replyCtx, phone, body)
}

func containsPaymentProofLanguage(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "paid") || strings.Contains(lower, "payment") ||
		strings.Contains(lower, "screenshot") || strings.Contains(lower, "transaction") ||
		strings.Contains(lower, "utr")
}

// isSelfChat reports whether a message was sent in the account's own
// "Message yourself" chat rather than to a customer.
func isSelfChat(chat types.JID, own *types.JID) bool {
	if own == nil {
		return false
	}
	return chat.Server == types.DefaultUserServer && chat.User == own.User
}

func phoneFromJID(jid types.JID) string {
	user := strings.TrimSuffix(jid.User, "@"+types.DefaultUserServer)
	phone, err := normalizePhone(user)
	if err != nil {
		return ""
	}
	return phone
}

var errNotConnected = errors.New("whatsapp is not connected yet")

// SendText delivers one text message, splitting it if the body is long.
func (w *whatsapp) SendText(ctx context.Context, phone, body string) error {
	cli := w.cli()
	if cli == nil || !cli.IsConnected() || !cli.IsLoggedIn() {
		return errNotConnected
	}
	if strings.TrimSpace(body) == "" {
		return errors.New("refusing to send an empty message")
	}

	jid := types.NewJID(strings.TrimPrefix(phone, "+"), types.DefaultUserServer)

	var lastID types.MessageID
	for _, part := range splitForWhatsApp(body) {
		text := part
		resp, err := cli.SendMessage(ctx, jid, &waE2E.Message{Conversation: &text})
		if err != nil {
			return fmt.Errorf("send to %s: %w", phone, err)
		}
		lastID = resp.ID
	}

	logCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = w.db.Insert(logCtx, "whatsapp_messages", map[string]any{
		"direction":   "out",
		"phone_e164":  phone,
		"body":        body,
		"message_key": lastID,
		"status":      "sent",
	}, nil)
	return nil
}

// splitForWhatsApp keeps each part under the point where WhatsApp starts
// rejecting a single message as too long.
func splitForWhatsApp(body string) []string {
	const limit = 3600
	if len(body) <= limit {
		return []string{body}
	}

	var parts []string
	rest := body
	for len(rest) > limit {
		cut := rest[:limit]
		if idx := strings.LastIndex(cut, "\n"); idx > limit/2 {
			cut = cut[:idx]
		}
		parts = append(parts, strings.TrimSpace(cut))
		rest = rest[len(cut):]
	}
	if tail := strings.TrimSpace(rest); tail != "" {
		parts = append(parts, tail)
	}
	return parts
}

// Logout forgets the paired device so a different number can be linked.
func (w *whatsapp) Logout(ctx context.Context) error {
	cli := w.cli()
	if cli == nil {
		return errNotConnected
	}
	if cli.Store.ID == nil {
		return errors.New("no device is paired yet")
	}
	if err := cli.Logout(ctx); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	cli.Disconnect()

	w.mu.Lock()
	w.jid = ""
	w.qr = ""
	w.status = "disconnected"
	w.lastErr = ""
	w.mu.Unlock()

	w.setState("disconnected", "", "", nil)
	return nil
}

// RequestPairingCode returns the 8-digit code a customer can type into
// WhatsApp > Linked devices, for phones that cannot scan the QR code.
func (w *whatsapp) RequestPairingCode(ctx context.Context, phone string) (string, error) {
	cli := w.cli()
	if cli == nil {
		return "", errors.New("whatsapp pairing is still starting; wait a moment and try again")
	}
	normalised, err := normalizePhone(phone)
	if err != nil {
		return "", err
	}

	// WhatsMeow documents that PairPhone must be called after the first QR
	// event. Waiting here also covers the short reconnect window after the
	// bridge starts, instead of exposing its low-level "websocket not
	// connected" error to the admin UI.
	deadline := time.NewTicker(50 * time.Millisecond)
	defer deadline.Stop()
	for {
		w.mu.RLock()
		ready := w.pairingReady
		w.mu.RUnlock()
		if ready && cli.IsConnected() {
			break
		}
		select {
		case <-ctx.Done():
			return "", errors.New("WhatsApp pairing is not ready yet; wait for a fresh QR code and try again")
		case <-deadline.C:
		}
	}
	return cli.PairPhone(ctx, normalised, true, whatsmeow.PairClientOtherWebClient, pairClientDisplayName)
}

// SetPushName renames the chat list entry customers see.
func (w *whatsapp) SetPushName(ctx context.Context, name string) error {
	cli := w.cli()
	if cli == nil || !cli.IsConnected() {
		return errNotConnected
	}
	if cli.Store.ID == nil {
		return errNotConnected
	}

	sqlStore := sqlstore.NewSQLStore(w.store, *cli.Store.ID)
	if _, _, err := sqlStore.PutPushName(ctx, *cli.Store.ID, name); err != nil {
		return err
	}
	cli.Store.PushName = name
	return nil
}

func (w *whatsapp) Close() {
	if cli := w.cli(); cli != nil {
		cli.Disconnect()
	}
	if w.store != nil {
		_ = w.store.Close()
	}
}

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

	// repair tells Run that the session it is holding is gone and a fresh
	// pairing needs to start. Buffered by one: the request is idempotent.
	repair chan struct{}

	mu     sync.RWMutex
	client *whatsmeow.Client
	status string
	jid    string
	qr     string
	// pairingReady is set only after WhatsMeow has emitted the first QR code.
	// PairPhone must not be called before that point: the pre-login websocket
	// may exist but still be unable to accept the pairing IQ request.
	pairingReady bool
	lastErr      string
}

func newWhatsApp(cfg Config, db *Client, log waLog.Logger) (*whatsapp, error) {
	container, err := sqlstore.New(context.Background(), cfg.WhatsAppDialect, cfg.WhatsAppDB, log)
	if err != nil {
		return nil, fmt.Errorf("open whatsmeow store: %w", err)
	}
	return &whatsapp{db: db, log: log, config: cfg, store: container, status: "disconnected", repair: make(chan struct{}, 1)}, nil
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
	// Written every time, so a code that expired or was superseded does not
	// stay in the connection row looking scannable.
	payload["qr_code"] = nil
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

// Run keeps a usable WhatsApp session for the life of the process.
//
// WhatsMeow deletes the local credentials when the phone unlinks the device and
// does not pair again by itself, so without this loop the bridge would report
// "disconnected" with no QR code until the next container restart.
func (w *whatsapp) Run(ctx context.Context) {
	backoff := 5 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}

		err := w.Start(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.log.Warnf("whatsapp session failed: %v, retrying in %s", err, backoff)
			w.setState("error", "", "", err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			if backoff < 2*time.Minute {
				backoff *= 2
			}
			continue
		}

		backoff = 5 * time.Second
		// A session is live. Drop any repair request queued while it was being
		// established, then wait for the next one.
		select {
		case <-w.repair:
		default:
		}

		select {
		case <-ctx.Done():
			return
		case <-w.repair:
			w.log.Infof("whatsapp session ended, pairing again")
		}
	}
}

// requestRepair hands the session back to Run so pairing can start over.
func (w *whatsapp) requestRepair() {
	select {
	case w.repair <- struct{}{}:
	default:
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
			w.setState("error", "", "", fmt.Errorf("could not open whatsapp qr channel: %w", err))
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
		w.requestRepair()

	case *events.StreamReplaced:
		w.setState("disconnected", "", "", errors.New("replaced by another connection, is a second bridge running?"))

	// Bans and connection refusals land on the server the pairing code is
	// scanned from, so say so instead of leaving Studio on a bare "Offline".
	case *events.TemporaryBan:
		w.setState("error", "", "", errors.New(evt.String()))

	case *events.ConnectFailure:
		w.setState("error", "", "", fmt.Errorf("whatsapp rejected the connection: %v (%s)", evt.Reason, evt.Message))

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
	cli := w.cli()
	var own *types.JID
	var ownLID *types.JID
	if cli != nil {
		own = cli.Store.ID
		lid := cli.Store.GetLID()
		if !lid.IsEmpty() {
			ownLID = &lid
		}
	}
	selfChat := isSelfChat(evt.Info.Chat, own, ownLID)
	// The customer bot is only for one-to-one phone-number chats. Group,
	// broadcast, status and newsletter events must never enter the customer
	// flow: their sender can otherwise be mistaken for an individual customer
	// and receive an unrelated reply.
	//
	// The paired account's own LID is the one exception, because WhatsApp can
	// represent "Message yourself" with the hidden-user server.
	if !isSupportedInboundChat(evt.Info.Chat, selfChat) {
		return
	}

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

	body := messageText(evt.Message)

	phone := phoneFromJID(evt.Info.Sender)
	if phone == "" {
		phone = phoneFromJID(evt.Info.SenderAlt)
	}
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
		"direction":    "in",
		"phone_e164":   phone,
		"body":         body,
		"message_key":  evt.Info.ID,
		"status":       "received",
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
	if selfChat {
		replyCtx = withAdminSelfChat(replyCtx, evt.Info.Chat)
	}
	w.inbound(replyCtx, phone, body)
}

// messageText handles both the original conversation field and WhatsApp's
// extended text field. Linked-device/self-chat messages can use either form.
func messageText(message *waE2E.Message) string {
	if message == nil {
		return ""
	}
	if text := message.GetConversation(); text != "" {
		return text
	}
	if extended := message.GetExtendedTextMessage(); extended != nil {
		return extended.GetText()
	}
	return ""
}

func containsPaymentProofLanguage(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "paid") || strings.Contains(lower, "payment") ||
		strings.Contains(lower, "screenshot") || strings.Contains(lower, "transaction") ||
		strings.Contains(lower, "utr")
}

// isSelfChat reports whether a message was sent in the account's own
// "Message yourself" chat rather than to a customer.
func isSelfChat(chat types.JID, own, ownLID *types.JID) bool {
	if own != nil && chat.Server == own.Server && chat.User == own.User {
		return true
	}
	return ownLID != nil && chat.Server == ownLID.Server && chat.User == ownLID.User
}

func isSupportedInboundChat(chat types.JID, selfChat bool) bool {
	return selfChat || chat.Server == types.DefaultUserServer
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
	return w.sendTextToJID(ctx, types.NewJID(strings.TrimPrefix(phone, "+"), types.DefaultUserServer), phone, body)
}

// SendSelfChat delivers a reply to the exact self-chat JID WhatsApp supplied
// for the inbound event. This matters when the chat is represented by an LID
// rather than the account's phone-number JID.
func (w *whatsapp) SendSelfChat(ctx context.Context, jid types.JID, phone, body string) error {
	return w.sendTextToJID(ctx, jid, phone, body)
}

func (w *whatsapp) sendTextToJID(ctx context.Context, jid types.JID, phone, body string) error {
	cli := w.cli()
	if cli == nil || !cli.IsConnected() || !cli.IsLoggedIn() {
		return errNotConnected
	}
	if strings.TrimSpace(body) == "" {
		return errors.New("refusing to send an empty message")
	}

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
	w.client = nil
	w.mu.Unlock()

	w.setState("disconnected", "", "", nil)
	w.requestRepair()
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

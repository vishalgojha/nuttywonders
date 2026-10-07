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

	cli := whatsmeow.NewClient(device, w.log)
	cli.AutoTrustIdentity = true
	cli.EnableAutoReconnect = true
	cli.AddEventHandler(w.handleEvent)

	w.mu.Lock()
	w.client = cli
	w.mu.Unlock()

	if cli.Store.ID == nil || !cli.IsLoggedIn() {
		return w.startPairing(ctx, cli)
	}

	jid := cli.Store.ID.String()
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

func (w *whatsapp) startPairing(ctx context.Context, cli *whatsmeow.Client) error {
	qrChan, err := cli.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("qr channel: %w", err)
	}

	w.setState("pairing", "", "", nil)

	_, cancelPair := context.WithCancel(ctx)
	go func() {
		defer cancelPair()
		for item := range qrChan {
			switch item.Event {
			case "code":
				w.setState("pairing", "", item.Code, nil)
			case "success":
				jid := ""
				if cli.Store.ID != nil {
					jid = cli.Store.ID.String()
				}
				w.setState("connected", jid, "", nil)
			case "timeout":
				w.log.Warnf("qr code expired, waiting for the next one")
			case "error":
				w.setState("error", "", "", item.Error)
			}
		}
	}()

	if err := cli.Connect(); err != nil {
		cancelPair()
		return fmt.Errorf("connect: %w", err)
	}
	w.log.Infof("whatsapp pairing started, scan the QR code from Studio > WhatsApp")
	return nil
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
		if cli := w.cli(); cli != nil && !cli.Store.Deleted {
			w.setState("disconnected", "", "", nil)
		}

	case *events.LoggedOut:
		w.setState("disconnected", "", "", errors.New("session logged out, scan a new QR code"))

	case *events.StreamReplaced:
		w.setState("disconnected", "", "", errors.New("replaced by another connection, is a second bridge running?"))

	case *events.PairSuccess:
		w.log.Infof("whatsapp paired as %s", evt.ID.String())
		w.setState("connected", evt.ID.String(), "", nil)

	case *events.Message:
		w.handleInbound(evt)
	}
}

func (w *whatsapp) handleInbound(evt *events.Message) {
	if evt.Info.IsFromMe || evt.Info.Chat.Server == types.HiddenUserServer {
		return
	}
	body := evt.Message.GetConversation()
	if strings.TrimSpace(body) == "" {
		return
	}

	cli := w.cli()
	if cli != nil && cli.Store.ID != nil && evt.Info.Sender.User == cli.Store.ID.User {
		return
	}

	phone := phoneFromJID(evt.Info.Sender)
	if phone == "" {
		return
	}

	logCtx, cancelLog := context.WithTimeout(context.Background(), 15*time.Second)
	_ = w.db.Insert(logCtx, "whatsapp_messages", map[string]any{
		"direction":   "in",
		"phone_e164":  phone,
		"body":        body,
		"message_key": evt.Info.ID,
		"status":      "received",
	}, nil)
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
		return "", errNotConnected
	}
	normalised, err := normalizePhone(phone)
	if err != nil {
		return "", err
	}
	return cli.PairPhone(ctx, normalised, true, whatsmeow.PairClientOtherWebClient, pushName)
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

package main

import (
	"encoding/json"
	"time"
)

type Product struct {
	ID                int64    `json:"id"`
	Slug              string   `json:"slug"`
	Name              string   `json:"name"`
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
	CostTotalINR      float64  `json:"cost_total_inr"`
	MarginPct         *float64 `json:"margin_pct"`
	StockPacks        int      `json:"stock_packs"`
	IsActive          bool     `json:"is_active"`
	SortOrder         int      `json:"sort_order"`
}

func (p Product) packLabel() string {
	return itoa(p.PackSizeG) + " g pack"
}

type OrderItem struct {
	ID           int64   `json:"id"`
	OrderID      int64   `json:"order_id"`
	ProductID    *int64  `json:"product_id"`
	ProductName  string  `json:"product_name"`
	PackSizeG    int     `json:"pack_size_g"`
	Quantity     int     `json:"quantity"`
	UnitPriceINR float64 `json:"unit_price_inr"`
	LineTotalINR float64 `json:"line_total_inr"`
}

type Order struct {
	ID                   int64       `json:"id"`
	Code                 string      `json:"code"`
	CustomerID           *int64      `json:"customer_id"`
	PhoneE164            string      `json:"phone_e164"`
	CustomerName         string      `json:"customer_name"`
	Channel              string      `json:"channel"`
	Status               string      `json:"status"`
	PaymentStatus        string      `json:"payment_status"`
	SubtotalINR          float64     `json:"subtotal_inr"`
	DiscountINR          float64     `json:"discount_inr"`
	ShippingINR          float64     `json:"shipping_inr"`
	TotalINR             float64     `json:"total_inr"`
	AddressLine1         *string     `json:"address_line1"`
	AddressLine2         *string     `json:"address_line2"`
	City                 *string     `json:"city"`
	Pincode              *string     `json:"pincode"`
	Landmark             *string     `json:"landmark"`
	DeliveryNote         *string     `json:"delivery_note"`
	InternalNote         *string     `json:"internal_note"`
	CreatedAt            time.Time   `json:"created_at"`
	PaymentGateway       *string     `json:"payment_gateway"`
	PaymentRef           *string     `json:"payment_ref"`
	PaymentTransactionID *string     `json:"payment_transaction_id"`
	Items                []OrderItem `json:"items"`
}

type Customer struct {
	ID             int64     `json:"id"`
	PhoneE164      string    `json:"phone_e164"`
	Name           *string   `json:"name"`
	Email          *string   `json:"email"`
	City           *string   `json:"city"`
	Pincode        *string   `json:"pincode"`
	MarketingOptIn bool      `json:"marketing_opt_in"`
	Unsubscribed   bool      `json:"unsubscribed"`
	CreatedAt      time.Time `json:"created_at"`
}

type WhatsAppJob struct {
	ID          int64   `json:"id"`
	ToPhone     string  `json:"to_phone"`
	Body        string  `json:"body"`
	Kind        string  `json:"kind"`
	Status      string  `json:"status"`
	OrderID     *int64  `json:"order_id"`
	BroadcastID *int64  `json:"broadcast_id"`
	Attempts    int     `json:"attempts"`
	LastError   *string `json:"last_error"`
	ClaimedBy   *string `json:"claimed_by"`
}

type WhatsAppMessage struct {
	ID        int64     `json:"id"`
	Direction string    `json:"direction"`
	PhoneE164 string    `json:"phone_e164"`
	Body      *string   `json:"body"`
	Status    string    `json:"status"`
	OrderID   *int64    `json:"order_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Broadcast struct {
	ID            int64      `json:"id"`
	Title         string     `json:"title"`
	Body          string     `json:"body"`
	Segment       string     `json:"segment"`
	Status        string     `json:"status"`
	AudienceCount int        `json:"audience_count"`
	SentCount     int        `json:"sent_count"`
	CreatedAt     time.Time  `json:"created_at"`
	SentAt        *time.Time `json:"sent_at"`
}

type Connection struct {
	ID        int64           `json:"id"`
	JID       *string         `json:"jid"`
	Status    string          `json:"status"`
	Device    json.RawMessage `json:"device"`
	QRCode    *string         `json:"qr_code"`
	LastError *string         `json:"last_error"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type Setting struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

type placeOrderResult struct {
	Order Order       `json:"order"`
	Items []OrderItem `json:"items"`
}

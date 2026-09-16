package web

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---------------------------------------------------------------------------
// Cart, checkout and order lifecycle: short transactions that change a few
// rows, lock what they touch and must never lose a write. Classic OLTP.
// ---------------------------------------------------------------------------

type cartLine struct {
	ProductID int64   `db:"product_id"`
	Name      string  `db:"name"`
	Brand     string  `db:"brand"`
	Quantity  int32   `db:"quantity"`
	PriceUSD  float64 `db:"price_usd"`
	Stock     int64   `db:"stock"`
}

const sqlCartLines = `-- cart lines with current prices and total stock
SELECT ci.product_id, p.name, b.name AS brand, ci.quantity, p.price_usd::float8 AS price_usd,
       (SELECT coalesce(sum(i.quantity_on_hand), 0) FROM inventory i WHERE i.product_id = p.id) AS stock
FROM carts ca
JOIN cart_items ci ON ci.cart_id = ca.id
JOIN products p ON p.id = ci.product_id
JOIN brands b ON b.id = p.brand_id
WHERE ca.customer_id = $1
ORDER BY ci.added_at, ci.product_id`

type cartData struct {
	Lines       []cartLine
	SubtotalUSD float64
	ShippingUSD float64
	TaxUSD      float64
	TotalUSD    float64
}

func (s *Server) cart(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	lines, err := collect[cartLine](r.Context(), s, sqlCartLines, p.Customer.ID)
	if err != nil {
		return nil, err
	}
	d := cartData{Lines: lines}
	for _, l := range lines {
		d.SubtotalUSD += l.PriceUSD * float64(l.Quantity)
	}
	if len(lines) > 0 && p.Customer.Segment != "vip" && d.SubtotalUSD < 50 {
		d.ShippingUSD = 5.99
	}
	d.TaxUSD = d.SubtotalUSD * p.Customer.TaxRate
	d.TotalUSD = d.SubtotalUSD + d.ShippingUSD + d.TaxUSD
	return &view{Template: "cart", Title: "Cart", Data: d}, nil
}

func (s *Server) cartAdd(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	ctx := r.Context()
	productID, _ := strconv.ParseInt(r.FormValue("product_id"), 10, 64)
	qty := min(max(atoi(r.FormValue("quantity")), 1), 99)
	err := pgx.BeginFunc(ctx, s.pg, func(tx pgx.Tx) error {
		var cartID int64
		if err := tx.QueryRow(ctx, `-- get or create the customer's cart (UPSERT; SQL Server would use MERGE)
INSERT INTO carts (customer_id) VALUES ($1)
ON CONFLICT (customer_id) DO UPDATE SET updated_at = now()
RETURNING id`, p.Customer.ID).Scan(&cartID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `-- add the product, or add to its quantity if it is already in the cart
INSERT INTO cart_items (cart_id, product_id, quantity) VALUES ($1, $2, $3)
ON CONFLICT (cart_id, product_id)
DO UPDATE SET quantity = cart_items.quantity + EXCLUDED.quantity`, cartID, productID, qty)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.keepQueries(w, r, "add to cart")
	return &view{Redirect: fmt.Sprintf("/p/%d", productID), Flash: "Added to your cart."}, nil
}

func (s *Server) cartUpdate(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	ctx := r.Context()
	productID, _ := strconv.ParseInt(r.FormValue("product_id"), 10, 64)
	qty := min(max(atoi(r.FormValue("quantity")), 0), 99)
	var err error
	if qty == 0 {
		_, err = s.pg.Exec(ctx, `-- remove a line (the join through carts makes sure it is this customer's cart)
DELETE FROM cart_items ci
USING carts ca
WHERE ca.id = ci.cart_id AND ca.customer_id = $1 AND ci.product_id = $2`, p.Customer.ID, productID)
	} else {
		_, err = s.pg.Exec(ctx, `-- change a quantity
UPDATE cart_items ci SET quantity = $3
FROM carts ca
WHERE ca.id = ci.cart_id AND ca.customer_id = $1 AND ci.product_id = $2`, p.Customer.ID, productID, qty)
	}
	if err != nil {
		return nil, err
	}
	s.keepQueries(w, r, "update cart")
	return &view{Redirect: "/cart"}, nil
}

// userError is shown to the customer as a flash message, not as a 500 page.
type userError string

func (e userError) Error() string { return string(e) }

// checkout places an order in ONE transaction. If any step fails, nothing is
// written: no order without stock, no stock taken without an order.
func (s *Server) checkout(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	ctx := r.Context()
	c := p.Customer
	method := r.FormValue("method")
	if !slices.Contains([]string{"card", "paypal", "bank_transfer", "gift_card", "pix"}, method) {
		method = "card"
	}
	promoCode := strings.ToUpper(strings.TrimSpace(r.FormValue("promo")))

	var orderID int64
	err := pgx.BeginFunc(ctx, s.pg, func(tx pgx.Tx) error {
		var err error
		orderID, err = placeOrder(ctx, tx, c, method, promoCode)
		return err
	})
	s.keepQueries(w, r, "checkout")
	var ue userError
	if errors.As(err, &ue) {
		return &view{Redirect: "/cart", Flash: ue.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	return &view{Redirect: fmt.Sprintf("/orders/%d", orderID), Flash: fmt.Sprintf("Order #%d placed. The queries of the checkout transaction are below.", orderID)}, nil
}

func placeOrder(ctx context.Context, tx pgx.Tx, c *Customer, method, promoCode string) (int64, error) {
	var cartID int64
	err := tx.QueryRow(ctx, `-- 1. lock the cart: a second checkout of the same cart (double click) waits here
SELECT id FROM carts WHERE customer_id = $1 FOR UPDATE`, c.ID).Scan(&cartID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, userError("Your cart is empty.")
	}
	if err != nil {
		return 0, err
	}

	type line struct {
		productID  int64
		qty        int32
		priceCents int64
	}
	rows, _ := tx.Query(ctx, `-- 2. cart lines with prices in cents (integer math for money)
SELECT ci.product_id, ci.quantity, (p.price_usd * 100)::bigint AS price_cents
FROM cart_items ci
JOIN products p ON p.id = ci.product_id
WHERE ci.cart_id = $1
ORDER BY ci.product_id`, cartID)
	var lines []line
	var l line
	_, err = pgx.ForEachRow(rows, []any{&l.productID, &l.qty, &l.priceCents}, func() error {
		lines = append(lines, l)
		return nil
	})
	if err != nil {
		return 0, err
	}
	if len(lines) == 0 {
		return 0, userError("Your cart is empty.")
	}
	productIDs := make([]int64, len(lines))
	for i, l := range lines {
		productIDs[i] = l.productID
	}

	// Promotion code (optional).
	var promoID *int64
	var promoPct float64
	promoProducts := map[int64]bool{}
	if promoCode != "" {
		var id int64
		err := tx.QueryRow(ctx, `-- 3. promotion code: must exist and be active now
SELECT id, discount_pct::float8 FROM promotions
WHERE code = $1 AND now() >= starts_at AND now() < ends_at`, promoCode).Scan(&id, &promoPct)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, userError(fmt.Sprintf("Promotion code %q is not active.", promoCode))
		}
		if err != nil {
			return 0, err
		}
		promoID = &id
		rows, _ := tx.Query(ctx, `-- which cart products the promotion covers
SELECT product_id FROM promotion_products
WHERE promotion_id = $1 AND product_id = ANY($2)`, id, productIDs)
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return 0, err
		}
		for _, pid := range ids {
			promoProducts[pid] = true
		}
		if len(ids) == 0 {
			return 0, userError(fmt.Sprintf("Promotion %s does not apply to the products in your cart.", promoCode))
		}
	}

	// Stock: lock every inventory row of these products, in a fixed order.
	// Two checkouts that lock the same rows in the same order cannot deadlock.
	type stockKey struct{ warehouse, product int64 }
	stock := map[stockKey]int32{}
	rows, _ = tx.Query(ctx, `-- 4. lock the stock rows (ORDER BY gives a fixed lock order: no deadlocks)
SELECT warehouse_id, product_id, quantity_on_hand
FROM inventory
WHERE product_id = ANY($1)
ORDER BY warehouse_id, product_id
FOR UPDATE`, productIDs)
	var wid, pid int64
	var qoh int32
	if _, err := pgx.ForEachRow(rows, []any{&wid, &pid, &qoh}, func() error {
		stock[stockKey{wid, pid}] = qoh
		return nil
	}); err != nil {
		return 0, err
	}

	rows, _ = tx.Query(ctx, `-- 5. warehouses in order of preference: same country, same region, any
SELECT w.id
FROM warehouses w
JOIN countries wc ON wc.code = w.country_code
JOIN countries cc ON cc.code = $1
ORDER BY (w.country_code = cc.code) DESC, (wc.region = cc.region) DESC, w.id`, c.CountryCode)
	warehouses, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return 0, err
	}
	// Each line ships from the most preferred warehouse that has enough stock,
	// so one order can ship from several warehouses.
	lineWarehouses := make([]int64, len(lines))
	qtys := make([]int32, len(lines))
	var shipFrom []int64
	for i, l := range lines {
		qtys[i] = l.qty
		for _, w := range warehouses {
			if stock[stockKey{w, l.productID}] >= l.qty {
				lineWarehouses[i] = w
				break
			}
		}
		if lineWarehouses[i] == 0 {
			return 0, userError(fmt.Sprintf("Not enough stock for product #%d: no warehouse has %d units. Reduce the quantity and try again.", l.productID, l.qty))
		}
		if !slices.Contains(shipFrom, lineWarehouses[i]) {
			shipFrom = append(shipFrom, lineWarehouses[i])
		}
	}

	if _, err := tx.Exec(ctx, `-- 6. take the stock for every line in ONE statement (arrays -> rows with unnest)
UPDATE inventory i
SET quantity_on_hand = i.quantity_on_hand - x.qty, updated_at = now()
FROM unnest($1::bigint[], $2::bigint[], $3::int[]) AS x(warehouse_id, product_id, qty)
WHERE i.warehouse_id = x.warehouse_id AND i.product_id = x.product_id`, lineWarehouses, productIDs, qtys); err != nil {
		return 0, err
	}

	// Money, in the customer's currency, in cents.
	lineNos := make([]int16, len(lines))
	units := make([]string, len(lines))
	discounts := make([]string, len(lines))
	var subtotal, discount int64
	for i, l := range lines {
		unit := int64(math.Round(float64(l.priceCents) * c.FX))
		var disc int64
		if promoProducts[l.productID] {
			disc = int64(math.Round(float64(unit*int64(l.qty)) * promoPct / 100))
		}
		lineNos[i] = int16(i + 1)
		units[i] = centsText(unit)
		discounts[i] = centsText(disc)
		subtotal += unit * int64(l.qty)
		discount += disc
	}
	var shipping int64
	if c.Segment != "vip" && float64(subtotal-discount)/c.FX < 5000 {
		shipping = int64(math.Round(599 * c.FX))
	}
	tax := int64(math.Round(float64(subtotal-discount) * c.TaxRate))
	total := subtotal - discount + shipping + tax

	var orderID int64
	if err := tx.QueryRow(ctx, `-- 7. the order header (the CHECK constraint verifies total = subtotal - discount + shipping + tax)
INSERT INTO orders (customer_id, shipping_address_id, promotion_id, status, currency_code,
                    subtotal, discount, shipping_fee, tax, total, placed_at, updated_at)
VALUES ($1,
        (SELECT id FROM addresses WHERE customer_id = $1 AND kind = 'shipping' ORDER BY is_default DESC, id LIMIT 1),
        $2, 'paid', $3, $4, $5, $6, $7, $8, now(), now())
RETURNING id`, c.ID, promoID, c.Currency, cents(subtotal), cents(discount), cents(shipping), cents(tax), cents(total)).Scan(&orderID); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `-- 8. all order lines in ONE insert (like a table-valued parameter in SQL Server)
INSERT INTO order_items (order_id, line_no, product_id, warehouse_id, quantity, unit_price, discount)
SELECT $1, x.line_no, x.product_id, x.warehouse_id, x.qty, x.unit_price::numeric, x.discount::numeric
FROM unnest($2::smallint[], $3::bigint[], $4::bigint[], $5::int[], $6::text[], $7::text[])
     AS x(line_no, product_id, warehouse_id, qty, unit_price, discount)`, orderID, lineNos, productIDs, lineWarehouses, qtys, units, discounts); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `-- 9. the payment (a real store calls the payment provider before COMMIT)
INSERT INTO payments (order_id, method, status, amount, paid_at)
VALUES ($1, $2, 'captured', $3, now())`, orderID, method, cents(total)); err != nil {
		return 0, err
	}

	carriers := make([]string, len(shipFrom))
	trackings := make([]string, len(shipFrom))
	for i := range shipFrom {
		carriers[i] = carrierFor(c.CountryCode)
		trackings[i] = fmt.Sprintf("%s%010d", strings.ToUpper(strings.ReplaceAll(carriers[i], " ", "")[:2]), rand.Int64N(1e10))
	}
	if _, err := tx.Exec(ctx, `-- 10. one shipment per warehouse, in 'preparing' state
INSERT INTO shipments (order_id, warehouse_id, carrier, tracking_number, status)
SELECT $1, x.warehouse_id, x.carrier, x.tracking, 'preparing'
FROM unnest($2::bigint[], $3::text[], $4::text[]) AS x(warehouse_id, carrier, tracking)`, orderID, shipFrom, carriers, trackings); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `-- 11. empty the cart (COMMIT follows)
DELETE FROM cart_items WHERE cart_id = $1`, cartID); err != nil {
		return 0, err
	}
	return orderID, nil
}

// ---------------------------------------------------------------------------
// Orders
// ---------------------------------------------------------------------------

type orderRow struct {
	ID       int64     `db:"id"`
	PlacedAt time.Time `db:"placed_at"`
	Status   string    `db:"status"`
	Currency string    `db:"currency_code"`
	Symbol   string    `db:"symbol"`
	Total    float64   `db:"total"`
	Lines    int64     `db:"lines"`
}

const sqlMyOrders = `-- my latest orders: an index seek on orders (customer_id, placed_at DESC)
SELECT o.id, o.placed_at, o.status, o.currency_code, cu.symbol, o.total::float8 AS total,
       (SELECT count(*) FROM order_items oi WHERE oi.order_id = o.id) AS lines
FROM orders o
JOIN currencies cu ON cu.code = o.currency_code
WHERE o.customer_id = $1
ORDER BY o.placed_at DESC
LIMIT 25`

func (s *Server) orders(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	rows, err := collect[orderRow](r.Context(), s, sqlMyOrders, p.Customer.ID)
	if err != nil {
		return nil, err
	}
	return &view{Template: "orders", Title: "Your orders", Data: rows}, nil
}

type orderHeader struct {
	ID          int64     `db:"id"`
	CustomerID  int64     `db:"customer_id"`
	Status      string    `db:"status"`
	PlacedAt    time.Time `db:"placed_at"`
	UpdatedAt   time.Time `db:"updated_at"`
	Currency    string    `db:"currency_code"`
	Symbol      string    `db:"symbol"`
	Subtotal    float64   `db:"subtotal"`
	Discount    float64   `db:"discount"`
	ShippingFee float64   `db:"shipping_fee"`
	Tax         float64   `db:"tax"`
	Total       float64   `db:"total"`
	Line1       string    `db:"line1"`
	City        string    `db:"city"`
	PostalCode  string    `db:"postal_code"`
	Country     string    `db:"country_code"`
	PromoCode   *string   `db:"promo_code"`
	Customer    string    `db:"customer"`
}

type orderLine struct {
	LineNo    int16   `db:"line_no"`
	ProductID int64   `db:"product_id"`
	Name      string  `db:"name"`
	Quantity  int32   `db:"quantity"`
	UnitPrice float64 `db:"unit_price"`
	Discount  float64 `db:"discount"`
	Returned  int64   `db:"returned"`
}

type paymentRow struct {
	Method string    `db:"method"`
	Status string    `db:"status"`
	Amount float64   `db:"amount"`
	PaidAt time.Time `db:"paid_at"`
}

type shipmentRow struct {
	Status      string     `db:"status"`
	Carrier     string     `db:"carrier"`
	Tracking    string     `db:"tracking_number"`
	ShippedAt   *time.Time `db:"shipped_at"`
	DeliveredAt *time.Time `db:"delivered_at"`
	Warehouse   string     `db:"warehouse"`
}

type returnRow struct {
	LineNo      int16     `db:"line_no"`
	Quantity    int32     `db:"quantity"`
	Reason      string    `db:"reason"`
	Status      string    `db:"status"`
	Refund      float64   `db:"refund_amount"`
	RequestedAt time.Time `db:"requested_at"`
}

const sqlOrderHeader = `-- order header, shipping address, promotion and customer
SELECT o.id, o.customer_id, o.status, o.placed_at, o.updated_at, o.currency_code, cu.symbol,
       o.subtotal::float8 AS subtotal, o.discount::float8 AS discount, o.shipping_fee::float8 AS shipping_fee,
       o.tax::float8 AS tax, o.total::float8 AS total,
       a.line1, a.city, a.postal_code, a.country_code, pr.code AS promo_code,
       c.first_name || ' ' || c.last_name AS customer
FROM orders o
JOIN currencies cu ON cu.code = o.currency_code
JOIN addresses a ON a.id = o.shipping_address_id
JOIN customers c ON c.id = o.customer_id
LEFT JOIN promotions pr ON pr.id = o.promotion_id
WHERE o.id = $1`

const sqlOrderLines = `-- order lines and how many units were returned
SELECT oi.line_no, oi.product_id, p.name, oi.quantity,
       oi.unit_price::float8 AS unit_price, oi.discount::float8 AS discount,
       coalesce(sum(r.quantity), 0) AS returned
FROM order_items oi
JOIN products p ON p.id = oi.product_id
LEFT JOIN returns r ON r.order_id = oi.order_id AND r.line_no = oi.line_no
WHERE oi.order_id = $1
GROUP BY oi.order_id, oi.line_no, p.name
ORDER BY oi.line_no`

type orderData struct {
	Order     orderHeader
	Lines     []orderLine
	Payments  []paymentRow
	Shipments []shipmentRow
	Returns   []returnRow
	Mine      bool
}

func (s *Server) order(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, notFound("order")
	}
	rows, _ := s.pg.Query(ctx, sqlOrderHeader, id)
	header, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[orderHeader])
	if err != nil {
		return nil, err
	}
	d := orderData{Order: header, Mine: header.CustomerID == p.Customer.ID}
	if d.Lines, err = collect[orderLine](ctx, s, sqlOrderLines, id); err != nil {
		return nil, err
	}
	if d.Payments, err = collect[paymentRow](ctx, s, `-- payments (a failed attempt and a retry are two rows)
SELECT method, status, amount::float8 AS amount, paid_at FROM payments WHERE order_id = $1 ORDER BY paid_at, id`, id); err != nil {
		return nil, err
	}
	if d.Shipments, err = collect[shipmentRow](ctx, s, `-- shipment
SELECT s.status, s.carrier, s.tracking_number, s.shipped_at, s.delivered_at, w.name AS warehouse
FROM shipments s JOIN warehouses w ON w.id = s.warehouse_id
WHERE s.order_id = $1`, id); err != nil {
		return nil, err
	}
	if d.Returns, err = collect[returnRow](ctx, s, `-- returns
SELECT line_no, quantity, reason, status, refund_amount::float8 AS refund_amount, requested_at
FROM returns WHERE order_id = $1 ORDER BY requested_at`, id); err != nil {
		return nil, err
	}
	return &view{Template: "order", Title: fmt.Sprintf("Order #%d", id), Data: d}, nil
}

// orderAction moves an order through its lifecycle: ship, deliver, cancel, return.
func (s *Server) orderAction(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, notFound("order")
	}
	action := r.PathValue("action")
	back := fmt.Sprintf("/orders/%d", id)
	msg := ""
	err = pgx.BeginFunc(ctx, s.pg, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `-- lock the order row: "ship" and "cancel" at the same time cannot both win
SELECT status FROM orders WHERE id = $1 FOR UPDATE`, id).Scan(&status); err != nil {
			return err
		}
		switch action {
		case "ship":
			if status != "paid" {
				return userError("Only paid orders can be shipped.")
			}
			if _, err := tx.Exec(ctx, `-- the parcels leave the warehouses
UPDATE shipments SET status = 'in_transit', shipped_at = now()
WHERE order_id = $1 AND status = 'preparing'`, id); err != nil {
				return err
			}
			msg = "Order shipped."
			return setOrderStatus(ctx, tx, id, "shipped")
		case "deliver":
			if status != "shipped" {
				return userError("Only shipped orders can be delivered.")
			}
			if _, err := tx.Exec(ctx, `-- the parcels arrive
UPDATE shipments SET status = 'delivered', delivered_at = now()
WHERE order_id = $1 AND status = 'in_transit'`, id); err != nil {
				return err
			}
			msg = "Order delivered."
			return setOrderStatus(ctx, tx, id, "delivered")
		case "cancel":
			if status != "pending" && status != "paid" {
				return userError("Only orders that have not shipped can be cancelled.")
			}
			if _, err := tx.Exec(ctx, `-- put the stock back in the warehouse each line came from
UPDATE inventory i
SET quantity_on_hand = i.quantity_on_hand + oi.quantity, updated_at = now()
FROM order_items oi
WHERE oi.order_id = $1
  AND i.warehouse_id = oi.warehouse_id
  AND i.product_id = oi.product_id`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `-- no shipments for a cancelled order
DELETE FROM shipments WHERE order_id = $1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `-- refund the payment
UPDATE payments SET status = 'refunded' WHERE order_id = $1 AND status IN ('captured', 'authorized')`, id); err != nil {
				return err
			}
			msg = "Order cancelled, stock returned and payment refunded."
			return setOrderStatus(ctx, tx, id, "cancelled")
		case "return":
			if status != "delivered" {
				return userError("Only delivered orders can be returned.")
			}
			lineNo := atoi(r.FormValue("line_no"))
			reason := r.FormValue("reason")
			if !slices.Contains([]string{"damaged", "wrong_item", "wrong_size", "not_as_described", "changed_mind", "late_delivery"}, reason) {
				reason = "changed_mind"
			}
			var left, qty int32
			var unitCents, discCents int64
			err := tx.QueryRow(ctx, `-- how many units of this line can still be returned (lock the line)
SELECT oi.quantity - coalesce((SELECT sum(r.quantity) FROM returns r
                               WHERE r.order_id = oi.order_id AND r.line_no = oi.line_no), 0),
       oi.quantity, (oi.unit_price * 100)::bigint, (oi.discount * 100)::bigint
FROM order_items oi
WHERE oi.order_id = $1 AND oi.line_no = $2
FOR UPDATE`, id, lineNo).Scan(&left, &qty, &unitCents, &discCents)
			if err != nil {
				return err
			}
			if left < 1 {
				return userError("Every unit of that line was already returned.")
			}
			refund := unitCents - discCents/int64(qty)
			if _, err := tx.Exec(ctx, `-- one unit back, refunded right away
INSERT INTO returns (order_id, line_no, quantity, reason, status, refund_amount, requested_at)
VALUES ($1, $2, 1, $3, 'refunded', $4, now())`, id, lineNo, reason, cents(refund)); err != nil {
				return err
			}
			msg = "Return registered and refunded."
			return nil
		}
		return notFound("action")
	})
	s.keepQueries(w, r, action+" order")
	var ue userError
	if errors.As(err, &ue) {
		return &view{Redirect: back, Flash: ue.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	return &view{Redirect: back, Flash: msg}, nil
}

func setOrderStatus(ctx context.Context, tx pgx.Tx, id int64, status string) error {
	_, err := tx.Exec(ctx, `-- new order status
UPDATE orders SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	return err
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// cents converts integer cents into an exact numeric(…,2) parameter.
func cents[T int64 | float64](c T) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(int64(c)), Exp: -2, Valid: true}
}

func centsText(c int64) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s%d.%02d", sign, c/100, c%100)
}

var carriersByCountry = map[string][]string{
	"US": {"UPS", "FedEx", "USPS"}, "CA": {"Canada Post", "UPS"}, "MX": {"Estafeta", "DHL"},
	"BR": {"Correios", "DHL"}, "CL": {"Chilexpress", "DHL"}, "GB": {"Royal Mail", "DPD"},
	"DE": {"DHL", "DPD", "GLS"}, "FR": {"DPD", "GLS"}, "ES": {"GLS", "DHL"}, "IT": {"GLS", "DHL"},
	"NL": {"DPD", "DHL"}, "PT": {"GLS", "DHL"}, "SE": {"DHL", "DPD"}, "JP": {"Yamato", "DHL"},
	"AU": {"Australia Post", "DHL"}, "IN": {"Blue Dart", "DHL"}, "SG": {"Ninja Van", "DHL"},
}

func carrierFor(country string) string {
	if list, ok := carriersByCountry[country]; ok {
		return list[rand.IntN(len(list))]
	}
	return "DHL"
}

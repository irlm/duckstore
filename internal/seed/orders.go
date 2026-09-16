package seed

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

type orderStreams struct {
	orders, items, payments, shipments, returns, reviews *stream
}

func newOrderStreams() *orderStreams {
	return &orderStreams{
		orders: newStream("orders", "id", "customer_id", "shipping_address_id", "promotion_id", "status", "currency_code",
			"subtotal", "discount", "shipping_fee", "tax", "total", "placed_at", "updated_at"),
		items:     newStream("order_items", "order_id", "line_no", "product_id", "quantity", "unit_price", "discount"),
		payments:  newStream("payments", "id", "order_id", "method", "status", "amount", "paid_at"),
		shipments: newStream("shipments", "id", "order_id", "warehouse_id", "carrier", "tracking_number", "status", "shipped_at", "delivered_at"),
		returns:   newStream("returns", "id", "order_id", "line_no", "quantity", "reason", "status", "refund_amount", "requested_at"),
		reviews:   newStream("reviews", "id", "product_id", "customer_id", "rating", "title", "body", "created_at"),
	}
}

func (o *orderStreams) all() []*stream {
	return []*stream{o.orders, o.items, o.payments, o.shipments, o.returns, o.reviews}
}

// Demand multipliers.
var (
	weekdayFactor = [7]float64{1.10, 0.95, 0.95, 0.97, 1.00, 1.02, 1.12} // Sunday..Saturday
	monthFactor   = [12]float64{0.90, 0.85, 0.95, 0.95, 1.00, 1.00, 1.02, 1.00, 0.98, 1.05, 1.30, 1.45}
	hourWeights   = []float64{0.3, 0.2, 0.15, 0.1, 0.1, 0.15, 0.3, 0.5, 0.8, 1.0, 1.1, 1.2, 1.3, 1.2, 1.1, 1.1, 1.2, 1.4, 1.6, 1.8, 1.9, 1.7, 1.2, 0.6}
)

type orderLine struct {
	product    int
	qty        int64
	unitCents  int64
	discCents  int64
	returned   bool
}

type orderCounters struct {
	payment, shipment, ret, review int64
	reviewed                       map[uint64]struct{}
}

// generateOrders produces about 2.5M orders (at scale 1) in time order, with
// their lines, payments, shipments, returns and reviews.
func (g *gen) generateOrders(ctx context.Context, s *orderStreams) (err error) {
	defer func() {
		for _, st := range s.all() {
			if ferr := st.finish(ctx); err == nil {
				err = ferr
			}
		}
	}()

	special := map[time.Time]float64{}
	for y := g.start.Year(); y <= g.end.Year(); y++ {
		bf := blackFriday(y)
		special[bf] = 3.2
		special[bf.AddDate(0, 0, 1)] = 2.0
		special[bf.AddDate(0, 0, 2)] = 1.8
		special[bf.AddDate(0, 0, 3)] = 2.6 // Cyber Monday
		special[time.Date(y, 12, 24, 0, 0, 0, 0, time.UTC)] = 0.7
		special[time.Date(y, 12, 25, 0, 0, 0, 0, time.UTC)] = 0.5
		special[time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)] = 0.7
	}
	for range 2 { // two "site outage" days, something to find in the analytics
		special[g.day(30+g.rng.IntN(g.days-60))] = 0.3
	}

	// Daily demand grows with the number of customers who already signed up.
	weights := make([]float64, g.days)
	total := 0.0
	for d := 1; d < g.days; d++ {
		date := g.day(d)
		existing := float64(g.firstCustOfDay[d] - 1)
		w := math.Pow(existing, 0.85) * weekdayFactor[date.Weekday()] * monthFactor[date.Month()-1] * g.uniform(0.9, 1.1)
		if f, ok := special[date]; ok {
			w *= f
		}
		weights[d] = w
		total += w
	}

	target := float64(g.scaled(2_500_000, 5000))
	counters := &orderCounters{reviewed: map[uint64]struct{}{}}
	var orderID int64
	carry := 0.0
	for d := 1; d < g.days; d++ {
		exact := weights[d]/total*target + carry
		count := int(exact)
		carry = exact - float64(count)

		secs := make([]int, count)
		for i := range secs {
			secs[i] = (g.weighted(hourWeights) * 3600) + g.rng.IntN(3600)
		}
		slices.Sort(secs)

		day := g.day(d)
		var active []int
		for i := range g.promotions {
			if !day.Before(g.promotions[i].starts) && day.Before(g.promotions[i].ends) {
				active = append(active, i)
			}
		}
		for _, sec := range secs {
			orderID++
			if err := g.emitOrder(ctx, s, counters, orderID, d, day.Add(time.Duration(sec)*time.Second), active); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *gen) emitOrder(ctx context.Context, s *orderStreams, n *orderCounters, orderID int64, d int, placed time.Time, activePromos []int) error {
	custID := g.pickCustomer(d, placed)
	c := &g.customers[custID-1]
	cd := &countryDefs[c.country]
	curr := g.currIdx[cd.Currency]
	rate := g.rateAt(curr, placed)
	month := int(placed.Month()) - 1

	// Promotion: the customer uses a code that is active today.
	var promo *promotion
	var promoID int64
	if len(activePromos) > 0 {
		i := activePromos[g.rng.IntN(len(activePromos))]
		chance := 0.3
		if strings.HasPrefix(g.promotions[i].code, "BLACKFRIDAY") {
			chance = 0.6
		}
		if g.rng.Float64() < chance {
			promo, promoID = &g.promotions[i], int64(i+1)
		}
	}

	// Order lines.
	nLines := 1 + g.weighted([]float64{45, 25, 14, 8, 5, 3})
	lines := make([]orderLine, 0, nLines)
	var subtotal, discount int64
	for l := range nLines {
		pi := -1
		if l == 0 && promo != nil {
			for range 5 {
				if cand := int(promo.products[g.rng.IntN(len(promo.products))]); g.products[cand].createdAt.Before(placed) {
					pi = cand
					break
				}
			}
		}
		if pi < 0 {
			pi = g.pickProduct(placed, month)
		}
		if slices.ContainsFunc(lines, func(x orderLine) bool { return x.product == pi }) {
			continue
		}
		p := &g.products[pi]
		qty := int64(1 + g.weighted([]float64{75, 17, 5, 2, 1}))
		if categoryTree[g.categories[p.cat].top].Name == "Grocery" {
			qty += int64(g.rng.IntN(3))
		}
		unit := int64(math.Round(float64(p.priceAt(placed)) * rate))
		var disc int64
		if promo != nil {
			if _, ok := promo.set[int32(pi)]; ok {
				disc = int64(math.Round(float64(qty*unit) * promo.pct / 100))
			}
		}
		lines = append(lines, orderLine{product: pi, qty: qty, unitCents: unit, discCents: disc})
		subtotal += qty * unit
		discount += disc
	}
	if discount == 0 {
		promoID = 0
	}
	var shipping int64
	if c.segment != segVIP && float64(subtotal-discount)/rate < 5000 {
		shipping = int64(math.Round(599 * rate)) // 5.99 USD in local currency, free above 50 USD
	}
	tax := int64(math.Round(float64(subtotal-discount) * cd.TaxRate))
	total := subtotal - discount + shipping + tax

	// Lifecycle: paid -> shipped -> delivered, compared with the dataset's "now".
	cancelled := g.rng.Float64() < 0.03
	paidAt := placed.Add(time.Duration(60+g.rng.IntN(30*60)) * time.Second)
	var whID int64
	var carrier string
	var shippedAt, deliveredAt time.Time
	transit := 0
	if !cancelled {
		whs, crossBorder := g.whByCountry[cd.Code], false
		if len(whs) == 0 {
			whs, crossBorder = g.whByRegion[cd.Region], true
		}
		whID = whs[g.rng.IntN(len(whs))]
		carrier = cd.Carriers[g.weighted([]float64{3, 2, 1}[:len(cd.Carriers)])]
		shippedAt = paidAt.Add(time.Duration(4*3600+g.rng.IntN(68*3600)) * time.Second)
		transit = carrierTransitDays[carrier] + g.rng.IntN(3)
		if crossBorder {
			transit += 3
		}
		if placed.Month() == time.December {
			transit += 1 + g.rng.IntN(2)
		}
		if g.rng.Float64() < 0.03 {
			transit += 5 + g.rng.IntN(8) // lost in the network for a while
		}
		deliveredAt = shippedAt.Add(time.Duration(transit*24*3600+g.rng.IntN(10*3600)) * time.Second)
	}

	status, updated := "delivered", deliveredAt
	switch {
	case cancelled:
		status, updated = "cancelled", placed.Add(time.Duration(3600+g.rng.IntN(48*3600))*time.Second)
	case !paidAt.Before(g.end):
		status, updated = "pending", placed
	case !shippedAt.Before(g.end):
		status, updated = "paid", paidAt
	case !deliveredAt.Before(g.end):
		status, updated = "shipped", shippedAt
	}
	if updated.After(g.end) {
		updated = g.end.Add(-time.Second)
	}

	var promoArg any
	if promoID != 0 {
		promoArg = promoID
	}
	if err := s.orders.add(ctx, orderID, int64(custID), c.addrID, promoArg, status, cd.Currency,
		money(subtotal), money(discount), money(shipping), money(tax), money(total), placed, updated); err != nil {
		return err
	}
	for i, l := range lines {
		if err := s.items.add(ctx, orderID, int16(i+1), int64(l.product+1), int32(l.qty), money(l.unitCents), money(l.discCents)); err != nil {
			return err
		}
	}

	// Payments.
	method := "card"
	if cd.Code == "BR" && g.rng.Float64() < 0.4 {
		method = "pix"
	} else {
		method = []string{"card", "paypal", "bank_transfer", "gift_card"}[g.weighted([]float64{65, 20, 8, 7})]
	}
	addPayment := func(status string, at time.Time) error {
		n.payment++
		return s.payments.add(ctx, n.payment, orderID, method, status, money(total), at)
	}
	switch {
	case status == "pending":
		if err := addPayment("authorized", placed.Add(5*time.Second)); err != nil {
			return err
		}
	case cancelled:
		ps := "refunded"
		if g.rng.Float64() < 0.3 {
			ps = "failed"
		}
		if err := addPayment(ps, placed.Add(time.Minute)); err != nil {
			return err
		}
	default:
		if g.rng.Float64() < 0.02 {
			if err := addPayment("failed", placed.Add(time.Minute)); err != nil {
				return err
			}
		}
		if err := addPayment("captured", paidAt); err != nil {
			return err
		}
	}

	if status != "shipped" && status != "delivered" {
		return nil
	}

	// Shipment.
	n.shipment++
	tracking := fmt.Sprintf("%s%010d", strings.ToUpper(slugify(carrier)[:2]), g.rng.Int64N(1e10))
	if status == "shipped" {
		return s.shipments.add(ctx, n.shipment, orderID, whID, carrier, tracking, "in_transit", shippedAt, nil)
	}
	if err := s.shipments.add(ctx, n.shipment, orderID, whID, carrier, tracking, "delivered", shippedAt, deliveredAt); err != nil {
		return err
	}

	// Returns and reviews only happen after delivery.
	for i := range lines {
		l := &lines[i]
		p := &g.products[l.product]
		cat := &g.categories[p.cat]
		late := transit > 8
		chance := cat.returnRate
		if late {
			chance *= 1.5
		}
		if g.rng.Float64() >= chance {
			continue
		}
		requested := deliveredAt.Add(time.Duration(24*3600+g.rng.IntN(29*24*3600)) * time.Second)
		if !requested.Before(g.end) {
			continue
		}
		l.returned = true
		qty := 1 + g.rng.Int64N(l.qty)
		reasons, weights := []string{"damaged", "wrong_item", "wrong_size", "not_as_described", "changed_mind", "late_delivery"}, []float64{30, 15, 0, 20, 25, 10}
		if categoryTree[cat.top].Name == "Fashion" {
			weights = []float64{5, 5, 45, 20, 20, 5}
		}
		if late {
			weights[5] = 60
		}
		retStatus := "requested"
		if g.end.Sub(requested) > 3*24*time.Hour {
			retStatus = []string{"refunded", "approved", "rejected"}[g.weighted([]float64{85, 7, 8})]
		}
		var refund int64
		if retStatus == "refunded" || retStatus == "approved" {
			refund = qty*l.unitCents - l.discCents*qty/l.qty
		}
		n.ret++
		if err := s.returns.add(ctx, n.ret, orderID, int16(i+1), int32(qty), reasons[g.weighted(weights)], retStatus, money(refund), requested); err != nil {
			return err
		}
	}

	for _, l := range lines {
		chance := 0.07
		if l.returned {
			chance = 0.25
		}
		if g.rng.Float64() >= chance {
			continue
		}
		key := uint64(l.product)<<32 | uint64(custID)
		if _, dup := n.reviewed[key]; dup {
			continue
		}
		created := deliveredAt.Add(time.Duration(24*3600+g.rng.IntN(19*24*3600)) * time.Second)
		if !created.Before(g.end) {
			continue
		}
		n.reviewed[key] = struct{}{}
		rating := int(math.Round(g.rng.NormFloat64()*0.9 + g.products[l.product].quality))
		if l.returned {
			rating -= 2
		}
		rating = min(5, max(1, rating))
		n.review++
		if err := s.reviews.add(ctx, n.review, int64(l.product+1), int64(custID), int16(rating),
			g.pick(reviewTitles[rating]), g.pick(reviewBodies[rating]), created); err != nil {
			return err
		}
	}
	return nil
}

// pickCustomer chooses who places an order on day d. Recent sign-ups often
// buy soon; older customers come back less and less (this shapes the cohort
// retention curves). VIP and business customers buy more often.
func (g *gen) pickCustomer(d int, placed time.Time) int {
	accept := [3]float64{0.55, 0.85, 1.0}
	for range 30 {
		var e int
		if g.rng.Float64() < 0.3 {
			e = d - g.rng.IntN(30)
		} else {
			e = d - int(g.rng.ExpFloat64()*200)
		}
		if e < 0 {
			e = g.rng.IntN(d + 1)
		}
		lo, hi := g.firstCustOfDay[e], g.firstCustOfDay[e+1]-1
		if hi < lo {
			continue
		}
		id := lo + g.rng.IntN(hi-lo+1)
		c := &g.customers[id-1]
		if !c.createdAt.Before(placed) || g.rng.Float64() > accept[c.segment] {
			continue
		}
		return id
	}
	return 1 + g.rng.IntN(g.firstCustOfDay[d]-1) // anyone who signed up before today
}

// pickProduct samples by popularity (Zipf), skips products not launched yet,
// and applies category seasonality (toys in December, tents in summer).
func (g *gen) pickProduct(placed time.Time, month int) int {
	for range 15 {
		pi := int(g.popularity[g.zipf.Uint64()])
		p := &g.products[pi]
		if p.createdAt.After(placed) {
			continue
		}
		if g.rng.Float64()*2.3 > g.categories[p.cat].season[month] {
			continue
		}
		return pi
	}
	for {
		if pi := g.rng.IntN(len(g.products)); g.products[pi].createdAt.Before(placed) {
			return pi
		}
	}
}

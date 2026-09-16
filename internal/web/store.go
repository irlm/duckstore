package web

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/irlm/duckstore/internal/query"
)

// ---------------------------------------------------------------------------
// The store runs on Postgres. Every query is small and uses an index: this is
// the OLTP workload Postgres (and SQL Server) are built for.
// ---------------------------------------------------------------------------

type Customer struct {
	ID          int64   `db:"id"`
	FirstName   string  `db:"first_name"`
	LastName    string  `db:"last_name"`
	Segment     string  `db:"segment"`
	CountryCode string  `db:"country_code"`
	Country     string  `db:"country"`
	Region      string  `db:"region"`
	Currency    string  `db:"currency_code"`
	Symbol      string  `db:"symbol"`
	TaxRate     float64 `db:"tax_rate"`
	FX          float64 `db:"fx"`
	CartUnits   int64   `db:"cart_units"`
}

const sqlCustomer = `-- current customer, latest exchange rate and cart size
SELECT c.id, c.first_name, c.last_name, c.segment, c.country_code,
       co.name AS country, co.region, co.currency_code, cu.symbol,
       co.tax_rate::float8 AS tax_rate,
       (SELECT f.units_per_usd::float8
          FROM fx_rates f
         WHERE f.currency_code = co.currency_code
         ORDER BY f.rate_date DESC
         LIMIT 1) AS fx,
       (SELECT coalesce(sum(ci.quantity), 0)
          FROM carts ca
          JOIN cart_items ci ON ci.cart_id = ca.id
         WHERE ca.customer_id = c.id) AS cart_units
FROM customers c
JOIN countries co ON co.code = c.country_code
JOIN currencies cu ON cu.code = co.currency_code
WHERE c.id = $1`

const sqlRandomCustomer = `-- pick a random customer, optionally from one country
SELECT id
FROM customers
WHERE id >= (SELECT floor(random() * max(id))::bigint FROM customers)
  AND ($1 = '' OR country_code = $1)
ORDER BY id
LIMIT 1`

func (s *Server) currentCustomer(w http.ResponseWriter, r *http.Request) (*Customer, error) {
	ctx := r.Context()
	if c, err := r.Cookie("customer"); err == nil {
		if id, err := strconv.ParseInt(c.Value, 10, 64); err == nil {
			cust, err := s.loadCustomer(ctx, id)
			if err == nil {
				return cust, nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
		}
	}
	id, err := s.randomCustomer(ctx, "")
	if err != nil {
		return nil, err
	}
	setCustomerCookie(w, id)
	return s.loadCustomer(ctx, id)
}

func (s *Server) loadCustomer(ctx context.Context, id int64) (*Customer, error) {
	rows, _ := s.pg.Query(ctx, sqlCustomer, id)
	c, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[Customer])
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Server) randomCustomer(ctx context.Context, country string) (int64, error) {
	var id int64
	for range 5 {
		err := s.pg.QueryRow(ctx, sqlRandomCustomer, country).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
	}
	return 0, fmt.Errorf("no customers found; did you run `make seed`?")
}

func setCustomerCookie(w http.ResponseWriter, id int64) {
	http.SetCookie(w, &http.Cookie{Name: "customer", Value: strconv.FormatInt(id, 10), Path: "/", MaxAge: 30 * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// ---------------------------------------------------------------------------
// Home
// ---------------------------------------------------------------------------

type categoryCount struct {
	Name     string `db:"name"`
	Slug     string `db:"slug"`
	Products int64  `db:"products"`
}

type productCard struct {
	ID       int64   `db:"id"`
	Name     string  `db:"name"`
	Brand    string  `db:"brand"`
	PriceUSD float64 `db:"price_usd"`
	Units    int64   `db:"units"`
	Rating   float64 `db:"rating"`
	Reviews  int64   `db:"reviews"`
	Total    int64   `db:"total"`
}

const sqlTopCategories = `-- top-level categories with product counts (recursive CTE walks DOWN the tree)
WITH RECURSIVE tree AS (
    SELECT id AS root_id, id FROM categories WHERE parent_id IS NULL
    UNION ALL
    SELECT t.root_id, c.id FROM categories c JOIN tree t ON c.parent_id = t.id
)
SELECT r.name, r.slug, count(p.id) AS products
FROM tree t
JOIN categories r ON r.id = t.root_id
LEFT JOIN products p ON p.category_id = t.id AND p.is_active
GROUP BY r.id, r.name, r.slug
ORDER BY r.name`

const sqlBestSellers = `-- best sellers in the last 7 days (live aggregation on the OLTP tables)
SELECT p.id, p.name, b.name AS brand, p.price_usd::float8 AS price_usd,
       sum(oi.quantity) AS units, 0::float8 AS rating, 0::bigint AS reviews, 0::bigint AS total
FROM orders o
JOIN order_items oi ON oi.order_id = o.id
JOIN products p ON p.id = oi.product_id
JOIN brands b ON b.id = p.brand_id
WHERE o.placed_at >= now() - interval '7 days'
  AND o.status <> 'cancelled'
GROUP BY p.id, b.name
ORDER BY units DESC
LIMIT 8`

const sqlTrending = `-- most sold products in the last 90 days (pre-computed by the ETL in dw.product_stats)
SELECT product_id, units_90d, avg_rating, review_count
FROM dw.product_stats
ORDER BY units_90d DESC
LIMIT 8`

const sqlProductsByID = `-- current name and price for ids that came from the warehouse
SELECT p.id, p.name, b.name AS brand, p.price_usd::float8 AS price_usd,
       0::bigint AS units, 0::float8 AS rating, 0::bigint AS reviews, 0::bigint AS total
FROM products p
JOIN brands b ON b.id = p.brand_id
WHERE p.id = ANY($1)`

type homeData struct {
	Categories  []categoryCount
	BestSellers []productCard
	Trending    []productCard
	TrendingErr string
}

func (s *Server) home(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	ctx := r.Context()
	d := homeData{}
	var err error
	if d.Categories, err = collect[categoryCount](ctx, s, sqlTopCategories); err != nil {
		return nil, err
	}
	if d.BestSellers, err = collect[productCard](ctx, s, sqlBestSellers); err != nil {
		return nil, err
	}
	// Hybrid: DuckDB says WHICH products (heavy analytics, pre-computed),
	// Postgres says their CURRENT name and price (fresh OLTP data).
	d.Trending, err = s.warehouseProducts(ctx, sqlTrending, nil, func(row []any) (int64, int64, float64, int64) {
		units, _ := query.Float(row[1])
		rating, _ := query.Float(row[2])
		reviews, _ := query.Float(row[3])
		return toInt(row[0]), int64(units), rating, int64(reviews)
	})
	if err != nil {
		d.TrendingErr = err.Error()
	}
	return &view{Template: "home", Title: "duckstore", Data: d}, nil
}

// warehouseProducts runs a DuckDB query whose rows start with product ids and
// fills names/prices from Postgres, keeping the warehouse order.
func (s *Server) warehouseProducts(ctx context.Context, sql string, arg any, pick func(row []any) (id, units int64, rating float64, reviews int64)) ([]productCard, error) {
	var res *query.Result
	var err error
	if arg != nil {
		res, err = duck(ctx, s.wh, sql, 0, arg)
	} else {
		res, err = duck(ctx, s.wh, sql, 0)
	}
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, nil
	}
	ids := make([]int64, len(res.Rows))
	for i, row := range res.Rows {
		ids[i], _, _, _ = pick(row)
	}
	live, err := collect[productCard](ctx, s, sqlProductsByID, ids)
	if err != nil {
		return nil, err
	}
	byID := map[int64]productCard{}
	for _, c := range live {
		byID[c.ID] = c
	}
	out := make([]productCard, 0, len(res.Rows))
	for _, row := range res.Rows {
		id, units, rating, reviews := pick(row)
		if c, ok := byID[id]; ok {
			c.Units, c.Rating, c.Reviews = units, rating, reviews
			out = append(out, c)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Category
// ---------------------------------------------------------------------------

type crumb struct {
	ID   int64  `db:"id"`
	Name string `db:"name"`
	Slug string `db:"slug"`
}

const sqlBreadcrumbBySlug = `-- breadcrumb: recursive CTE walks UP from this category to the root
WITH RECURSIVE up AS (
    SELECT id, parent_id, name, slug, 0 AS lvl FROM categories WHERE slug = $1
    UNION ALL
    SELECT c.id, c.parent_id, c.name, c.slug, up.lvl + 1
    FROM categories c
    JOIN up ON c.id = up.parent_id
)
SELECT id, name, slug FROM up ORDER BY lvl DESC`

const sqlBreadcrumbByID = `-- breadcrumb: recursive CTE walks UP from this category to the root
WITH RECURSIVE up AS (
    SELECT id, parent_id, name, slug, 0 AS lvl FROM categories WHERE id = $1
    UNION ALL
    SELECT c.id, c.parent_id, c.name, c.slug, up.lvl + 1
    FROM categories c
    JOIN up ON c.id = up.parent_id
)
SELECT id, name, slug FROM up ORDER BY lvl DESC`

const sqlChildCategories = `-- child categories
SELECT id, name, slug FROM categories WHERE parent_id = $1 ORDER BY name`

const sqlCategoryProducts = `-- one page of products from this category AND all its descendants
WITH RECURSIVE subtree AS (
    SELECT id FROM categories WHERE id = $1
    UNION ALL
    SELECT c.id FROM categories c JOIN subtree s ON c.parent_id = s.id
),
page AS (
    SELECT p.id, p.name, p.price_usd, p.brand_id, p.created_at,
           count(*) OVER () AS total              -- total rows before LIMIT, for paging
    FROM products p
    JOIN subtree s ON s.id = p.category_id
    WHERE p.is_active
    ORDER BY %[1]s
    LIMIT 24 OFFSET $2
)
-- ratings are looked up only for the 24 rows of the page (LATERAL = CROSS/OUTER APPLY)
SELECT page.id, page.name, b.name AS brand, page.price_usd::float8 AS price_usd, 0::bigint AS units,
       coalesce(r.avg_rating, 0)::float8 AS rating, coalesce(r.reviews, 0) AS reviews, page.total
FROM page
JOIN brands b ON b.id = page.brand_id
LEFT JOIN LATERAL (
    SELECT avg(rating) AS avg_rating, count(*) AS reviews
    FROM reviews
    WHERE reviews.product_id = page.id
) r ON true
ORDER BY %[2]s`

var categorySorts = map[string][2]string{
	"name":       {"p.name, p.id", "page.name, page.id"},
	"price":      {"p.price_usd, p.id", "page.price_usd, page.id"},
	"price_desc": {"p.price_usd DESC, p.id", "page.price_usd DESC, page.id"},
	"newest":     {"p.created_at DESC, p.id", "page.created_at DESC, page.id"},
}

type categoryData struct {
	Crumbs   []crumb
	Children []crumb
	Products []productCard
	Total    int64
	Page     int
	Pages    int
	Sort     string
}

func (s *Server) category(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	ctx := r.Context()
	d := categoryData{Sort: r.URL.Query().Get("sort"), Page: max(1, atoi(r.URL.Query().Get("page")))}
	if _, ok := categorySorts[d.Sort]; !ok {
		d.Sort = "name"
	}
	var err error
	if d.Crumbs, err = collect[crumb](ctx, s, sqlBreadcrumbBySlug, r.PathValue("slug")); err != nil {
		return nil, err
	}
	if len(d.Crumbs) == 0 {
		return nil, notFound("category")
	}
	cat := d.Crumbs[len(d.Crumbs)-1]
	if d.Children, err = collect[crumb](ctx, s, sqlChildCategories, cat.ID); err != nil {
		return nil, err
	}
	sorts := categorySorts[d.Sort]
	if d.Products, err = collect[productCard](ctx, s, fmt.Sprintf(sqlCategoryProducts, sorts[0], sorts[1]), cat.ID, (d.Page-1)*24); err != nil {
		return nil, err
	}
	if len(d.Products) > 0 {
		d.Total = d.Products[0].Total
	}
	d.Pages = int(math.Ceil(float64(d.Total) / 24))
	return &view{Template: "category", Title: cat.Name, Data: d}, nil
}

// ---------------------------------------------------------------------------
// Product
// ---------------------------------------------------------------------------

type productDetail struct {
	ID          int64          `db:"id"`
	SKU         string         `db:"sku"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	PriceUSD    float64        `db:"price_usd"`
	CostUSD     float64        `db:"cost_usd"`
	Attributes  map[string]any `db:"attributes"`
	IsActive    bool           `db:"is_active"`
	CategoryID  int64          `db:"category_id"`
	Brand       string         `db:"brand"`
}

type promo struct {
	Code        string    `db:"code"`
	Name        string    `db:"name"`
	DiscountPct float64   `db:"discount_pct"`
	EndsAt      time.Time `db:"ends_at"`
}

type stockRow struct {
	Code     string `db:"code"`
	City     string `db:"city"`
	Country  string `db:"country"`
	Quantity int64  `db:"quantity_on_hand"`
}

type ratingSummary struct {
	Reviews   int64   `db:"reviews"`
	AvgRating float64 `db:"avg_rating"`
	R5        int64   `db:"r5"`
	R4        int64   `db:"r4"`
	R3        int64   `db:"r3"`
	R2        int64   `db:"r2"`
	R1        int64   `db:"r1"`
}

type review struct {
	Rating    int16     `db:"rating"`
	Title     string    `db:"title"`
	Body      string    `db:"body"`
	CreatedAt time.Time `db:"created_at"`
	FirstName string    `db:"first_name"`
	Country   string    `db:"country_code"`
}

type pricePoint struct {
	ValidFrom time.Time  `db:"valid_from"`
	ValidTo   *time.Time `db:"valid_to"`
	PriceUSD  float64    `db:"price_usd"`
}

const sqlProduct = `-- the product and its brand
SELECT p.id, p.sku, p.name, p.description, p.price_usd::float8 AS price_usd, p.cost_usd::float8 AS cost_usd,
       p.attributes, p.is_active, p.category_id, b.name AS brand
FROM products p
JOIN brands b ON b.id = p.brand_id
WHERE p.id = $1`

const sqlActivePromo = `-- active promotion (many-to-many through promotion_products)
SELECT pr.code, pr.name, pr.discount_pct::float8 AS discount_pct, pr.ends_at
FROM promotion_products pp
JOIN promotions pr ON pr.id = pp.promotion_id
WHERE pp.product_id = $1
  AND now() >= pr.starts_at AND now() < pr.ends_at
ORDER BY pr.discount_pct DESC
LIMIT 1`

const sqlStock = `-- stock in each warehouse
SELECT w.code, w.city, co.name AS country, i.quantity_on_hand
FROM inventory i
JOIN warehouses w ON w.id = i.warehouse_id
JOIN countries co ON co.code = w.country_code
WHERE i.product_id = $1
ORDER BY i.quantity_on_hand DESC`

const sqlRatingSummary = `-- rating summary: FILTER counts each star level in a single pass
SELECT count(*) AS reviews,
       coalesce(avg(rating), 0)::float8 AS avg_rating,
       count(*) FILTER (WHERE rating = 5) AS r5,
       count(*) FILTER (WHERE rating = 4) AS r4,
       count(*) FILTER (WHERE rating = 3) AS r3,
       count(*) FILTER (WHERE rating = 2) AS r2,
       count(*) FILTER (WHERE rating = 1) AS r1
FROM reviews
WHERE product_id = $1`

const sqlLatestReviews = `-- latest reviews (index on reviews (product_id, created_at DESC))
SELECT r.rating, r.title, r.body, r.created_at, c.first_name, c.country_code
FROM reviews r
JOIN customers c ON c.id = r.customer_id
WHERE r.product_id = $1
ORDER BY r.created_at DESC
LIMIT 5`

const sqlPriceHistory = `-- price history (temporal table: one row per price version)
SELECT valid_from, valid_to, price_usd::float8 AS price_usd
FROM product_prices
WHERE product_id = $1
ORDER BY valid_from`

const sqlBoughtTogether = `-- frequently bought together (pre-computed by the ETL in dw.product_pairs)
SELECT other_product_id, orders_together
FROM dw.product_pairs
WHERE product_id = $1
ORDER BY orders_together DESC`

type productData struct {
	Product     productDetail
	Crumbs      []crumb
	Promo       *promo
	Stock       []stockRow
	TotalStock  int64
	Ratings     ratingSummary
	Reviews     []review
	Prices      []pricePoint
	PriceChart  string
	Together    []productCard
	TogetherErr string
	PromoPrice  float64
}

func (s *Server) product(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, notFound("product")
	}
	d := productData{}
	rows, _ := s.pg.Query(ctx, sqlProduct, id)
	prod, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[productDetail])
	if err != nil {
		return nil, err
	}
	d.Product = prod
	if d.Crumbs, err = collect[crumb](ctx, s, sqlBreadcrumbByID, prod.CategoryID); err != nil {
		return nil, err
	}
	promos, err := collect[promo](ctx, s, sqlActivePromo, id)
	if err != nil {
		return nil, err
	}
	if len(promos) > 0 {
		d.Promo = &promos[0]
		d.PromoPrice = math.Round(prod.PriceUSD*(100-d.Promo.DiscountPct)) / 100
	}
	if d.Stock, err = collect[stockRow](ctx, s, sqlStock, id); err != nil {
		return nil, err
	}
	for _, st := range d.Stock {
		d.TotalStock += st.Quantity
	}
	ratings, err := collect[ratingSummary](ctx, s, sqlRatingSummary, id)
	if err != nil {
		return nil, err
	}
	d.Ratings = ratings[0]
	if d.Reviews, err = collect[review](ctx, s, sqlLatestReviews, id); err != nil {
		return nil, err
	}
	if d.Prices, err = collect[pricePoint](ctx, s, sqlPriceHistory, id); err != nil {
		return nil, err
	}
	d.PriceChart = priceHistoryChart(d.Prices, p.Customer)

	d.Together, err = s.warehouseProducts(ctx, sqlBoughtTogether, id, func(row []any) (int64, int64, float64, int64) {
		return toInt(row[0]), toInt(row[1]), 0, 0
	})
	if err != nil {
		d.TogetherErr = err.Error()
	}
	return &view{Template: "product", Title: prod.Name, Data: d}, nil
}

// changePrice closes the current price version and opens a new one: the
// OLTP side of a Type 2 slowly changing dimension.
func (s *Server) changePrice(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, notFound("product")
	}
	price, err := strconv.ParseFloat(r.FormValue("price_usd"), 64)
	back := fmt.Sprintf("/p/%d", id)
	if err != nil || price <= 0 || price > 100000 {
		return &view{Redirect: back, Flash: "Enter a price between 0.01 and 100000 USD."}, nil
	}
	err = pgx.BeginFunc(ctx, s.pg, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `-- close the current price version
UPDATE product_prices SET valid_to = now()
WHERE product_id = $1 AND valid_to IS NULL`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return notFound("current price")
		}
		if _, err := tx.Exec(ctx, `-- open the new price version
INSERT INTO product_prices (product_id, valid_from, price_usd)
VALUES ($1, now(), $2)`, id, cents(math.Round(price*100))); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `-- keep the current price on the product row too
UPDATE products SET price_usd = $2 WHERE id = $1`, id, cents(math.Round(price*100)))
		return err
	})
	if err != nil {
		return nil, err
	}
	s.keepQueries(w, r, "change price")
	return &view{Redirect: back, Flash: fmt.Sprintf("Price changed to $%.2f. Postgres now has a new price version; run the ETL to see it in dw.dim_product.", price)}, nil
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

const sqlSearch = `-- search: ILIKE '%word%' can use the trigram (pg_trgm) GIN index
SELECT p.id, p.name, b.name AS brand, p.price_usd::float8 AS price_usd,
       0::bigint AS units, 0::float8 AS rating, 0::bigint AS reviews, 0::bigint AS total
FROM products p
JOIN brands b ON b.id = p.brand_id
WHERE p.name ILIKE '%' || $1 || '%'
  AND p.is_active
ORDER BY public.similarity(p.name, $1) DESC, p.id
LIMIT 48`

type searchData struct {
	Q        string
	Products []productCard
}

func (s *Server) search(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	d := searchData{Q: strings.TrimSpace(r.URL.Query().Get("q"))}
	if len(d.Q) >= 2 {
		var err error
		if d.Products, err = collect[productCard](r.Context(), s, sqlSearch, d.Q); err != nil {
			return nil, err
		}
	}
	return &view{Template: "search", Title: "Search", Data: d}, nil
}

// ---------------------------------------------------------------------------
// Account
// ---------------------------------------------------------------------------

type accountSummary struct {
	Email          string     `db:"email"`
	CreatedAt      time.Time  `db:"created_at"`
	AccountManager *string    `db:"account_manager"`
	Orders         int64      `db:"orders"`
	FirstOrder     *time.Time `db:"first_order"`
}

type person struct {
	Name  string `db:"name"`
	Title string `db:"title"`
	Depth int32  `db:"depth"`
}

type levelCount struct {
	Depth int32 `db:"depth"`
	Count int64 `db:"count"`
}

type address struct {
	Kind       string `db:"kind"`
	Line1      string `db:"line1"`
	City       string `db:"city"`
	PostalCode string `db:"postal_code"`
	IsDefault  bool   `db:"is_default"`
}

type country struct {
	Code string `db:"code"`
	Name string `db:"name"`
}

const sqlAccountSummary = `-- profile, account manager and order count
SELECT c.email, c.created_at,
       e.first_name || ' ' || e.last_name AS account_manager,
       count(o.id) AS orders,
       min(o.placed_at) AS first_order
FROM customers c
LEFT JOIN employees e ON e.id = c.account_manager_id
LEFT JOIN orders o ON o.customer_id = c.id AND o.status <> 'cancelled'
WHERE c.id = $1
GROUP BY c.id, e.id`

const sqlReferredBy = `-- who referred me, who referred them... (recursive CTE walks UP)
WITH RECURSIVE chain AS (
    SELECT id, first_name, last_name, referred_by_customer_id, 0 AS depth
    FROM customers WHERE id = $1
    UNION ALL
    SELECT c.id, c.first_name, c.last_name, c.referred_by_customer_id, chain.depth + 1
    FROM customers c
    JOIN chain ON c.id = chain.referred_by_customer_id
)
SELECT first_name || ' ' || last_name AS name, '' AS title, depth
FROM chain WHERE depth > 0 ORDER BY depth`

const sqlReferrals = `-- everyone I referred, directly or indirectly (recursive CTE walks DOWN)
WITH RECURSIVE tree AS (
    SELECT id, 1 AS depth FROM customers WHERE referred_by_customer_id = $1
    UNION ALL
    SELECT c.id, t.depth + 1
    FROM customers c
    JOIN tree t ON c.referred_by_customer_id = t.id
)
SELECT depth, count(*) AS count FROM tree GROUP BY depth ORDER BY depth`

const sqlManagerChain = `-- my account manager and their management chain up to the CEO
WITH RECURSIVE chain AS (
    SELECT e.id, e.manager_id, e.first_name || ' ' || e.last_name AS name, e.title, 0 AS depth
    FROM employees e
    JOIN customers c ON c.account_manager_id = e.id
    WHERE c.id = $1
    UNION ALL
    SELECT m.id, m.manager_id, m.first_name || ' ' || m.last_name, m.title, chain.depth + 1
    FROM employees m
    JOIN chain ON m.id = chain.manager_id
)
SELECT name, title, depth FROM chain ORDER BY depth`

const sqlAddresses = `-- addresses
SELECT kind, line1, city, postal_code, is_default
FROM addresses WHERE customer_id = $1
ORDER BY is_default DESC, id`

type accountData struct {
	Summary    accountSummary
	ReferredBy []person
	Referrals  []levelCount
	Managers   []person
	Addresses  []address
	Countries  []country
}

func (s *Server) account(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	ctx := r.Context()
	id := p.Customer.ID
	d := accountData{}
	rows, _ := s.pg.Query(ctx, sqlAccountSummary, id)
	summary, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[accountSummary])
	if err != nil {
		return nil, err
	}
	d.Summary = summary
	if d.ReferredBy, err = collect[person](ctx, s, sqlReferredBy, id); err != nil {
		return nil, err
	}
	if d.Referrals, err = collect[levelCount](ctx, s, sqlReferrals, id); err != nil {
		return nil, err
	}
	if d.Managers, err = collect[person](ctx, s, sqlManagerChain, id); err != nil {
		return nil, err
	}
	if d.Addresses, err = collect[address](ctx, s, sqlAddresses, id); err != nil {
		return nil, err
	}
	if d.Countries, err = collect[country](ctx, s, `-- countries for the switch form
SELECT code, name FROM countries ORDER BY name`); err != nil {
		return nil, err
	}
	return &view{Template: "account", Title: "Your account", Data: d}, nil
}

func (s *Server) switchCustomer(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	country := strings.ToUpper(strings.TrimSpace(r.FormValue("country")))
	if len(country) != 2 {
		country = ""
	}
	id, err := s.randomCustomer(r.Context(), country)
	if err != nil {
		return nil, err
	}
	setCustomerCookie(w, id)
	back := r.FormValue("back")
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") {
		back = "/account"
	}
	return &view{Redirect: back, Flash: fmt.Sprintf("You are now shopping as customer #%d.", id)}, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// collect runs a Postgres query and maps rows to structs by column name.
func collect[T any](ctx context.Context, s *Server, sql string, args ...any) ([]T, error) {
	rows, err := s.pg.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[T])
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func toInt(v any) int64 {
	f, _ := query.Float(v)
	return int64(f)
}

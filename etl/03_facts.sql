-- =============================================================================
-- ETL step 3: FACTS (the measurable events)
--
-- Facts are long and narrow: keys to dimensions plus numbers. Every money
-- value is converted to USD here, once, with the exchange rate that was valid
-- on the order date.
--
-- Tables are written ORDER BY date. DuckDB stores min/max values for each
-- block of rows (zone maps), so a WHERE on the date skips whole blocks. It is
-- the closest thing to a clustered index in a columnar engine.
-- =============================================================================

-- step: dw.fact_orders (grain: one order)
CREATE TABLE dw.fact_orders AS
WITH items AS (
    SELECT order_id, count(*) AS line_count, sum(quantity) AS units
    FROM raw.order_items
    GROUP BY ALL
),
pay AS (
    SELECT order_id,
           arg_max(method, paid_at)                  AS payment_method,   -- method of the LAST payment
           count(*) FILTER (WHERE status = 'failed') AS failed_payments
    FROM raw.payments
    GROUP BY ALL
),
ship AS (
    -- An order can have several shipments (one per warehouse). Keep the one
    -- that arrived last: the order is complete when its last parcel arrives.
    SELECT order_id, warehouse_id, carrier, shipped_at::TIMESTAMP AS shipped_at, delivered_at::TIMESTAMP AS delivered_at,
           count(*) OVER (PARTITION BY order_id) AS shipment_count
    FROM raw.shipments
    QUALIFY row_number() OVER (PARTITION BY order_id ORDER BY delivered_at DESC NULLS FIRST, shipped_at DESC NULLS FIRST) = 1
),
ret AS (
    SELECT order_id, count(*) AS returned_lines, sum(refund_amount) AS refund_local
    FROM raw.returns
    GROUP BY ALL
)
SELECT
    o.id                                                     AS order_id,
    year(o.placed_at::DATE) * 10000 + month(o.placed_at::DATE) * 100 + day(o.placed_at::DATE) AS date_key,
    o.placed_at::DATE                                        AS order_date,
    o.placed_at::TIMESTAMP                                   AS placed_at,
    o.customer_id,
    o.status,
    o.promotion_id,
    o.currency_code,
    fx.units_per_usd                                         AS fx_rate,
    items.line_count,
    items.units,
    o.total                                                  AS total_local,
    round(o.subtotal / fx.units_per_usd, 2)::DECIMAL(14,2)   AS subtotal_usd,
    round(o.discount / fx.units_per_usd, 2)::DECIMAL(14,2)   AS discount_usd,
    round(o.shipping_fee / fx.units_per_usd, 2)::DECIMAL(14,2) AS shipping_usd,
    round(o.tax / fx.units_per_usd, 2)::DECIMAL(14,2)        AS tax_usd,
    round(o.total / fx.units_per_usd, 2)::DECIMAL(14,2)      AS total_usd,
    pay.payment_method,
    pay.failed_payments,
    ship.warehouse_id,
    ship.shipment_count,
    ship.carrier,
    ship.shipped_at,
    ship.delivered_at,
    round(date_diff('minute', o.placed_at::TIMESTAMP, ship.shipped_at) / 1440.0, 2)   AS days_to_ship,
    round(date_diff('minute', ship.shipped_at, ship.delivered_at) / 1440.0, 2)        AS transit_days,
    coalesce(ret.returned_lines, 0)                          AS returned_lines,
    round(coalesce(ret.refund_local, 0) / fx.units_per_usd, 2)::DECIMAL(14,2) AS refund_usd,
    -- 1 = the customer's first order, 2 = second, ... (used for new vs returning)
    row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at, o.id) AS customer_order_number
FROM raw.orders o
-- ASOF JOIN: for each order, the fx row with the same currency and the
-- LATEST rate_date that is <= the order date. Weekends have no rate, so an
-- exact join would lose every weekend order.
-- T-SQL: OUTER APPLY (SELECT TOP 1 ... WHERE rate_date <= @d ORDER BY rate_date DESC)
ASOF LEFT JOIN raw.fx_rates fx
       ON fx.currency_code = o.currency_code
      AND o.placed_at::DATE >= fx.rate_date
LEFT JOIN items ON items.order_id = o.id
LEFT JOIN pay   ON pay.order_id   = o.id
LEFT JOIN ship  ON ship.order_id  = o.id
LEFT JOIN ret   ON ret.order_id   = o.id
ORDER BY o.placed_at;

-- step: dw.fact_sales (grain: one order line)
-- Cancelled orders are excluded: they are not sales.
CREATE TABLE dw.fact_sales AS
SELECT
    oi.order_id,
    oi.line_no,
    fo.date_key,
    fo.order_date,
    fo.placed_at,
    fo.customer_id,
    dp.product_key,                         -- the product VERSION valid at order time (SCD2)
    oi.product_id,
    fo.promotion_id,
    oi.warehouse_id,                        -- where the line shipped from
    fo.status                               AS order_status,
    fo.currency_code,
    fo.fx_rate,
    oi.quantity,
    oi.unit_price                           AS unit_price_local,
    round(oi.unit_price * oi.quantity / fo.fx_rate, 2)::DECIMAL(14,2)               AS gross_usd,
    round(oi.discount / fo.fx_rate, 2)::DECIMAL(14,2)                               AS discount_usd,
    round((oi.unit_price * oi.quantity - oi.discount) / fo.fx_rate, 2)::DECIMAL(14,2) AS net_usd,
    (dp.cost_usd * oi.quantity)::DECIMAL(14,2)                                     AS cost_usd,
    -- A column alias defined above can be used in the same SELECT list.
    -- SQL Server does not allow this; you would repeat the expression.
    net_usd - cost_usd                      AS margin_usd
FROM raw.order_items oi
JOIN dw.fact_orders fo
  ON fo.order_id = oi.order_id
-- SCD2 lookup: equality on the business key + the order time inside the version's range.
JOIN dw.dim_product dp
  ON dp.product_id = oi.product_id
 AND fo.placed_at >= dp.valid_from
 AND fo.placed_at <  dp.valid_to
WHERE fo.status <> 'cancelled'
ORDER BY fo.placed_at, oi.order_id, oi.line_no;

-- step: dw.fact_returns (grain: one return)
CREATE TABLE dw.fact_returns AS
SELECT
    r.id                             AS return_id,
    r.order_id,
    r.line_no,
    r.requested_at::DATE             AS return_date,
    fs.order_date,
    fs.customer_id,
    fs.product_key,
    fs.product_id,
    fs.warehouse_id,
    r.quantity,
    r.reason,
    r.status,
    round(r.refund_amount / fs.fx_rate, 2)::DECIMAL(14,2) AS refund_usd,
    date_diff('day', fs.order_date, r.requested_at::DATE)  AS days_after_order
FROM raw.returns r
JOIN dw.fact_sales fs USING (order_id, line_no)
ORDER BY return_date;

-- step: dw.fact_reviews (grain: one review)
CREATE TABLE dw.fact_reviews AS
SELECT
    id AS review_id, product_id, customer_id, rating, title,
    created_at::DATE AS review_date
FROM raw.reviews
ORDER BY review_date;

-- step: dw.fact_inventory (snapshot at ETL time)
CREATE TABLE dw.fact_inventory AS
SELECT
    i.warehouse_id,
    i.product_id,
    i.quantity_on_hand,
    i.reorder_point,
    i.quantity_on_hand <= i.reorder_point              AS needs_reorder,
    (i.quantity_on_hand * p.cost_usd)::DECIMAL(14,2)    AS stock_value_usd
FROM raw.inventory i
JOIN raw.products p ON p.id = i.product_id;

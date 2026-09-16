-- Postgres names parameters @customer_id here (Npgsql); DuckDB uses $customer_id.
SELECT order_id, placed_at, status, line_count, total_usd
FROM dw.fact_orders
WHERE customer_id = @customer_id
ORDER BY placed_at DESC, order_id DESC
LIMIT 20

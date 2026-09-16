-- title: 06 · LIST, STRUCT, MAP and JSON
-- engine: duckdb
--
-- SQL Server has scalar columns and JSON strings. DuckDB also has real
-- nested types, so a value can be a list, a record or a map, and still be
-- stored in columns and queried fast.

-- LIST: the products of an order, in line order, as one value.
-- (T-SQL STRING_AGG gives a string; this stays a list you can index and filter.)
SELECT fs.order_id,
       list(dp.product_name ORDER BY fs.line_no) AS products,
       len(list(dp.product_name))                AS line_count
FROM dw.fact_sales fs
JOIN dw.dim_product dp USING (product_key)
WHERE fs.order_id BETWEEN 1000 AND 1005
GROUP BY fs.order_id
ORDER BY fs.order_id;

-- Lambdas: filter, transform and slice lists without unnesting them.
SELECT [10, 25, 40, 60]                            AS prices,
       list_filter(prices, lambda p: p > 20)       AS over_20,
       list_transform(prices, lambda p: p * 0.9)   AS with_10_pct_off,
       list_sum(prices)                            AS total,
       prices[2:3]                                 AS second_and_third;

-- unnest: a list back into rows (T-SQL: CROSS APPLY OPENJSON / STRING_SPLIT).
SELECT management_chain_ids, unnest(management_chain_ids) AS manager_id
FROM dw.dim_employee
WHERE employee_id = 100;

-- STRUCT: a record inside a column. max_by returns the whole record of the
-- biggest order for each customer.
SELECT customer_id, biggest_order, biggest_order.total_usd AS biggest_total_usd
FROM (
    SELECT customer_id,
           max_by({'order_id': order_id, 'total_usd': total_usd, 'order_date': order_date}, total_usd) AS biggest_order
    FROM dw.fact_orders
    GROUP BY customer_id
)
ORDER BY biggest_total_usd DESC
LIMIT 5;

-- MAP: key/value pairs. histogram() counts values into a map.
SELECT year(order_date)            AS year,
       histogram(payment_method)   AS orders_by_method
FROM dw.fact_orders
WHERE payment_method IS NOT NULL
GROUP BY ALL
ORDER BY year;

-- JSON: products.attributes was a jsonb column in Postgres.
SELECT id,
       attributes,
       attributes ->> '$.color'                   AS color,
       (attributes ->> '$.warranty_months')::INT  AS warranty_months,
       json_array_length(attributes -> '$.sizes') AS size_count
FROM raw.products
WHERE (attributes -> '$.sizes') IS NOT NULL   -- parentheses needed: -> binds looser than IS NOT NULL
ORDER BY id
LIMIT 5;

-- Which keys exist in the JSON, and how often?
SELECT key, count(*) AS products
FROM (SELECT unnest(json_keys(attributes)) AS key FROM raw.products)
GROUP BY ALL
ORDER BY products DESC;

-- json_transform: JSON text into a typed STRUCT, then plain column access.
SELECT attrs.color, attrs.warranty_months, attrs.sizes
FROM (
    SELECT json_transform(attributes, '{"color": "VARCHAR", "warranty_months": "INTEGER", "sizes": "VARCHAR[]"}') AS attrs
    FROM raw.products
    ORDER BY id
    LIMIT 5
);

# The data model

## The store (Postgres, schema `store`)

A normalized model for the application. Files: [01_tables.sql](../internal/pg/schema/01_tables.sql) and
[02_constraints.sql](../internal/pg/schema/02_constraints.sql).

```mermaid
erDiagram
    currencies ||--o{ countries : "used by"
    currencies ||--o{ fx_rates : "has daily"
    countries ||--o{ customers : "lives in"
    countries ||--o{ warehouses : "located in"
    countries ||--o{ suppliers : "based in"
    customers ||--o{ customers : "referred"
    employees ||--o{ employees : "manages"
    employees ||--o{ customers : "account manager of"
    warehouses ||--o{ employees : "employs"
    employees |o--o| warehouses : "manages (circular)"
    customers ||--o{ addresses : has
    categories ||--o{ categories : "parent of"
    categories ||--o{ products : contains
    brands ||--o{ products : makes
    products ||--o{ product_prices : "price versions"
    products ||--o{ product_suppliers : "supplied by"
    suppliers ||--o{ product_suppliers : supplies
    products ||--o{ inventory : stocked
    warehouses ||--o{ inventory : holds
    promotions ||--o{ promotion_products : covers
    products ||--o{ promotion_products : "on promotion"
    customers ||--o| carts : has
    carts ||--o{ cart_items : contains
    customers ||--o{ orders : places
    addresses ||--o{ orders : "ships to"
    promotions |o--o{ orders : "applied to"
    orders ||--|{ order_items : "has lines"
    products ||--o{ order_items : "sold as"
    warehouses ||--o{ order_items : "ships line"
    orders ||--o{ payments : "paid by"
    orders ||--o{ shipments : "shipped in"
    warehouses ||--o{ shipments : "ships from"
    order_items ||--o{ returns : "returned (order_id, line_no)"
    products ||--o{ reviews : reviewed
    customers ||--o{ reviews : writes
```

Relational features worth noticing:

| Feature | Where |
|---|---|
| Self-referencing hierarchies | `categories.parent_id` (tree of 2-4 levels), `employees.manager_id` (6 levels), `customers.referred_by_customer_id` (chains) |
| Circular foreign keys | `warehouses.manager_employee_id` ↔ `employees.warehouse_id` |
| Many-to-many with attributes | `product_suppliers` (cost, lead time, primary), `promotion_products` |
| Composite primary keys | `order_items (order_id, line_no)`, `inventory (warehouse_id, product_id)`, `fx_rates (currency_code, rate_date)` |
| Composite foreign key | `returns (order_id, line_no)` → `order_items` |
| Temporal table | `product_prices (valid_from, valid_to)`, with a filtered unique index allowing one open version per product |
| Business rules as constraints | `CHECK (total = subtotal - discount + shipping_fee + tax)`, status lists, shipment state consistency |
| Semi-structured data | `products.attributes jsonb` |
| One cart per customer | `UNIQUE (customer_id)`, used by `INSERT ... ON CONFLICT` |

Money in `orders`, `order_items`, `payments` and `returns` is in the **order's currency**. Converting to USD
needs the exchange rate of the order date, and exchange rates only exist for business days.

## The warehouse (DuckDB, schemas `raw` and `dw`)

`raw.*` holds untouched copies of the store tables (used by the race to compare engines on the same model).
`dw.*` is a star schema built by [the ETL](../etl/).

```mermaid
erDiagram
    dim_date ||--o{ fact_sales : date_key
    dim_customer ||--o{ fact_sales : customer_id
    dim_product ||--o{ fact_sales : "product_key (version)"
    dim_warehouse ||--o{ fact_sales : warehouse_id
    dim_promotion |o--o{ fact_sales : promotion_id
    dim_category ||--o{ dim_product : category_id
    dim_employee ||--o{ dim_customer : account_manager_id
    dim_date ||--o{ fact_orders : date_key
    dim_customer ||--o{ fact_orders : customer_id
    fact_sales ||--o{ fact_returns : "(order_id, line_no)"
    dim_product ||--o{ fact_returns : product_key
    dim_customer ||--o{ fact_reviews : customer_id
    dim_warehouse ||--o{ fact_inventory : warehouse_id

    fact_sales {
        bigint order_id
        smallint line_no
        int date_key
        bigint product_key
        int quantity
        decimal net_usd
        decimal cost_usd
        decimal margin_usd
    }
    fact_orders {
        bigint order_id
        date order_date
        varchar status
        decimal total_usd
        varchar carrier
        double transit_days
        bigint customer_order_number
    }
    dim_product {
        bigint product_key
        bigint product_id
        varchar category_l1
        decimal list_price_usd
        timestamp valid_from
        timestamp valid_to
        boolean is_current
    }
    dim_employee {
        bigint employee_id
        int level
        list management_chain_ids
    }
```

| Table | Grain | Built with |
|---|---|---|
| `fact_sales` | one order line (cancelled orders excluded) | SCD2 range join to `dim_product`, USD via `fact_orders` |
| `fact_orders` | one order | `ASOF JOIN` to exchange rates, `arg_max` for the last payment, `row_number()` for the customer's order number |
| `fact_returns`, `fact_reviews` | one return, one review | |
| `fact_inventory` | warehouse × product, at ETL time | a snapshot |
| `dim_product` | one row per price version | Type 2 slowly changing dimension from `product_prices` |
| `dim_category` | one row per category, with its full path | recursive CTE carrying a `LIST` |
| `dim_employee` | one row per employee, with the chain of managers | recursive CTE carrying a `LIST` of ids |
| `dim_customer` | one row per customer (Type 1) | recursive CTE for referral depth |
| `dim_date` | one row per day | `generate_series` |
| `product_pairs`, `product_stats` | marts for the store pages | self-join of `fact_sales`, `QUALIFY` |

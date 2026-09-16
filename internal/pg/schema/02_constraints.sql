-- =============================================================================
-- Keys and indexes, created after the bulk load.
--
-- Postgres, like SQL Server, does NOT index foreign key columns automatically.
-- Every FK that the application joins or filters on gets its own index here.
-- =============================================================================
SET search_path = store;

-- Primary keys ----------------------------------------------------------------
ALTER TABLE currencies         ADD PRIMARY KEY (code);
ALTER TABLE countries          ADD PRIMARY KEY (code);
ALTER TABLE fx_rates           ADD PRIMARY KEY (currency_code, rate_date);
ALTER TABLE employees          ADD PRIMARY KEY (id);
ALTER TABLE warehouses         ADD PRIMARY KEY (id);
ALTER TABLE customers          ADD PRIMARY KEY (id);
ALTER TABLE addresses          ADD PRIMARY KEY (id);
ALTER TABLE categories         ADD PRIMARY KEY (id);
ALTER TABLE brands             ADD PRIMARY KEY (id);
ALTER TABLE suppliers          ADD PRIMARY KEY (id);
ALTER TABLE products           ADD PRIMARY KEY (id);
ALTER TABLE product_prices     ADD PRIMARY KEY (product_id, valid_from);
ALTER TABLE product_suppliers  ADD PRIMARY KEY (product_id, supplier_id);
ALTER TABLE inventory          ADD PRIMARY KEY (warehouse_id, product_id);
ALTER TABLE promotions         ADD PRIMARY KEY (id);
ALTER TABLE promotion_products ADD PRIMARY KEY (promotion_id, product_id);
ALTER TABLE carts              ADD PRIMARY KEY (id);
ALTER TABLE cart_items         ADD PRIMARY KEY (cart_id, product_id);
ALTER TABLE orders             ADD PRIMARY KEY (id);
ALTER TABLE order_items        ADD PRIMARY KEY (order_id, line_no);
ALTER TABLE payments           ADD PRIMARY KEY (id);
ALTER TABLE shipments          ADD PRIMARY KEY (id);
ALTER TABLE returns            ADD PRIMARY KEY (id);
ALTER TABLE reviews            ADD PRIMARY KEY (id);

-- Alternate keys (unique constraints) ----------------------------------------
ALTER TABLE warehouses ADD CONSTRAINT warehouses_code_key  UNIQUE (code);
ALTER TABLE customers  ADD CONSTRAINT customers_email_key  UNIQUE (email);
ALTER TABLE categories ADD CONSTRAINT categories_slug_key  UNIQUE (slug);
ALTER TABLE products   ADD CONSTRAINT products_sku_key     UNIQUE (sku);
ALTER TABLE promotions ADD CONSTRAINT promotions_code_key  UNIQUE (code);
ALTER TABLE carts      ADD CONSTRAINT carts_customer_key   UNIQUE (customer_id); -- one open cart per customer
ALTER TABLE reviews    ADD CONSTRAINT reviews_one_per_customer UNIQUE (product_id, customer_id);

-- Only one current (open) price per product: a filtered unique index.
-- SQL Server equivalent: CREATE UNIQUE INDEX ... WHERE valid_to IS NULL.
CREATE UNIQUE INDEX product_prices_one_current ON product_prices (product_id) WHERE valid_to IS NULL;

-- Foreign keys ----------------------------------------------------------------
ALTER TABLE countries          ADD FOREIGN KEY (currency_code)           REFERENCES currencies (code);
ALTER TABLE fx_rates           ADD FOREIGN KEY (currency_code)           REFERENCES currencies (code);
ALTER TABLE employees          ADD FOREIGN KEY (manager_id)              REFERENCES employees (id);
ALTER TABLE employees          ADD FOREIGN KEY (warehouse_id)            REFERENCES warehouses (id);
ALTER TABLE warehouses         ADD FOREIGN KEY (country_code)            REFERENCES countries (code);
ALTER TABLE warehouses         ADD FOREIGN KEY (manager_employee_id)     REFERENCES employees (id);
ALTER TABLE customers          ADD FOREIGN KEY (country_code)            REFERENCES countries (code);
ALTER TABLE customers          ADD FOREIGN KEY (referred_by_customer_id) REFERENCES customers (id);
ALTER TABLE customers          ADD FOREIGN KEY (account_manager_id)      REFERENCES employees (id);
ALTER TABLE addresses          ADD FOREIGN KEY (customer_id)             REFERENCES customers (id);
ALTER TABLE addresses          ADD FOREIGN KEY (country_code)            REFERENCES countries (code);
ALTER TABLE categories         ADD FOREIGN KEY (parent_id)               REFERENCES categories (id);
ALTER TABLE suppliers          ADD FOREIGN KEY (country_code)            REFERENCES countries (code);
ALTER TABLE products           ADD FOREIGN KEY (category_id)             REFERENCES categories (id);
ALTER TABLE products           ADD FOREIGN KEY (brand_id)                REFERENCES brands (id);
ALTER TABLE product_prices     ADD FOREIGN KEY (product_id)              REFERENCES products (id);
ALTER TABLE product_suppliers  ADD FOREIGN KEY (product_id)              REFERENCES products (id);
ALTER TABLE product_suppliers  ADD FOREIGN KEY (supplier_id)             REFERENCES suppliers (id);
ALTER TABLE inventory          ADD FOREIGN KEY (warehouse_id)            REFERENCES warehouses (id);
ALTER TABLE inventory          ADD FOREIGN KEY (product_id)              REFERENCES products (id);
ALTER TABLE promotion_products ADD FOREIGN KEY (promotion_id)            REFERENCES promotions (id);
ALTER TABLE promotion_products ADD FOREIGN KEY (product_id)              REFERENCES products (id);
ALTER TABLE carts              ADD FOREIGN KEY (customer_id)             REFERENCES customers (id);
ALTER TABLE cart_items         ADD FOREIGN KEY (cart_id)                 REFERENCES carts (id) ON DELETE CASCADE;
ALTER TABLE cart_items         ADD FOREIGN KEY (product_id)              REFERENCES products (id);
ALTER TABLE orders             ADD FOREIGN KEY (customer_id)             REFERENCES customers (id);
ALTER TABLE orders             ADD FOREIGN KEY (shipping_address_id)     REFERENCES addresses (id);
ALTER TABLE orders             ADD FOREIGN KEY (promotion_id)            REFERENCES promotions (id);
ALTER TABLE orders             ADD FOREIGN KEY (currency_code)           REFERENCES currencies (code);
ALTER TABLE order_items        ADD FOREIGN KEY (order_id)                REFERENCES orders (id);
ALTER TABLE order_items        ADD FOREIGN KEY (product_id)              REFERENCES products (id);
ALTER TABLE order_items        ADD FOREIGN KEY (warehouse_id)            REFERENCES warehouses (id);
ALTER TABLE payments           ADD FOREIGN KEY (order_id)                REFERENCES orders (id);
ALTER TABLE shipments          ADD FOREIGN KEY (order_id)                REFERENCES orders (id);
ALTER TABLE shipments          ADD FOREIGN KEY (warehouse_id)            REFERENCES warehouses (id);
ALTER TABLE returns            ADD FOREIGN KEY (order_id, line_no)       REFERENCES order_items (order_id, line_no);
ALTER TABLE reviews            ADD FOREIGN KEY (product_id)              REFERENCES products (id);
ALTER TABLE reviews            ADD FOREIGN KEY (customer_id)             REFERENCES customers (id);

-- Indexes for the application's access paths ---------------------------------
CREATE INDEX employees_manager_idx       ON employees (manager_id);
CREATE INDEX customers_referred_by_idx   ON customers (referred_by_customer_id);
CREATE INDEX customers_account_mgr_idx   ON customers (account_manager_id);
CREATE INDEX addresses_customer_idx      ON addresses (customer_id);
CREATE INDEX categories_parent_idx       ON categories (parent_id);
CREATE INDEX products_category_idx       ON products (category_id);
CREATE INDEX products_brand_idx          ON products (brand_id);
CREATE INDEX products_name_trgm_idx      ON products USING gin (name public.gin_trgm_ops); -- ILIKE '%word%'
CREATE INDEX product_suppliers_supp_idx  ON product_suppliers (supplier_id);
CREATE INDEX inventory_product_idx       ON inventory (product_id);
CREATE INDEX promotion_products_prod_idx ON promotion_products (product_id);
CREATE INDEX orders_customer_placed_idx  ON orders (customer_id, placed_at DESC); -- "my orders" page
CREATE INDEX orders_placed_idx           ON orders (placed_at);
CREATE INDEX order_items_product_idx     ON order_items (product_id);
CREATE INDEX payments_order_idx          ON payments (order_id);
CREATE INDEX shipments_order_idx         ON shipments (order_id);
CREATE INDEX returns_order_line_idx      ON returns (order_id, line_no);
CREATE INDEX reviews_product_created_idx ON reviews (product_id, created_at DESC);
CREATE INDEX reviews_customer_idx        ON reviews (customer_id);

-- Identity columns: move each sequence past the loaded ids, like
-- DBCC CHECKIDENT (..., RESEED) after SET IDENTITY_INSERT ON in SQL Server.
DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['employees', 'warehouses', 'customers', 'addresses', 'categories', 'brands',
                             'suppliers', 'products', 'promotions', 'carts', 'orders', 'payments',
                             'shipments', 'returns', 'reviews']
    LOOP
        EXECUTE format(
            'SELECT setval(pg_get_serial_sequence(%L, ''id''), COALESCE((SELECT max(id) FROM store.%I), 0) + 1, false)',
            'store.' || t, t);
    END LOOP;
END $$;

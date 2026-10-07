-- NuttyWonders app schema
-- Storefront orders + WhatsApp (whatsmeow) bridge support.
--
-- Access model: the browser never talks to Postgres. The Go bridge
-- (apps/bridge) is the only client, using the service_role key. Every table
-- therefore has RLS enabled with policies for authenticated admins only —
-- anon gets zero rows even where the Data API default grants apply.

-- ---------------------------------------------------------------------------
-- shared trigger
-- ---------------------------------------------------------------------------
create or replace function public.set_updated_at()
returns trigger
language plpgsql
as $$
begin
  new.updated_at := now();
  return new;
end;
$$;

-- ---------------------------------------------------------------------------
-- catalogue
-- ---------------------------------------------------------------------------
create table public.products (
  id                      bigint generated always as identity primary key,
  slug                    text not null unique,
  name                    text not null,
  tagline                 text,
  description             text,
  category                text not null default 'bars'
                            check (category in ('granola', 'bites', 'bars')),
  image_path              text,
  pack_size_g             integer not null default 200 check (pack_size_g > 0),
  -- grams one batch of the recipe below yields; lets us turn a production
  -- batch into a per-pack ingredient cost
  batch_output_g          integer check (batch_output_g > 0),
  shelf_life_days         integer not null default 20 check (shelf_life_days > 0),
  ingredients             text[] not null default '{}',
  allergens               text[] not null default '{}',
  price_inr               numeric(10, 2) not null check (price_inr >= 0),
  cost_ingredient_inr     numeric(10, 2) not null default 0 check (cost_ingredient_inr >= 0),
  cost_packaging_inr      numeric(10, 2) not null default 0 check (cost_packaging_inr >= 0),
  cost_labour_inr         numeric(10, 2) not null default 0 check (cost_labour_inr >= 0),
  cost_overhead_inr       numeric(10, 2) not null default 0 check (cost_overhead_inr >= 0),
  stock_packs             integer not null default 0 check (stock_packs >= 0),
  is_active               boolean not null default true,
  sort_order              integer not null default 100,
  created_at              timestamptz not null default now(),
  updated_at              timestamptz not null default now()
);

comment on table public.products is 'Sellable packs. Economics mirror the costing mastersheet.';

create table public.ingredients (
  id                bigint generated always as identity primary key,
  name              text not null unique,
  price_per_kg_inr  numeric(10, 2) check (price_per_kg_inr >= 0),
  suggested_brand   text,
  is_active         boolean not null default true,
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now()
);

create table public.recipe_items (
  id            bigint generated always as identity primary key,
  product_id    bigint not null references public.products (id) on delete cascade,
  ingredient_id bigint not null references public.ingredients (id) on delete restrict,
  qty_g         numeric(10, 2) not null check (qty_g > 0),
  unique (product_id, ingredient_id)
);

-- ---------------------------------------------------------------------------
-- people + orders
-- ---------------------------------------------------------------------------
create table public.customers (
  id               bigint generated always as identity primary key,
  phone_e164       text not null unique,
  name             text,
  email            text,
  address_line1    text,
  address_line2    text,
  city             text,
  pincode          text,
  landmark         text,
  marketing_opt_in boolean not null default false,
  unsubscribed     boolean not null default false,
  notes            text,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now()
);

create table public.orders (
  id               bigint generated always as identity primary key,
  code             text not null unique,
  customer_id      bigint references public.customers (id) on delete set null,
  phone_e164       text not null,
  customer_name    text not null,
  channel          text not null default 'web'
                     check (channel in ('web', 'whatsapp', 'manual')),
  status           text not null default 'new'
                     check (status in ('new', 'confirmed', 'packed', 'shipped',
                                       'delivered', 'cancelled')),
  payment_status   text not null default 'pending'
                     check (payment_status in ('pending', 'paid', 'cod', 'failed', 'refunded')),
  subtotal_inr     numeric(10, 2) not null default 0 check (subtotal_inr >= 0),
  discount_inr     numeric(10, 2) not null default 0 check (discount_inr >= 0),
  shipping_inr     numeric(10, 2) not null default 0 check (shipping_inr >= 0),
  total_inr        numeric(10, 2) not null default 0 check (total_inr >= 0),
  address_line1    text,
  address_line2    text,
  city             text,
  pincode          text,
  landmark         text,
  delivery_note    text,
  internal_note    text,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now(),
  constraint orders_total_ck
    check (total_inr = subtotal_inr - discount_inr + shipping_inr)
);

create table public.order_items (
  id                bigint generated always as identity primary key,
  order_id          bigint not null references public.orders (id) on delete cascade,
  product_id        bigint references public.products (id) on delete set null,
  product_name      text not null,
  pack_size_g       integer not null default 200 check (pack_size_g > 0),
  quantity          integer not null check (quantity > 0),
  unit_price_inr    numeric(10, 2) not null check (unit_price_inr >= 0),
  line_total_inr    numeric(10, 2) not null check (line_total_inr >= 0),
  constraint order_items_line_ck check (line_total_inr = unit_price_inr * quantity)
);

create index order_items_order_id_idx on public.order_items (order_id);
create index orders_customer_id_idx on public.orders (customer_id);
create index orders_phone_idx on public.orders (phone_e164, created_at desc);
-- the admin "open orders" board and the storefront order-status page
create index orders_open_idx on public.orders (created_at desc)
  where status not in ('delivered', 'cancelled');
create index orders_status_idx on public.orders (status, created_at desc);

-- ---------------------------------------------------------------------------
-- whatsapp
-- ---------------------------------------------------------------------------
create table public.broadcasts (
  id             bigint generated always as identity primary key,
  title          text not null,
  body           text not null,
  segment        text not null default 'past_customers'
                   check (segment in ('all_customers', 'opt_in', 'past_customers', 'pending_orders')),
  status         text not null default 'draft'
                   check (status in ('draft', 'queued', 'sending', 'done', 'cancelled')),
  audience_count integer not null default 0 check (audience_count >= 0),
  sent_count     integer not null default 0 check (sent_count >= 0),
  created_at     timestamptz not null default now(),
  sent_at        timestamptz
);

create table public.whatsapp_jobs (
  id              bigint generated always as identity primary key,
  to_phone        text not null,
  body            text not null,
  kind            text not null default 'custom'
                    check (kind in ('order_confirmation', 'order_alert', 'order_update',
                                    'broadcast', 'auto_reply', 'custom')),
  status          text not null default 'queued'
                    check (status in ('queued', 'sending', 'sent', 'failed', 'cancelled')),
  order_id        bigint references public.orders (id) on delete cascade,
  broadcast_id    bigint references public.broadcasts (id) on delete cascade,
  attempts        integer not null default 0 check (attempts >= 0),
  last_error      text,
  idempotency_key text unique,
  claimed_at      timestamptz,
  claimed_by      text,
  created_at      timestamptz not null default now(),
  sent_at         timestamptz
);

-- the bridge poller only ever scans queued rows
create index whatsapp_jobs_queue_idx on public.whatsapp_jobs (id)
  where status = 'queued';
create index whatsapp_jobs_order_id_idx on public.whatsapp_jobs (order_id);

create table public.whatsapp_messages (
  id           bigint generated always as identity primary key,
  direction    text not null check (direction in ('in', 'out')),
  phone_e164   text not null,
  body         text,
  message_key  text,
  status       text not null default 'received',
  order_id     bigint references public.orders (id) on delete set null,
  created_at   timestamptz not null default now()
);

create index whatsapp_messages_phone_idx on public.whatsapp_messages (phone_e164, created_at desc);
create index whatsapp_messages_created_idx on public.whatsapp_messages (created_at desc);

create table public.whatsapp_connection (
  id          smallint primary key default 1 check (id = 1),
  jid         text,
  status      text not null default 'disconnected'
                check (status in ('disconnected', 'pairing', 'connected', 'error')),
  device      jsonb,
  qr_code     text,
  last_error  text,
  updated_at  timestamptz not null default now()
);

create table public.app_settings (
  key        text primary key,
  value      jsonb not null,
  updated_at timestamptz not null default now()
);

-- ---------------------------------------------------------------------------
-- triggers
-- ---------------------------------------------------------------------------
create trigger products_set_updated_at before update on public.products
  for each row execute function public.set_updated_at();
create trigger ingredients_set_updated_at before update on public.ingredients
  for each row execute function public.set_updated_at();
create trigger customers_set_updated_at before update on public.customers
  for each row execute function public.set_updated_at();
create trigger orders_set_updated_at before update on public.orders
  for each row execute function public.set_updated_at();
create trigger app_settings_set_updated_at before update on public.app_settings
  for each row execute function public.set_updated_at();

-- ---------------------------------------------------------------------------
-- row level security
-- ---------------------------------------------------------------------------
alter table public.products enable row level security;
alter table public.ingredients enable row level security;
alter table public.recipe_items enable row level security;
alter table public.customers enable row level security;
alter table public.orders enable row level security;
alter table public.order_items enable row level security;
alter table public.broadcasts enable row level security;
alter table public.whatsapp_jobs enable row level security;
alter table public.whatsapp_messages enable row level security;
alter table public.whatsapp_connection enable row level security;
alter table public.app_settings enable row level security;

-- `authenticated` = someone signed in through Supabase Auth with
-- app_metadata.role = 'admin'. app_metadata (not user_metadata) because
-- user_metadata is user-writable. Wrapped in select() so the JWT is read
-- once per query instead of once per row.
create policy admin_all on public.products
  for all to authenticated
  using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin')
  with check ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

create policy admin_all on public.ingredients
  for all to authenticated
  using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin')
  with check ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

create policy admin_all on public.recipe_items
  for all to authenticated
  using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin')
  with check ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

create policy admin_all on public.customers
  for all to authenticated
  using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin')
  with check ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

create policy admin_all on public.orders
  for all to authenticated
  using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin')
  with check ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

create policy admin_all on public.order_items
  for all to authenticated
  using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin')
  with check ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

create policy admin_all on public.broadcasts
  for all to authenticated
  using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin')
  with check ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

create policy admin_read on public.whatsapp_jobs
  for select to authenticated
  using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

create policy admin_all on public.whatsapp_jobs
  for update to authenticated
  using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin')
  with check ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

create policy admin_read on public.whatsapp_messages
  for select to authenticated
  using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

create policy admin_read on public.whatsapp_connection
  for select to authenticated
  using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

create policy admin_read on public.app_settings
  for select to authenticated
  using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

create policy admin_write on public.app_settings
  for all to authenticated
  using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin')
  with check ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

-- anon/authenticated intentionally have no insert policies on orders or
-- customers: those tables are only written by the bridge via service_role.

-- ---------------------------------------------------------------------------
-- reporting view (inherits RLS from the base tables)
-- ---------------------------------------------------------------------------
create view public.product_economics
with (security_invoker = true)
as
select
  p.id,
  p.slug,
  p.name,
  p.pack_size_g,
  p.price_inr,
  p.cost_packaging_inr,
  p.cost_labour_inr,
  p.cost_overhead_inr,
  p.cost_ingredient_inr,
  (p.cost_ingredient_inr + p.cost_packaging_inr + p.cost_labour_inr + p.cost_overhead_inr)
    as cost_total_inr,
  round(
    100 * (p.price_inr - (p.cost_ingredient_inr + p.cost_packaging_inr + p.cost_labour_inr + p.cost_overhead_inr))
      / nullif(p.price_inr, 0)
  ) as margin_pct
from public.products p
where p.is_active;

-- ---------------------------------------------------------------------------
-- rpc helpers (invoker rights — RLS above still applies)
-- ---------------------------------------------------------------------------

-- Human-readable order codes: NW-YYMMDD-0007
create sequence public.order_code_seq;

create or replace function public.get_setting(p_key text, p_default text default null)
returns text
language sql
stable
as $$
  select coalesce(
    (select value #>> '{}' from public.app_settings where key = p_key),
    p_default
  );
$$;

-- Worker side: take queued jobs without blocking other workers.
create or replace function public.claim_whatsapp_jobs(p_worker text, p_limit integer default 10)
returns setof public.whatsapp_jobs
language sql
as $$
  with picked as (
    select id
    from public.whatsapp_jobs
    where status = 'queued'
    order by id
    for update skip locked
    limit greatest(1, least(coalesce(p_limit, 10), 50))
  )
  update public.whatsapp_jobs j
     set status = 'sending',
         claimed_at = now(),
         claimed_by = p_worker
    from picked
   where j.id = picked.id
  returning j.*;
$$;

-- Place an order atomically. Prices, shipping and the customer record are all
-- resolved server-side so a tampered cart cannot change what is charged.
create or replace function public.place_order(
  p_phone        text,
  p_name         text,
  p_items        jsonb,
  p_address      jsonb default '{}'::jsonb,
  p_channel      text default 'web',
  p_delivery_note text default null,
  p_reply_to_owner boolean default true
)
returns jsonb
language plpgsql
as $$
declare
  v_customer_id bigint;
  v_order       public.orders%rowtype;
  v_item        jsonb;
  v_product     public.products%rowtype;
  v_subtotal    numeric(10, 2) := 0;
  v_shipping    numeric(10, 2);
  v_free_over   numeric(10, 2);
  v_fee         numeric(10, 2);
  v_lines       jsonb := '[]'::jsonb;
  v_confirm     text;
begin
  if p_phone is null or length(regexp_replace(p_phone, '\D', '', 'g')) < 10 then
    raise exception 'invalid phone number';
  end if;

  -- customer upsert (phone is the identity)
  insert into public.customers as c (phone_e164, name, marketing_opt_in)
  values (p_phone, nullif(trim(p_name), ''), false)
  on conflict (phone_e164) do update
    set name = coalesce(nullif(trim(p_name), ''), c.name),
        updated_at = now()
  returning id into v_customer_id;

  -- price every requested line from the catalogue
  for v_item in select * from jsonb_array_elements(p_items)
  loop
    select * into v_product
    from public.products
    where id = (v_item ->> 'product_id')::bigint and is_active;

    if not found then
      raise exception 'product % is unavailable', v_item ->> 'product_id';
    end if;

    if coalesce((v_item ->> 'quantity')::int, 0) < 1 then
      raise exception 'quantity must be at least 1';
    end if;

    v_subtotal := v_subtotal + v_product.price_inr * (v_item ->> 'quantity')::int;

    v_lines := v_lines || jsonb_build_object(
      'product_id', v_product.id,
      'product_name', v_product.name,
      'pack_size_g', v_product.pack_size_g,
      'quantity', (v_item ->> 'quantity')::int,
      'unit_price_inr', v_product.price_inr,
      'line_total_inr', v_product.price_inr * (v_item ->> 'quantity')::int
    );
  end loop;

  if jsonb_array_length(v_lines) = 0 then
    raise exception 'cart is empty';
  end if;

  v_free_over := coalesce(public.get_setting('free_shipping_over_inr', '799')::numeric, 799);
  v_fee := coalesce(public.get_setting('delivery_fee_inr', '60')::numeric, 60);
  v_shipping := case when v_subtotal >= v_free_over then 0 else v_fee end;

  insert into public.orders (
    code, customer_id, phone_e164, customer_name, channel,
    subtotal_inr, shipping_inr, total_inr,
    address_line1, address_line2, city, pincode, landmark, delivery_note
  )
  values (
    'NW-' || to_char(now(), 'YYMMDD') || '-' || lpad(nextval('public.order_code_seq')::text, 4, '0'),
    v_customer_id,
    p_phone,
    coalesce(nullif(trim(p_name), ''), 'NuttyWonders customer'),
    p_channel,
    v_subtotal, v_shipping, v_subtotal + v_shipping,
    nullif(trim(coalesce(p_address ->> 'address_line1', '')), ''),
    nullif(trim(coalesce(p_address ->> 'address_line2', '')), ''),
    nullif(trim(coalesce(p_address ->> 'city', '')), ''),
    nullif(trim(coalesce(p_address ->> 'pincode', '')), ''),
    nullif(trim(coalesce(p_address ->> 'landmark', '')), ''),
    nullif(trim(coalesce(p_delivery_note, '')), '')
  )
  returning * into v_order;

  insert into public.order_items (
    order_id, product_id, product_name, pack_size_g, quantity, unit_price_inr, line_total_inr
  )
  select v_order.id,
         (line ->> 'product_id')::bigint,
         line ->> 'product_name',
         (line ->> 'pack_size_g')::int,
         (line ->> 'quantity')::int,
         (line ->> 'unit_price_inr')::numeric,
         (line ->> 'line_total_inr')::numeric
  from jsonb_array_elements(v_lines) as line;

  -- notify the customer and the shop owner; the bridge drains the queue
  v_confirm := 'Hi ' || coalesce(nullif(trim(p_name), ''), 'there')
    || ', your NuttyWonders order ' || v_order.code || ' is confirmed! Total '
    || v_order.total_inr || ' INR. ';
  if coalesce(public.get_setting('payment_method', 'upi'), 'upi') = 'razorpay' then
    v_confirm := v_confirm
      || 'Please finish the secure online payment on our site. Track: '
      || coalesce(public.get_setting('app_base_url', ''), '')
      || '/order?code=' || v_order.code;
  else
    v_confirm := v_confirm
      || 'Pay by UPI to ' || coalesce(public.get_setting('upi_vpa', ''), 'our UPI id')
      || ' and share the screenshot. Track: '
      || coalesce(public.get_setting('app_base_url', ''), '')
      || '/order?code=' || v_order.code;
  end if;

  insert into public.whatsapp_jobs (to_phone, body, kind, order_id)
  values (v_order.phone_e164, v_confirm, 'order_confirmation', v_order.id);

  if p_reply_to_owner then
    insert into public.whatsapp_jobs (to_phone, body, kind, order_id)
    values (
      coalesce(public.get_setting('owner_phone_e164', ''), ''),
      'New order ' || v_order.code || ' from ' || v_order.customer_name
      || ' (' || v_order.phone_e164 || ') — ' || v_order.total_inr || ' INR',
      'order_alert',
      v_order.id
    );
  end if;

  return jsonb_build_object(
    'order', to_jsonb(v_order),
    'items', v_lines
  );
end;
$$;

-- ---------------------------------------------------------------------------
-- rpc privileges
-- ---------------------------------------------------------------------------
-- The browser never talks to Postgres directly, so none of these functions
-- need to be callable by anon or by signed-in admins: the bridge calls them
-- with the service_role key. RLS is the backstop, but not the only defence.
revoke execute on function public.set_updated_at() from public, anon, authenticated;
revoke execute on function public.get_setting(text, text) from public, anon, authenticated;
revoke execute on function public.claim_whatsapp_jobs(text, integer) from public, anon, authenticated;
revoke execute on function public.place_order(text, text, jsonb, jsonb, text, text, boolean)
  from public, anon, authenticated;

grant execute on function public.get_setting(text, text) to service_role;
grant execute on function public.claim_whatsapp_jobs(text, integer) to service_role;
grant execute on function public.place_order(text, text, jsonb, jsonb, text, text, boolean) to service_role;
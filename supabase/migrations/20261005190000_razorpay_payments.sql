-- ---------------------------------------------------------------------------
-- Razorpay payment capture fields on orders.
-- payment_ref carries the Razorpay order id; payment_transaction_id carries
-- the Razorpay payment id once a payment is captured. Webhooks and the
-- storefront signature-verification endpoint look orders up by payment_ref.
-- ---------------------------------------------------------------------------
alter table public.orders
  add column payment_gateway         text,
  add column payment_ref             text,
  add column payment_transaction_id  text;

create index orders_payment_ref_idx on public.orders (payment_ref)
  where payment_ref is not null;

-- How the shop takes payment (mirrors RAZORPAY_KEY_ID in the bridge env; the
-- setting only drives WhatsApp confirmation copy, never credentials).
insert into public.app_settings (key, value) values
  ('payment_method', '"razorpay"'::jsonb)
on conflict (key) do nothing;
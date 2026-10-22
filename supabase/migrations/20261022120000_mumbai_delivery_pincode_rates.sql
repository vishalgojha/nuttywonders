-- Chembur-based delivery pricing for the Mumbai storefront.
-- The legacy delivery_fee_inr remains the fallback for pincodes that have not
-- yet been configured, which keeps manual/WhatsApp orders backwards-compatible.

create table if not exists public.delivery_pincode_rates (
  pincode text primary key check (pincode ~ '^[0-9]{6}$'),
  area text not null,
  charge_inr numeric(10, 2) not null check (charge_inr >= 0),
  active boolean not null default true,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create or replace function public.delivery_pincode_rates_set_updated_at()
returns trigger
language plpgsql
as $$
begin
  new.updated_at := now();
  return new;
end;
$$;

drop trigger if exists delivery_pincode_rates_updated_at on public.delivery_pincode_rates;
create trigger delivery_pincode_rates_updated_at
before update on public.delivery_pincode_rates
for each row execute function public.delivery_pincode_rates_set_updated_at();

alter table public.delivery_pincode_rates enable row level security;

insert into public.delivery_pincode_rates (pincode, area, charge_inr) values
  -- Local to Chembur
  ('400071', 'Chembur', 40), ('400074', 'Chembur / Govandi', 40),
  ('400043', 'Govandi', 40), ('400088', 'Mankhurd', 40),
  ('400037', 'Wadala', 40), ('400022', 'Sion', 40),
  -- Nearby eastern and central suburbs
  ('400070', 'Kurla', 60), ('400077', 'Ghatkopar', 60),
  ('400075', 'Ghatkopar / Vikhroli', 60), ('400079', 'Vikhroli', 60),
  ('400076', 'Powai', 60), ('400080', 'Mulund', 60),
  ('400086', 'Vikhroli', 60),
  -- Central / western Mumbai
  ('400050', 'Bandra', 90), ('400051', 'Bandra East', 90),
  ('400054', 'Santacruz', 90), ('400055', 'Santacruz East', 90),
  ('400014', 'Dadar', 90), ('400028', 'Dadar', 90),
  ('400053', 'Andheri', 90), ('400058', 'Andheri', 90),
  ('400069', 'Andheri East', 90), ('400093', 'Andheri East', 90),
  ('400005', 'Colaba', 90), ('400001', 'Fort', 90),
  -- Far Mumbai and nearby cities
  ('400092', 'Borivali', 130), ('400101', 'Kandivali', 130),
  ('400066', 'Borivali East', 130), ('400095', 'Malad', 130),
  ('400607', 'Thane', 130), ('400601', 'Thane', 130),
  ('400602', 'Thane', 130), ('400604', 'Thane', 130),
  ('400703', 'Vashi / Navi Mumbai', 130), ('400706', 'Nerul / Navi Mumbai', 130),
  ('400708', 'Navi Mumbai', 130), ('401107', 'Mira Road', 130)
on conflict (pincode) do update set
  area = excluded.area,
  charge_inr = excluded.charge_inr,
  active = true,
  updated_at = now();

comment on table public.delivery_pincode_rates is
  'Chembur-origin delivery prices. Unlisted pincodes use the legacy fallback fee.';

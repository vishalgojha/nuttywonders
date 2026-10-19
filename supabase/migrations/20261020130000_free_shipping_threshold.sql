-- Align the live storefront's free-delivery threshold with the current offer.
update public.app_settings
set value = '"1000"'::jsonb
where key = 'free_shipping_over_inr'
  and value in ('799'::jsonb, '"799"'::jsonb);

insert into public.app_settings (key, value)
values ('free_shipping_over_inr', '"1000"'::jsonb)
on conflict (key) do nothing;

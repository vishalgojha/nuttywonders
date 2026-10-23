-- Default delivery is ₹120 below the free-delivery threshold. Pincode rows
-- remain authoritative for areas with a configured local rate.
insert into public.app_settings (key, value)
values ('delivery_fee_inr', '"120"'::jsonb)
on conflict (key) do update set value = excluded.value, updated_at = now();

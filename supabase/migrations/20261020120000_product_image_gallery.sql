-- Allow each product to have up to five image URLs/paths.
alter table public.products
  add column if not exists image_paths text[] not null default '{}';

alter table public.products
  add constraint products_image_paths_limit
  check (cardinality(image_paths) <= 5);

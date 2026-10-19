create table if not exists public.journal_articles (
  id bigint generated always as identity primary key,
  slug text not null unique,
  title text not null,
  eyebrow text not null default 'Snack notes',
  category text not null default 'Snack notes',
  excerpt text not null default '',
  body text not null default '',
  image_path text,
  is_published boolean not null default false,
  published_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create or replace function public.set_journal_articles_updated_at()
returns trigger language plpgsql as $$
begin
  new.updated_at = now();
  return new;
end;
$$;

drop trigger if exists journal_articles_set_updated_at on public.journal_articles;
create trigger journal_articles_set_updated_at
before update on public.journal_articles
for each row execute function public.set_journal_articles_updated_at();

alter table public.journal_articles enable row level security;
drop policy if exists journal_articles_admin_all on public.journal_articles;
create policy journal_articles_admin_all on public.journal_articles
for all to authenticated
using ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin')
with check ((select auth.jwt() -> 'app_metadata' ->> 'role') = 'admin');

insert into public.journal_articles
  (slug, title, eyebrow, category, excerpt, body, image_path, is_published, published_at)
values
  ('how-to-build-a-happier-pantry', 'How to build a happier pantry', 'Snack notes', 'Snack notes',
   'A few small habits that make everyday snacking feel more considered.',
   'A happier pantry starts with a little intention. Keep a few wholesome favourites where you can see them, make room for different textures, and choose snacks that fit the rhythm of your day.\n\nThe best pantry is not the biggest one. It is the one that makes a good choice easy when breakfast is rushed, chai is brewing, or someone drops by.',
   null, true, now()),
  ('meet-the-humble-mighty-ragi', 'Meet the humble, mighty ragi', 'Ingredient spotlight', 'Ingredient spotlight',
   'Why this familiar grain has earned a place in our small-batch kitchen.',
   'Ragi has long been part of everyday Indian kitchens. We love it for its earthy character and the depth it brings to a recipe without asking for much fuss.\n\nIn our kitchen, it finds its way into comforting bites and bakes made in small batches, alongside dates, nuts and spices chosen for balance.',
   'assets/ragi-brownie.webp', true, now()),
  ('why-small-batch-tastes-better', 'Why small batch tastes better', 'Good to know', 'Good to know',
   'The thoughtful little details behind snacks made close to home.',
   'Small batches let us pay attention. Ingredients are mixed in manageable quantities, textures are checked as we go, and each batch gets the care it deserves.\n\nThat is what we mean by wholesome everyday wonder: snacks made with patience, packed with care, and meant to be enjoyed while they are at their best.',
   'assets/crunchy-munchy.webp', true, now())
on conflict (slug) do nothing;

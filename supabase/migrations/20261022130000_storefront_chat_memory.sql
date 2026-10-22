-- Persistent, anonymous storefront assistant memory.
-- The browser owns a random session UUID; no customer identity is stored here.

create table if not exists public.storefront_chat_messages (
  id bigint generated always as identity primary key,
  session_id uuid not null,
  role text not null check (role in ('user', 'assistant')),
  body text not null check (length(body) > 0),
  created_at timestamptz not null default now()
);

create index if not exists storefront_chat_messages_session_idx
  on public.storefront_chat_messages (session_id, created_at desc);

alter table public.storefront_chat_messages enable row level security;

comment on table public.storefront_chat_messages is
  'Anonymous per-browser storefront assistant context; accessed only by the bridge service role.';

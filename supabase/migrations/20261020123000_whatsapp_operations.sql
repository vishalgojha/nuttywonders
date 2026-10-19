-- WhatsApp operations: human handoff, scheduled broadcasts, and payment review.
alter table public.customers
  add column if not exists support_mode text not null default 'ai'
    check (support_mode in ('ai', 'human')),
  add column if not exists assigned_to text;

alter table public.broadcasts
  add column if not exists scheduled_at timestamptz;

alter table public.whatsapp_messages
  add column if not exists message_type text not null default 'text',
  add column if not exists payment_review_status text,
  add column if not exists reviewed_at timestamptz,
  add column if not exists review_note text;

alter table public.whatsapp_messages
  add constraint whatsapp_messages_payment_review_status_ck
  check (payment_review_status is null or payment_review_status in ('pending', 'approved', 'rejected'));

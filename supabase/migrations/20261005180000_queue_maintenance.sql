-- Background maintenance for the WhatsApp queue.
-- The bridge calls these from its periodic retention sweep; keeping them in
-- Postgres means several bridge replicas cannot double-delete.

-- Cancels queued jobs whose order was cancelled or already delivered.
create or replace function public.cleanup_expired_jobs()
returns integer
language plpgsql
as $$
declare
  v_removed integer;
begin
  with removed as (
    delete from public.whatsapp_jobs j
    using public.orders o
    where j.order_id = o.id
      and j.status = 'queued'
      and o.status in ('cancelled', 'delivered')
    returning 1
  )
  select count(*)::int into v_removed from removed;

  return v_removed;
end;
$$;

-- Releases jobs that were claimed but never finished, for example because the
-- bridge was restarted mid-send.
create or replace function public.release_stale_jobs(p_older_than interval default interval '10 minutes')
returns integer
language plpgsql
as $$
declare
  v_released integer;
begin
  with released as (
    update public.whatsapp_jobs
       set status = 'queued', claimed_by = null, claimed_at = null
     where status = 'sending'
       and claimed_at is not null
       and claimed_at < now() - p_older_than
    returning 1
  )
  select count(*)::int into v_released from released;

  return v_released;
end;
$$;

-- Keeps the message log bounded while preserving the recent conversation
-- history the owner actually needs to read.
create or replace function public.prune_whatsapp_messages(p_keep_days integer default 120)
returns integer
language plpgsql
as $$
declare
  v_deleted integer;
begin
  with deleted as (
    delete from public.whatsapp_messages
    where created_at < now() - make_interval(days => greatest(1, coalesce(p_keep_days, 120)))
    returning 1
  )
  select count(*)::int into v_deleted from deleted;

  return v_deleted;
end;
$$;

-- Postgres grants EXECUTE to PUBLIC by default, which would leave these
-- reachable through the Data API with the anon key. Only the bridge's
-- service_role key may call them.
revoke execute on function public.cleanup_expired_jobs() from public, anon, authenticated;
revoke execute on function public.release_stale_jobs(interval) from public, anon, authenticated;
revoke execute on function public.prune_whatsapp_messages(integer) from public, anon, authenticated;

grant execute on function public.cleanup_expired_jobs() to service_role;
grant execute on function public.release_stale_jobs(interval) to service_role;
grant execute on function public.prune_whatsapp_messages(integer) to service_role;

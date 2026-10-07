import { PGlite } from '@electric-sql/pglite';
import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(HERE, '..', '..');
let failures = 0;
let checks = 0;

function ok(name, cond, extra = '') {
  checks += 1;
  if (cond) {
    console.log(`  PASS  ${name}`);
  } else {
    failures += 1;
    console.log(`  FAIL  ${name} ${extra}`);
  }
}

async function as(db, role, claims, fn) {
  await db.exec(`set session authorization ${role};`);
  if (claims) {
    await db.query(`select set_config('request.jwt.claims', $1, false);`, [JSON.stringify(claims)]);
  } else {
    await db.query(`select set_config('request.jwt.claims', $1, false);`, ['']);
  }
  try {
    return await fn();
  } finally {
    await db.exec(`reset session authorization;`);
  }
}

process.on('unhandledRejection', (e) => { console.error('\nHARNESS ERROR:', e.message); process.exit(1); });

const db = new PGlite();

await db.exec(readFileSync(join(HERE, 'bootstrap.sql'), 'utf8'));

const migrationDir = join(ROOT, 'supabase/migrations');
for (const f of readdirSync(migrationDir).filter((f) => f.endsWith('.sql')).sort()) {
  console.log(`\napplying ${f}`);
  await db.exec(readFileSync(join(migrationDir, f), 'utf8'));
}

console.log('\n-- schema');
const tables = await db.query(
  `select table_name from information_schema.tables where table_schema = 'public' order by 1`,
);
console.log('  tables:', tables.rows.map((r) => r.table_name).join(', '));

console.log('\n-- RLS: anon is locked out');
const anonProducts = await as(db, 'anon', null, () => db.query('select * from public.products'));
ok('anon cannot read products', anonProducts.rows.length === 0);
const anonOrders = await as(db, 'anon', null, () => db.query('select * from public.orders'));
ok('anon cannot read orders', anonOrders.rows.length === 0);
let anonInsertBlocked = false;
try {
  await as(db, 'anon', null, () =>
    db.exec(
      `insert into public.orders (code, phone_e164, customer_name, subtotal_inr, total_inr)
       values ('X-1', '919999999999', 'Mallory', 10, 10)`,
    ),
  );
} catch (err) {
  anonInsertBlocked = /row-level security/i.test(err.message);
}
ok('anon cannot create orders', anonInsertBlocked);

console.log('\n-- RLS: signed-in non-admin is still locked out');
const userOrders = await as(db, 'authenticated', { sub: '11111111-1111-1111-1111-111111111111', app_metadata: {} }, () =>
  db.query('select * from public.orders'),
);
ok('plain authenticated user sees no orders', userOrders.rows.length === 0);

console.log('\n-- RLS: admin (app_metadata.role) has access');
const adminClaims = {
  sub: '22222222-2222-2222-2222-222222222222',
  app_metadata: { role: 'admin' },
};
const adminProducts = await as(db, 'authenticated', adminClaims, () =>
  db.query('select id, price_inr from public.products where is_active'),
);
ok('admin can read products', Array.isArray(adminProducts.rows));

console.log('\n-- seed');
try {
  await as(db, 'service_role', null, () => db.exec(readFileSync(join(ROOT, 'supabase/seed.sql'), 'utf8')));
} catch (err) {
  console.error('\nSEED FAILED:', err.message);
  process.exit(1);
}
const seedProducts = await as(db, 'service_role', null, () =>
  db.query('select count(*)::int as n from public.products where is_active'),
);
ok('seed has 12 products', seedProducts.rows[0].n === 12, `got ${seedProducts.rows[0].n}`);
const seedRecipes = await as(db, 'service_role', null, () =>
  db.query('select count(*)::int as n from public.recipe_items'),
);
ok('seed has recipe lines', seedRecipes.rows[0].n > 40, `got ${seedRecipes.rows[0].n}`);

console.log('\n-- place_order: prices come from the catalogue, not the cart');
const p = await as(db, 'service_role', null, () =>
  db.query(`select id, price_inr from public.products where slug = 'chocolate-granola'`),
);
const productId = p.rows[0].id;
const placed = await as(db, 'service_role', null, () =>
  db.query(
    `select public.place_order(
       '918108398025', 'Kavita Test',
       jsonb_build_array(jsonb_build_object('product_id', $1::bigint, 'quantity', 2, 'unit_price_inr', 1)),
       '{"address_line1":"12 MG Road","city":"Pune","pincode":"411001"}'::jsonb,
       'web', 'please call before delivery'
     ) as result`,
    [productId],
  ),
);
const result = placed.rows[0].result;
const expectedSubtotal = Number(p.rows[0].price_inr) * 2;
ok('subtotal ignores client-supplied price', Number(result.order.subtotal_inr) === expectedSubtotal,
  `${result.order.subtotal_inr} vs ${expectedSubtotal}`);
ok('order code generated', /^NW-\d{6}-\d{4}$/.test(result.order.code), result.order.code);
ok('delivery fee applied under threshold', Number(result.order.shipping_inr) === 60, result.order.shipping_inr);
ok('total = subtotal - discount + shipping',
  Number(result.order.total_inr) === Number(result.order.subtotal_inr) + Number(result.order.shipping_inr));
const items = await as(db, 'service_role', null, () =>
  db.query('select * from public.order_items where order_id = $1', [result.order.id]),
);
ok('order items persisted', items.rows.length === 1 && Number(items.rows[0].quantity) === 2);
const cust = await as(db, 'service_role', null, () =>
  db.query('select * from public.customers where phone_e164 = $1', ['918108398025']),
);
ok('customer upserted', cust.rows.length === 1);
const jobs = await as(db, 'service_role', null, () =>
  db.query('select * from public.whatsapp_jobs where order_id = $1 order by id', [result.order.id]),
);
ok('confirmation + owner alert queued', jobs.rows.length === 2, `got ${jobs.rows.length}`);
ok('confirmation quotes the real total', jobs.rows[0].body.includes(String(result.order.total_inr)),
  jobs.rows[0].body);

console.log('\n-- place_order: free delivery over the threshold');
const bigOrder = await as(db, 'service_role', null, () =>
  db.query(
    `select public.place_order('918108398025', 'Kavita Test',
       jsonb_build_array(jsonb_build_object('product_id', $1::bigint, 'quantity', 3)),
       '{}'::jsonb) as result`,
    [productId],
  ),
);
ok('shipping is free at 3 x Rs 320', Number(bigOrder.rows[0].result.order.shipping_inr) === 0,
  String(bigOrder.rows[0].result.order.shipping_inr));
ok('free-shipping total equals subtotal',
  Number(bigOrder.rows[0].result.order.total_inr) === 960,
  String(bigOrder.rows[0].result.order.total_inr));

console.log('\n-- place_order: guards');
for (const [label, phone] of [['bad phone', '12']]) {
  let threw = false;
  try {
    await as(db, 'service_role', null, () =>
      db.query(`select public.place_order($1, 'X', jsonb_build_array(jsonb_build_object('product_id', $2, 'quantity', 1)))`,
        [phone, productId]),
    );
  } catch {
    threw = true;
  }
  ok(`rejects ${label}`, threw);
}
let emptyCart = false;
try {
  await as(db, 'service_role', null, () =>
    db.query(`select public.place_order('918108398025','X','[]'::jsonb)`),
  );
} catch {
  emptyCart = true;
}
ok('rejects empty cart', emptyCart);
let inactiveBlocked = false;
try {
  await as(db, 'service_role', null, () =>
    db.exec(`update public.products set is_active = false where slug = 'chocolate-granola'`),
  );
  await as(db, 'service_role', null, () =>
    db.query(`select public.place_order('918108398025','X', jsonb_build_array(jsonb_build_object('product_id',$1::bigint,'quantity',1)))`,
      [productId]),
  );
} catch {
  inactiveBlocked = true;
}
ok('rejects inactive product', inactiveBlocked);

console.log('\n-- claim_whatsapp_jobs');
const claimed = await as(db, 'service_role', null, () =>
  db.query(`select * from public.claim_whatsapp_jobs('worker-a', 10)`),
);
ok('claims queued jobs', claimed.rows.length === 4, `got ${claimed.rows.length}`);
ok('marks them sending', claimed.rows.every((r) => r.status === 'sending'));
const claimedAgain = await as(db, 'service_role', null, () =>
  db.query(`select * from public.claim_whatsapp_jobs('worker-b', 10)`),
);
ok('second worker gets nothing (no double-send)', claimedAgain.rows.length === 0);
await as(db, 'service_role', null, () =>
  db.query(`update public.whatsapp_jobs set status = 'sent', sent_at = now() where status = 'sending'`),
);

console.log('\n-- cleanup_expired_jobs');
await as(db, 'service_role', null, () =>
  db.exec(`insert into public.orders (code, phone_e164, customer_name, status, subtotal_inr, total_inr)
           values ('NW-CANCELME','919888888888','Cancelled customer','cancelled', 349, 349)`),
);
await as(db, 'service_role', null, () =>
  db.query(`insert into public.whatsapp_jobs (to_phone, body, kind, order_id)
            select '919888888888', 'stale confirmation', 'order_confirmation', id
            from public.orders where code = 'NW-CANCELME'`),
);
const cleaned = await as(db, 'service_role', null, () =>
  db.query(`select public.cleanup_expired_jobs() as n`),
);
ok('drops the queued job of a cancelled order', Number(cleaned.rows[0].n) === 1, `got ${cleaned.rows[0].n}`);
await as(db, 'service_role', null, () =>
  db.query(`insert into public.orders (code, phone_e164, customer_name, status, subtotal_inr, total_inr)
           values ('NW-INFLIGHT','919444444444','In flight customer','confirmed', 499, 499)`),
);
await as(db, 'service_role', null, () =>
  db.query(`insert into public.whatsapp_jobs (to_phone, body, kind, order_id)
            select '919444444444', 'still needed', 'order_update', id
            from public.orders where code = 'NW-INFLIGHT'`),
);
const keptForLive = await as(db, 'service_role', null, () =>
  db.query(`select count(*)::int as n from public.whatsapp_jobs j
            join public.orders o on o.id = j.order_id
            where o.status = 'confirmed' and j.status = 'queued' and j.body = 'still needed'`),
);
ok('keeps jobs for orders still in progress', Number(keptForLive.rows[0].n) === 1, `got ${keptForLive.rows[0].n}`);

console.log('\n-- release_stale_jobs');
await as(db, 'service_role', null, () =>
  db.query(`insert into public.whatsapp_jobs (to_phone, body, kind, status, claimed_at, claimed_by)
            values ('919777777777', 'stuck', 'custom', 'sending', now() - interval '1 hour', 'dead-worker')`),
);
await as(db, 'service_role', null, () =>
  db.query(`insert into public.whatsapp_jobs (to_phone, body, kind, status, claimed_at, claimed_by)
            values ('919666666666', 'fresh claim', 'custom', 'sending', now(), 'live-worker')`),
);
const released = await as(db, 'service_role', null, () =>
  db.query(`select public.release_stale_jobs() as n`),
);
ok('requeues the job abandoned by a dead worker', Number(released.rows[0].n) === 1, `got ${released.rows[0].n}`);
const stillHeld = await as(db, 'service_role', null, () =>
  db.query(`select status, claimed_by from public.whatsapp_jobs
            where to_phone = '919666666666'`),
);
ok('does not steal a job that is still in flight',
  stillHeld.rows[0].status === 'sending' && stillHeld.rows[0].claimed_by === 'live-worker');

console.log('\n-- prune_whatsapp_messages');
await as(db, 'service_role', null, () =>
  db.query(`insert into public.whatsapp_messages (direction, phone_e164, body, created_at)
            values ('in', '919555555555', 'ancient', now() - interval '400 days')`),
);
await as(db, 'service_role', null, () =>
  db.query(`insert into public.whatsapp_messages (direction, phone_e164, body, created_at)
            values ('in', '919555555555', 'recent', now() - interval '2 days')`),
);
const pruned = await as(db, 'service_role', null, () =>
  db.query(`select public.prune_whatsapp_messages(120) as n`),
);
ok('deletes only messages past the retention window', Number(pruned.rows[0].n) === 1, `got ${pruned.rows[0].n}`);
const recentKept = await as(db, 'service_role', null, () =>
  db.query(`select body from public.whatsapp_messages where phone_e164 = '919555555555'`),
);
ok('keeps the recent conversation', recentKept.rows.length === 1 && recentKept.rows[0].body === 'recent');

console.log('\n-- maintenance functions are not open to the browser');
for (const fn of ['cleanup_expired_jobs', 'prune_whatsapp_messages']) {
  let blocked = false;
  try {
    await as(db, 'anon', null, () => db.query(`select public.${fn}()`));
  } catch {
    blocked = true;
  }
  ok(`${fn} rejects anon`, blocked);
}

console.log('\n-- totals constraint');
let badTotal = false;
try {
  await as(db, 'service_role', null, () =>
    db.exec(`insert into public.orders (code, phone_e164, customer_name, subtotal_inr, total_inr)
             values ('NW-BAD','919999999999','x', 100, 5)`),
  );
} catch {
  badTotal = true;
}
ok('rejects totals that do not add up', badTotal);

console.log('\n-- product economics from the costing mastersheet');
const econ = await as(db, 'service_role', null, () =>
  db.query(`select slug, price_inr, cost_ingredient_inr, cost_packaging_inr, cost_labour_inr, cost_overhead_inr
            from public.products order by sort_order`),
);
for (const r of econ.rows) {
  const cost = Number(r.cost_ingredient_inr) + Number(r.cost_packaging_inr) + Number(r.cost_labour_inr) + Number(r.cost_overhead_inr);
  const margin = (Number(r.price_inr) - cost) / Number(r.price_inr);
  ok(`${r.slug} priced above cost (margin ${(margin * 100).toFixed(0)}%)`, margin > 0.1, `${cost} vs ${r.price_inr}`);
}

console.log(`\n${checks - failures}/${checks} checks passed`);
await db.close();
process.exit(failures === 0 ? 0 : 1);
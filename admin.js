const state = {
  products: [],
  orders: [],
  broadcasts: [],
  settings: {},
  dashboard: null,
  wa: null,
  editingOrderLines: [],
};

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

async function api(path, options = {}) {
  const response = await fetch('/api' + path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    ...options,
    body: options.body ? JSON.stringify(options.body) : undefined,
  });

  if (response.status === 401 || response.status === 403) {
    showLogin();
    throw new Error('Your session has ended, please sign in again.');
  }

  let payload = null;
  try {
    payload = await response.json();
  } catch {
    payload = null;
  }
  if (!response.ok) throw new Error(payload?.error || `Request failed (${response.status})`);
  return payload;
}

function money(amount) {
  return '₹' + Number(amount || 0).toLocaleString('en-IN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

function moneyShort(amount) {
  return '₹' + Math.round(Number(amount || 0)).toLocaleString('en-IN');
}

function escapeHtml(value) {
  return String(value ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[c]);
}

function formatWhen(value) {
  if (!value) return '';
  return new Date(value).toLocaleString('en-IN', { day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit' });
}

function toast(message) {
  const el = $('#toast');
  el.textContent = message;
  el.classList.add('show');
  setTimeout(() => el.classList.remove('show'), 2600);
}

// --- session ---------------------------------------------------------------

function showLogin() {
  $('#login-screen').hidden = false;
  $('#studio').hidden = true;
}

function showStudio(email) {
  $('#login-screen').hidden = true;
  $('#studio').hidden = false;
  $('#today').textContent = new Date().toLocaleDateString('en-IN', { weekday: 'long', day: '2-digit', month: 'long' });
  if (email) {
    $('.topbar h1').innerHTML = `Good to see you, ${escapeHtml(email.split('@')[0])} <span>✦</span>`;
  }
}

async function signIn(event) {
  event.preventDefault();
  const button = $('#login-submit');
  const error = $('#login-error');
  error.textContent = '';
  button.disabled = true;
  button.textContent = 'Signing in…';

  try {
    await api('/admin/session', {
      method: 'POST',
      body: { email: $('#login-email').value.trim(), password: $('#login-password').value },
    });
    showStudio($('#login-email').value.trim());
    await refreshAll();
  } catch (err) {
    error.textContent = err.message;
  } finally {
    button.disabled = false;
    button.textContent = 'Sign in';
  }
}

async function signOut() {
  try {
    await api('/admin/session/sign-out', { method: 'POST', body: {} });
  } catch {
    // already gone
  }
  showLogin();
}

// --- dashboard -------------------------------------------------------------

async function refreshAll() {
  try {
    const [dashboard, products, orders, broadcasts, settings, wa] = await Promise.all([
      api('/admin/dashboard'),
      api('/admin/products'),
      api('/admin/orders'),
      api('/admin/broadcasts'),
      api('/admin/settings'),
      api('/admin/whatsapp'),
    ]);
    state.dashboard = dashboard;
    state.products = products.products || [];
    state.orders = orders.orders || [];
    state.broadcasts = broadcasts.broadcasts || [];
    state.settings = settings.settings || {};
    state.wa = wa;

    renderStats();
    renderOrders();
    renderProducts();
    renderBroadcasts();
    renderSettings();
    renderWhatsApp();
    $('#sync-state').innerHTML = '<i></i> Synced';
  } catch (err) {
    $('#sync-state').textContent = err.message;
  }
}

function renderStats() {
  const data = state.dashboard || {};
  const counts = data.counts || {};
  const byStatus = data.orders_by_status || {};

  $('#stat-orders').textContent = counts.orders ?? 0;
  $('#stat-new').textContent = byStatus.new ?? 0;
  $('#stat-products').textContent = counts.products ?? 0;
  $('#stat-pending').textContent = data.pending_payments ?? 0;
  $('#stat-queue').textContent = data.queue_pending ?? 0;
  $('#nav-order-count').textContent = byStatus.new ?? 0;

  const costed = state.products.filter((p) => Number(p.price_inr) > 0);
  const margins = costed.map((p) => (Number(p.price_inr) - unitCost(p)) / Number(p.price_inr) * 100);
  $('#stat-margin').textContent = margins.length
    ? `${Math.round(margins.reduce((a, b) => a + b, 0) / margins.length)}%`
    : '0%';

  if (state.wa?.jid) $('#stat-jid').textContent = state.wa.jid;
}

function unitCost(product) {
  return Number(product.cost_ingredient_inr || 0) + Number(product.cost_packaging_inr || 0)
    + Number(product.cost_labour_inr || 0) + Number(product.cost_overhead_inr || 0);
}

// --- orders ----------------------------------------------------------------

function statusLabel(status) {
  return { new: 'New', confirmed: 'Confirmed', packed: 'Packed', shipped: 'Shipped', delivered: 'Delivered', cancelled: 'Cancelled' }[status] || status;
}

function paymentLabel(status) {
  return { pending: 'Awaiting payment', paid: 'Paid', cod: 'Cash on delivery', failed: 'Failed', refunded: 'Refunded' }[status] || status;
}

function renderOrders() {
  const list = filteredOrders();
  $('#order-list').innerHTML = list.length ? list.map((order) => `
    <div class="order-row" data-id="${order.id}">
      <div class="order-main">
        <strong>${escapeHtml(order.customer_name)}</strong>
        <small>${escapeHtml(order.code)} · ${escapeHtml(order.phone_e164)} · ${formatWhen(order.created_at)}</small>
        <small>${order.items.map((item) => `${escapeHtml(item.product_name)} × ${item.quantity}`).join(', ')}</small>
      </div>
      <div class="order-tags">
        <span class="tag status-${escapeHtml(order.status)}">${escapeHtml(statusLabel(order.status))}</span>
        <span class="tag pay-${escapeHtml(order.payment_status)}">${escapeHtml(paymentLabel(order.payment_status))}</span>
      </div>
      <div class="order-amount">${money(order.total_inr)}</div>
      <div class="order-actions">
        <select data-status="${order.id}">
          ${Object.entries({ new: 'New', confirmed: 'Confirmed', packed: 'Packed', shipped: 'Shipped', delivered: 'Delivered', cancelled: 'Cancelled' })
            .map(([value, label]) => `<option value="${value}" ${order.status === value ? 'selected' : ''}>${label}</option>`).join('')}
        </select>
        <select data-payment="${order.id}">
          ${Object.entries({ pending: 'Awaiting payment', paid: 'Paid', cod: 'Cash on delivery', failed: 'Failed', refunded: 'Refunded' })
            .map(([value, label]) => `<option value="${value}" ${order.payment_status === value ? 'selected' : ''}>${label}</option>`).join('')}
        </select>
        <button data-notify="${order.id}">Notify</button>
        <button data-edit-order="${order.id}">Details</button>
      </div>
    </div>`).join('') : '<div class="empty">No orders match this view yet.</div>';

  $$('[data-status]').forEach((el) => el.addEventListener('change', () => updateOrder(el.dataset.status, { status: el.value })));
  $$('[data-payment]').forEach((el) => el.addEventListener('change', () => updateOrder(el.dataset.payment, { payment_status: el.value })));
  $$('[data-notify]').forEach((el) => el.addEventListener('click', () => {
    const order = state.orders.find((o) => String(o.id) === el.dataset.notify);
    updateOrder(order.id, { notify_customer: true });
  }));
  $$('[data-edit-order]').forEach((el) => el.addEventListener('click', () => openOrderDetails(el.dataset.editOrder)));
}

function filteredOrders() {
  const status = $('#order-status').value;
  const term = $('#order-search').value.trim().toLowerCase();
  return state.orders.filter((order) => {
    if (status !== 'all' && order.status !== status) return false;
    if (!term) return true;
    return [order.code, order.customer_name, order.phone_e164].some((field) => String(field || '').toLowerCase().includes(term));
  });
}

async function updateOrder(id, changes) {
  try {
    await api(`/admin/orders/${id}`, { method: 'PATCH', body: changes });
    toast(changes.notify_customer ? 'Order updated and customer notified' : 'Order updated');
    await refreshAll();
  } catch (err) {
    toast(err.message);
  }
}

function openOrderDetails(id) {
  const order = state.orders.find((o) => String(o.id) === String(id));
  if (!order) return;
  $('#order-form-title').textContent = `${order.code} · ${order.customer_name}`;
  $('#order-id').value = order.id;
  $('#order-customer').value = order.customer_name;
  $('#order-phone').value = order.phone_e164;
  $('#order-address').value = order.address_line1 || '';
  $('#order-city').value = order.city || '';
  $('#order-pincode').value = order.pincode || '';
  $('#order-lines').innerHTML = order.items.map((item) => `
    <div class="order-edit-line"><span>${escapeHtml(item.product_name)} × ${item.quantity}</span><strong>${money(item.line_total_inr)}</strong></div>`).join('')
    + `<p class="order-edit-total">Total ${money(order.total_inr)} · ${escapeHtml(paymentLabel(order.payment_status))}</p>`;
  $('#order-modal').classList.add('open');
}

// --- manual order ----------------------------------------------------------

function openNewOrder() {
  $('#order-form').reset();
  $('#order-form-title').textContent = 'New order';
  $('#order-id').value = '';
  state.editingOrderLines = [];
  renderOrderLines();
  $('#order-modal').classList.add('open');
}

function renderOrderLines() {
  const products = state.products.filter((p) => p.is_active);
  $('#order-lines').innerHTML = state.editingOrderLines.map((line, index) => `
    <div class="order-edit-row">
      <select data-line-product="${index}">
        ${products.map((p) => `<option value="${p.id}" ${String(p.id) === String(line.product_id) ? 'selected' : ''}>${escapeHtml(p.name)} · ${money(p.price_inr)}</option>`).join('')}
      </select>
      <input data-line-qty="${index}" type="number" min="1" value="${line.quantity}" />
      <button type="button" data-remove-line="${index}">×</button>
    </div>`).join('');

  $$('[data-line-product]').forEach((el) => el.addEventListener('change', () => {
    state.editingOrderLines[Number(el.dataset.lineProduct)].product_id = Number(el.value);
  }));
  $$('[data-line-qty]').forEach((el) => el.addEventListener('change', () => {
    state.editingOrderLines[Number(el.dataset.lineQty)].quantity = Math.max(1, Number(el.value) || 1);
  }));
  $$('[data-remove-line]').forEach((el) => el.addEventListener('click', () => {
    state.editingOrderLines.splice(Number(el.dataset.removeLine), 1);
    renderOrderLines();
  }));
}

async function submitOrder(event) {
  event.preventDefault();
  const items = state.editingOrderLines.filter((line) => line.product_id && line.quantity > 0);
  if (!items.length) {
    toast('Add at least one item');
    return;
  }

  try {
    await api('/admin/orders', {
      method: 'POST',
      body: {
        name: $('#order-customer').value.trim(),
        phone: $('#order-phone').value.trim(),
        address: {
          address_line1: $('#order-address').value.trim(),
          city: $('#order-city').value.trim(),
          pincode: $('#order-pincode').value.trim(),
        },
        items,
      },
    });
    $('#order-modal').classList.remove('open');
    toast('Order created');
    await refreshAll();
  } catch (err) {
    toast(err.message);
  }
}

// --- products --------------------------------------------------------------

function renderProducts() {
  const term = $('#search').value.toLowerCase();
  const category = $('#category-filter').value;
  const list = state.products.filter((product) => {
    if (category !== 'all' && product.category !== category) return false;
    if (!term) return true;
    return product.name.toLowerCase().includes(term) || String(product.slug || '').includes(term);
  });

  $('#product-list').innerHTML = list.length ? list.map((product) => {
    const cost = unitCost(product);
    const margin = Number(product.price_inr) > 0 ? Math.round((Number(product.price_inr) - cost) / Number(product.price_inr) * 100) : 0;
    return `
    <div class="product-row" data-slug="${escapeHtml(product.slug)}">
      <div class="thumb">${product.image_path ? `<img src="${escapeHtml(product.image_path)}" alt="">` : '<div class="shape"></div>'}</div>
      <div class="row-name">
        <strong>${escapeHtml(product.name)} ${product.is_active ? '' : '<span class="tag">Hidden</span>'}</strong>
        <small>${product.pack_size_g} g · ${escapeHtml(product.category)} · ${product.stock_packs} in stock</small>
      </div>
      <div class="row-price">${money(product.price_inr)}<small> / pack</small></div>
      <div class="row-meta"><b>${margin}%</b><small>margin · ${money(cost)} cost</small></div>
      <div class="row-actions"><button data-edit="${escapeHtml(product.slug)}">Edit</button></div>
    </div>`;
  }).join('') : '<div class="empty">No products match your search.</div>';

  $$('[data-edit]').forEach((el) => el.addEventListener('click', () => openProductForm(el.dataset.edit)));
}

function openProductForm(slug) {
  const product = state.products.find((p) => p.slug === slug);
  $('#product-modal').classList.add('open');
  $('#form-title').textContent = product ? `Edit ${product.name}` : 'Add a product';
  $('#product-id').value = product?.slug || '';
  $('#name').value = product?.name || '';
  $('#tagline').value = product?.tagline || '';
  $('#category').value = product?.category || 'granola';
  $('#pack').value = product?.pack_size_g || 200;
  $('#price').value = product?.price_inr ?? '';
  $('#stock').value = product?.stock_packs ?? 0;
  $('#description').value = product?.description || '';
  $('#ingredients').value = (product?.ingredients || []).join(', ');
  $('#allergens').value = (product?.allergens || []).join(', ');
  $('#ingredient-cost').value = product?.cost_ingredient_inr ?? 0;
  $('#packaging-cost').value = product?.cost_packaging_inr ?? 0;
  $('#labour-cost').value = product?.cost_labour_inr ?? 0;
  $('#overhead-cost').value = product?.cost_overhead_inr ?? 0;
  $('#active').checked = product ? product.is_active : true;
  updateCostPreview();
}

function splitList(value) {
  return value.split(',').map((item) => item.trim()).filter(Boolean);
}

async function submitProduct(event) {
  event.preventDefault();
  const slug = $('#product-id').value;
  const body = {
    name: $('#name').value.trim(),
    tagline: $('#tagline').value.trim(),
    slug,
    category: $('#category').value,
    pack_size_g: Number($('#pack').value) || 200,
    price_inr: Number($('#price').value) || 0,
    stock_packs: Number($('#stock').value) || 0,
    description: $('#description').value.trim(),
    ingredients: splitList($('#ingredients').value),
    allergens: splitList($('#allergens').value),
    cost_ingredient_inr: Number($('#ingredient-cost').value) || 0,
    cost_packaging_inr: Number($('#packaging-cost').value) || 0,
    cost_labour_inr: Number($('#labour-cost').value) || 0,
    cost_overhead_inr: Number($('#overhead-cost').value) || 0,
    is_active: $('#active').checked,
  };

  try {
    await api('/admin/products', { method: 'POST', body });
    $('#product-modal').classList.remove('open');
    toast('Product saved to your storefront');
    await refreshAll();
  } catch (err) {
    toast(err.message);
  }
}

function updateCostPreview() {
  const cost = Number($('#ingredient-cost').value || 0) + Number($('#packaging-cost').value || 0)
    + Number($('#labour-cost').value || 0) + Number($('#overhead-cost').value || 0);
  const price = Number($('#price').value || 0);
  $('#unit-cost').textContent = money(cost);
  $('#unit-margin').textContent = price > 0 ? `${Math.round((price - cost) / price * 100)}%` : '0%';
  $('#unit-price-margin').textContent = price > 0 ? `${Math.round((price - cost) / price * 100)}%` : '0%';
}

// --- messages --------------------------------------------------------------

async function loadMessages() {
  const phone = $('#message-phone').value.trim();
  const query = phone ? `?phone=${encodeURIComponent(phone)}` : '';
  try {
    const result = await api('/admin/conversations' + query);
    const messages = result.messages || [];
    $('#message-list').innerHTML = messages.length ? messages.map((message) => `
      <div class="message-row ${escapeHtml(message.direction)}">
        <div>
          <strong>${escapeHtml(message.phone_e164)}</strong>
          <small>${formatWhen(message.created_at)} · ${escapeHtml(message.status)}</small>
        </div>
        <p>${escapeHtml(message.body || '')}</p>
      </div>`).join('') : '<div class="empty">No messages yet. Once your number is linked, replies land here.</div>';
    if (phone) $('#reply-phone').value = phone;
  } catch (err) {
    toast(err.message);
  }
}

async function sendReply(event) {
  event.preventDefault();
  try {
    await api('/admin/whatsapp/send', {
      method: 'POST',
      body: { phone: $('#reply-phone').value.trim(), body: $('#reply-body').value.trim() },
    });
    $('#reply-body').value = '';
    toast('Message sent');
    loadMessages();
  } catch (err) {
    toast(err.message);
  }
}

// --- broadcast -------------------------------------------------------------

function renderBroadcasts() {
  $('#broadcast-list').innerHTML = state.broadcasts.length ? state.broadcasts.map((broadcast) => `
    <div class="broadcast-row">
      <div><strong>${escapeHtml(broadcast.title)}</strong><small>${escapeHtml(broadcast.segment)} · ${broadcast.sent_count}/${broadcast.audience_count} sent · ${formatWhen(broadcast.created_at)}</small></div>
      <span class="tag status-${escapeHtml(broadcast.status)}">${escapeHtml(broadcast.status)}</span>
    </div>`).join('') : '<div class="empty">No broadcasts yet.</div>';
}

async function sendBroadcast(event, draft) {
  event?.preventDefault();
  try {
    const result = await api('/admin/broadcasts', {
      method: 'POST',
      body: {
        title: $('#broadcast-title').value.trim(),
        body: $('#broadcast-body').value.trim(),
        segment: $('#broadcast-segment').value,
        send_now: !draft,
      },
    });
    toast(draft ? 'Draft saved' : `Queued for ${result.broadcast.audience_count} customer(s)`);
    await refreshAll();
  } catch (err) {
    toast(err.message);
  }
}

// --- settings --------------------------------------------------------------

const settingFields = [
  ['upi_vpa', 'UPI id', 'e.g. nuttywonders@okaxis'],
  ['upi_payee_name', 'UPI payee name', 'What the customer sees in their UPI app'],
  ['owner_phone_e164', 'Shop WhatsApp number', 'Where order alerts go'],
  ['support_email', 'Support email', 'Shown to customers'],
  ['app_base_url', 'Storefront URL', 'Used in order links'],
  ['delivery_fee_inr', 'Delivery fee (₹)', '60'],
  ['free_shipping_over_inr', 'Free delivery above (₹)', '799'],
  ['payment_instructions', 'Payment instructions', 'One or two lines'],
  ['business_hours', 'Business hours', 'e.g. Mon to Sat, 10am to 7pm'],
  ['announcement', 'Announcement', 'Shown when customers ask about offers'],
  ['instagram_url', 'Instagram URL', ''],
];

function renderSettings() {
  $('#settings-grid').innerHTML = settingFields.map(([key, label, hint]) => `
    <label class="setting">
      <span>${escapeHtml(label)}${hint ? `<small>${escapeHtml(hint)}</small>` : ''}</span>
      <input data-setting="${key}" value="${escapeHtml(state.settings[key] ?? '')}" />
    </label>`).join('');

  $$('[data-setting]').forEach((input) => input.addEventListener('change', async () => {
    try {
      await api(`/admin/settings/${input.dataset.setting}`, { method: 'PUT', body: { value: input.value.trim() } });
      toast('Setting saved');
    } catch (err) {
      toast(err.message);
    }
  }));
}

// --- whatsapp --------------------------------------------------------------

function renderWhatsApp() {
  const wa = state.wa || {};
  const connected = wa.status === 'connected';

  $('#wa-dot').style.background = connected ? '#6f9b62' : '#d7824b';
  $('#wa-label').textContent = connected ? 'Connected' : wa.status === 'pairing' ? 'Pairing' : 'Offline';
  $('#wa-heading').textContent = connected ? `Linked as ${wa.jid || ''}` : 'Link your number';
  $('#wa-detail').textContent = wa.last_error || (connected
    ? 'Order confirmations and broadcasts go out from this number.'
    : 'Scan the QR code from WhatsApp → Linked devices to link this shop.');

  const qrBox = $('#qr-box');
  if (wa.qr_code && !connected) {
    qrBox.hidden = false;
    qrBox.innerHTML = pairingQr(wa.qr_code) +
      '<small>Expires in about 20 seconds — it refreshes on its own.</small>';
  } else {
    qrBox.hidden = true;
  }
}

/**
 * Draw the pairing code locally.
 *
 * That string is a live credential for the next twenty seconds or so, so it
 * never goes near a remote image service. If the renderer is missing we say so
 * rather than quietly shipping the code somewhere else.
 */
function pairingQr(text) {
  if (!window.nwQR) {
    return '<p class="qr-error">The QR renderer did not load. Reload the page.</p>';
  }
  try {
    const qr = window.nwQR.toSvgPath(text, { quiet: 2, scale: 5 });
    return '<span class="qr-frame">' + qr.svg + '</span>';
  } catch (err) {
    return '<p class="qr-error">Could not draw the pairing code. Reload the page.</p>';
  }
}

async function pollWhatsApp() {
  if ($('#studio').hidden) return;
  try {
    state.wa = await api('/admin/whatsapp');
    renderWhatsApp();
  } catch {
    // signed out; refreshAll will handle it
  }
}

// --- wiring ----------------------------------------------------------------

function wire() {
  $('#login-form').addEventListener('submit', signIn);
  $('#sign-out').addEventListener('click', signOut);

  $('#search').addEventListener('input', renderProducts);
  $('#category-filter').addEventListener('change', renderProducts);
  $('#new-product').addEventListener('click', () => openProductForm());
  $('#close-modal').addEventListener('click', () => $('#product-modal').classList.remove('open'));
  $('#cancel-form').addEventListener('click', () => $('#product-modal').classList.remove('open'));
  $('#product-form').addEventListener('submit', submitProduct);
  ['#ingredient-cost', '#packaging-cost', '#labour-cost', '#overhead-cost', '#price']
    .forEach((sel) => $(sel).addEventListener('input', updateCostPreview));

  $('#order-status').addEventListener('change', renderOrders);
  $('#order-search').addEventListener('input', renderOrders);
  $('#new-order').addEventListener('click', openNewOrder);
  $('#close-order-modal').addEventListener('click', () => $('#order-modal').classList.remove('open'));
  $('#cancel-order-form').addEventListener('click', () => $('#order-modal').classList.remove('open'));
  $('#order-form').addEventListener('submit', submitOrder);
  $('#add-order-line').addEventListener('click', () => {
    const first = state.products.find((p) => p.is_active);
    if (!first) {
      toast('Add a product first');
      return;
    }
    state.editingOrderLines.push({ product_id: first.id, quantity: 1 });
    renderOrderLines();
  });

  $('#refresh-messages').addEventListener('click', loadMessages);
  $('#quick-reply').addEventListener('submit', sendReply);

  $('#broadcast-form').addEventListener('submit', (event) => sendBroadcast(event, false));
  $('#broadcast-draft').addEventListener('click', (event) => sendBroadcast(event, true));

  $('#pair-code').addEventListener('click', async () => {
    try {
      const result = await api('/admin/whatsapp/pair-code', { method: 'POST', body: {} });
      toast(`Pairing code: ${result.code}`);
    } catch (err) {
      toast(err.message);
    }
  });

  $('#wa-logout').addEventListener('click', async () => {
    if (!confirm('Unlink this number? You will need to scan a new QR code.')) return;
    try {
      await api('/admin/whatsapp/logout', { method: 'POST', body: {} });
      toast('Number unlinked');
      pollWhatsApp();
    } catch (err) {
      toast(err.message);
    }
  });

  document.querySelectorAll('.sidebar nav a').forEach((link) => link.addEventListener('click', () => {
    document.querySelectorAll('.sidebar nav a').forEach((other) => other.classList.remove('active'));
    link.classList.add('active');
  }));
}

async function init() {
  wire();
  try {
    await api('/admin/me');
    showStudio();
    await refreshAll();
    loadMessages();
    setInterval(refreshAll, 60000);
    setInterval(pollWhatsApp, 4000);
  } catch {
    showLogin();
  }
}

init();
if('serviceWorker' in navigator)window.addEventListener('load',()=>navigator.serviceWorker.register('/sw.js'));

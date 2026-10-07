const API = '/api';

async function api(path, options = {}) {
  const response = await fetch(API + path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    ...options,
    body: options.body ? JSON.stringify(options.body) : undefined,
  });

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

function packLabel(grams) {
  const g = Number(grams || 0);
  if (g >= 1000 && g % 1000 === 0) return `${g / 1000} kg`;
  return `${g} g`;
}

const state = {
  products: [],
  cart: JSON.parse(localStorage.getItem('nutty-cart') || '[]'),
  settings: {},
  current: null,
  filter: 'all',
};

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

const productById = (id) => state.products.find((p) => String(p.id) === String(id));

function visualFor(product) {
  if (product.image_path) {
    return `<div class="product-image photo"><img src="${product.image_path}" alt="${escapeHtml(product.name)}" loading="lazy"></div>`;
  }
  return `<div class="product-image ${product.category}"><div class="shape"></div></div>`;
}

function escapeHtml(value) {
  return String(value ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[c]);
}

function renderProducts() {
  const list = state.filter === 'all'
    ? state.products
    : state.products.filter((p) => p.category === state.filter);

  $('#product-count').textContent = `${list.length} snack${list.length === 1 ? '' : 's'}`;
  $('#product-grid').innerHTML = list.map((product) => `
    <article class="product-card" data-id="${product.id}">
      ${visualFor(product)}
      <div class="product-info">
        <div>
          <h3>${escapeHtml(product.name)}</h3>
          <p>${packLabel(product.pack_size_g)} · ${escapeHtml(product.category)}</p>
        </div>
        <span class="product-price">${money(product.price_inr)}</span>
      </div>
    </article>`).join('');

  $$('.product-card').forEach((card) => card.addEventListener('click', () => {
    openModal(productById(card.dataset.id));
  }));
}

function openModal(product) {
  if (!product) return;
  state.current = product;

  $('#modal-category').textContent = product.tagline || product.category;
  $('#modal-title').textContent = product.name;
  $('#modal-description').textContent = product.description || '';
  $('#modal-visual').innerHTML = visualFor(product);
  $('#modal-price').textContent = money(product.price_inr);
  $('#modal-pack').textContent = `${packLabel(product.pack_size_g)} pack`;

  const ingredients = $('#modal-ingredients');
  ingredients.innerHTML = '';
  (product.ingredients || []).forEach((item) => {
    const chip = document.createElement('span');
    chip.className = 'ingredient';
    chip.textContent = item;
    ingredients.appendChild(chip);
  });
  if ((product.allergens || []).length) {
    const note = document.createElement('small');
    note.className = 'allergen-note';
    note.textContent = `Contains: ${product.allergens.join(', ')}`;
    ingredients.appendChild(note);
  }

  $('#modal-backdrop').classList.add('open');
}

function addToCart(product) {
  if (!product) return;
  const line = state.cart.find((item) => String(item.id) === String(product.id));
  if (line) line.qty += 1;
  else state.cart.push({ id: product.id, qty: 1 });
  saveCart();
  renderCart();
  toast(`${product.name} added to your bag`);
}

function saveCart() {
  localStorage.setItem('nutty-cart', JSON.stringify(state.cart));
  $('#cart-count').textContent = state.cart.reduce((sum, item) => sum + item.qty, 0);
}

function cartTotals() {
  const subtotal = state.cart.reduce((sum, item) => {
    const product = productById(item.id);
    return sum + (product ? Number(product.price_inr) * item.qty : 0);
  }, 0);

  const freeOver = Number(state.settings.free_shipping_over_inr || 799);
  const fee = Number(state.settings.delivery_fee_inr || 60);
  const shipping = subtotal >= freeOver ? 0 : fee;

  return { subtotal, shipping, total: subtotal + shipping };
}

function renderCart() {
  const area = $('#cart-items');

  if (!state.cart.length) {
    area.innerHTML = `<p class="cart-empty">Your bag is waiting for something delicious.<br><br>Explore the pantry and add your favourites here.</p>`;
    $('#cart-total').textContent = '₹0';
    $('#cart-shipping').textContent = '₹0.00';
    return;
  }

  area.innerHTML = state.cart.map((item) => {
    const product = productById(item.id);
    if (!product) return '';
    return `
      <div class="cart-line">
        ${visualFor(product)}
        <div class="cart-line-info">
          <strong>${escapeHtml(product.name)}</strong>
          <small>${packLabel(product.pack_size_g)} pack · ${money(product.price_inr)}</small>
          <div class="cart-line-actions">
            <button class="qty-btn" data-action="minus" data-id="${product.id}">−</button>
            <span>${item.qty}</span>
            <button class="qty-btn" data-action="plus" data-id="${product.id}">+</button>
            <button class="remove" data-action="remove" data-id="${product.id}">Remove</button>
          </div>
        </div>
        <span class="cart-line-price">${money(product.price_inr * item.qty)}</span>
      </div>`;
  }).join('');

  const totals = cartTotals();
  $('#cart-total').textContent = money(totals.total);
  $('#cart-shipping').textContent = totals.shipping === 0 ? 'Free' : money(totals.shipping);
  $('#cart-subtotal').textContent = money(totals.subtotal);

  $$('.qty-btn,.remove', area).forEach((button) => button.addEventListener('click', () => {
    const line = state.cart.find((item) => String(item.id) === String(button.dataset.id));
    if (!line) return;
    if (button.dataset.action === 'plus') line.qty += 1;
    if (button.dataset.action === 'minus') line.qty -= 1;
    if (button.dataset.action === 'remove' || line.qty <= 0) {
      state.cart = state.cart.filter((item) => String(item.id) !== String(button.dataset.id));
    }
    saveCart();
    renderCart();
  }));
}

function toast(message) {
  const el = $('#toast');
  el.textContent = message;
  el.classList.add('show');
  setTimeout(() => el.classList.remove('show'), 2600);
}

function cartPayload() {
  return state.cart.map((item) => ({ product_id: Number(item.id), quantity: item.qty }));
}

async function submitCheckout(event) {
  event.preventDefault();
  const button = $('#checkout-submit');
  const feedback = $('#checkout-feedback');

  const payload = {
    name: $('#checkout-name').value.trim(),
    phone: $('#checkout-phone').value.trim(),
    email: $('#checkout-email').value.trim(),
    address: {
      address_line1: $('#checkout-address').value.trim(),
      address_line2: $('#checkout-address2').value.trim(),
      city: $('#checkout-city').value.trim(),
      pincode: $('#checkout-pincode').value.trim(),
      landmark: $('#checkout-landmark').value.trim(),
    },
    note: $('#checkout-note').value.trim(),
    items: cartPayload(),
    channel: 'web',
  };

  if (!payload.name) return showCheckoutError('Please tell us your name.');
  if (!payload.address.address_line1) return showCheckoutError('Please add your address.');
  if (!payload.address.city) return showCheckoutError('Please add your city.');
  if (!payload.address.pincode) return showCheckoutError('Please add your pincode.');

  button.disabled = true;
  button.textContent = 'Placing your order…';
  feedback.className = 'checkout-feedback';
  feedback.textContent = '';

  try {
    const result = await api('/orders', { method: 'POST', body: payload });
    showOrderPlaced(result);
    state.cart = [];
    saveCart();
    renderCart();
  } catch (error) {
    showCheckoutError(error.message);
  } finally {
    button.disabled = false;
    button.textContent = 'Place order';
  }
}

function showCheckoutError(message) {
  const feedback = $('#checkout-feedback');
  feedback.className = 'checkout-feedback error';
  feedback.textContent = message;
}

function showOrderPlaced(result, paid) {
  const order = result.order;
  const payment = result.payment || {};

  $('#checkout-form-panel').hidden = true;
  $('#checkout-done-panel').hidden = false;

  $('#order-code').textContent = order.code;
  $('#order-total').textContent = money(order.total_inr);
  $('#order-shipping').textContent = Number(order.shipping_inr) === 0 ? 'Free' : money(order.shipping_inr);

  const statusLine = $('#order-pay-status');
  const waLink = $('#order-wa-link');
  $('#order-track-link').href = payment.track_url || `/order.html?code=${order.code}`;

  const showManual = (message) => {
    statusLine.textContent = message || 'We have sent the details to your WhatsApp. Please pay by UPI and share the screenshot so we can start packing.';
    waLink.hidden = false;
    waLink.href = `https://wa.me/${order.phone_e164.replace('+', '')}`;
  };

  const showPaid = () => {
    statusLine.textContent = 'Payment received — we are packing your order. Watch your WhatsApp for updates.';
    waLink.hidden = true;
    $('#order-vpa').hidden = true;
  };

  const vpaBox = $('#order-vpa');
  if (payment.vpa) {
    vpaBox.hidden = false;
    $('#order-vpa-value').textContent = payment.vpa;
    $('#order-payee').textContent = payment.payee_name || 'NuttyWonders';
    $('#order-instructions').textContent = payment.instructions || '';
  } else {
    vpaBox.hidden = true;
  }

  $('#order-items').innerHTML = (result.items || []).map((item) => `
    <div class="order-line">
      <span>${escapeHtml(item.product_name)} × ${item.quantity}</span>
      <strong>${money(item.line_total_inr)}</strong>
    </div>`).join('');

  if (paid) {
    showPaid();
    return;
  }
  if (payment.method === 'razorpay' && payment.razorpay) {
    statusLine.textContent = 'Almost there — please complete the payment in the popup that is now opening.';
    waLink.hidden = true;
    openRazorpay(result, payment.razorpay, showManual, showPaid);
    return;
  }
  showManual();
}

function loadRazorpaySdk() {
  if (window.Razorpay) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const script = document.createElement('script');
    script.src = 'https://checkout.razorpay.com/v1/checkout.js';
    script.async = true;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error('Could not load the payment gateway. Please pay by UPI on WhatsApp instead.'));
    document.head.appendChild(script);
  });
}

async function openRazorpay(result, rz, showManual, showPaid) {
  const order = result.order;
  let rzp;
  try {
    await loadRazorpaySdk();
    rzp = new window.Razorpay({
      key: rz.key_id,
      amount: rz.amount_paise,
      currency: rz.currency || 'INR',
      name: 'NuttyWonders',
      description: 'Order ' + order.code,
      order_id: rz.order_id,
      prefill: {
        name: order.customer_name || '',
        contact: order.phone_e164 || '',
        email: $('#checkout-email').value.trim(),
      },
      theme: { color: '#d7824b' },
      handler(response) {
        verifyPayment(result, response, showManual, showPaid);
      },
      modal: { ondismiss() { showManual(); } },
    });
    rzp.on('payment.failed', () => {
      showManual('Payment was not completed. Your order is safe — you can pay below or on WhatsApp.');
    });
  } catch (error) {
    showManual(error.message);
    return;
  }
  rzp.open();
}

async function verifyPayment(result, response, showManual, showPaid) {
  try {
    await api('/orders/verify', {
      method: 'POST',
      body: {
        razorpay_order_id: response.razorpay_order_id,
        razorpay_payment_id: response.razorpay_payment_id,
        razorpay_signature: response.razorpay_signature,
      },
    });
    showPaid();
  } catch (error) {
    showManual('Payment could not be confirmed yet: ' + error.message + ' Your order is safe — share the UPI screenshot on WhatsApp if you paid.');
  }
}

function openCheckout() {
  if (!state.cart.length) {
    toast('Add something delicious first');
    return;
  }
  const totals = cartTotals();
  $('#checkout-summary-total').textContent = money(totals.total);
  $('#checkout-summary-shipping').textContent = totals.shipping === 0 ? 'Free' : money(totals.shipping);
  $('#checkout-form-panel').hidden = false;
  $('#checkout-done-panel').hidden = true;
  $('#checkout-drawer').classList.add('open');
  $('#cart-drawer').classList.remove('open');
}

function wireEvents() {
  $$('.filter').forEach((button) => button.addEventListener('click', () => {
    $$('.filter').forEach((other) => other.classList.remove('active'));
    button.classList.add('active');
    state.filter = button.dataset.filter;
    renderProducts();
  }));

  $('#modal-close').addEventListener('click', () => $('#modal-backdrop').classList.remove('open'));
  $('#modal-backdrop').addEventListener('click', (event) => {
    if (event.target.id === 'modal-backdrop') event.currentTarget.classList.remove('open');
  });
  $('#modal-add').addEventListener('click', () => {
    addToCart(state.current);
    $('#modal-backdrop').classList.remove('open');
    $('#cart-drawer').classList.add('open');
  });

  $('#cart-open').addEventListener('click', () => {
    renderCart();
    $('#cart-drawer').classList.add('open');
  });
  $('#cart-close').addEventListener('click', () => $('#cart-drawer').classList.remove('open'));
  $('#checkout').addEventListener('click', openCheckout);
  $('#checkout-close').addEventListener('click', () => $('#checkout-drawer').classList.remove('open'));
  $('#checkout-form').addEventListener('submit', submitCheckout);

  $('#newsletter-form').addEventListener('submit', (event) => {
    event.preventDefault();
    $('#newsletter-note').textContent = 'You’re on the list. Welcome to the good stuff!';
    event.target.reset();
  });

  $('#menu-toggle').addEventListener('click', () => {
    const nav = $('.desktop-nav');
    const open = nav.style.display === 'flex';
    nav.style.display = open ? '' : 'flex';
    if (!open) {
      nav.style.position = 'absolute';
      nav.style.top = '78px';
      nav.style.left = '0';
      nav.style.right = '0';
      nav.style.padding = '20px 5vw';
      nav.style.background = 'var(--cream)';
      nav.style.flexDirection = 'column';
    }
  });

  const params = new URLSearchParams(location.search);
  if (params.get('code')) {
    $('#order-lookup-code').value = params.get('code');
  }
}

async function loadTrackOrder(event) {
  event.preventDefault();
  const code = $('#order-lookup-code').value.trim();
  const phone = $('#order-lookup-phone').value.trim();
  const output = $('#order-lookup-result');

  output.hidden = false;
  output.textContent = 'Looking up your order…';

  try {
    const result = await api(`/orders/track?code=${encodeURIComponent(code)}&phone=${encodeURIComponent(phone)}`);
    const order = result.order;
    output.innerHTML = `
      <p class="eyebrow">${escapeHtml(order.code)}</p>
      <h3>${escapeHtml(statusLabel(order.status))}</h3>
      <p>Placed ${new Date(order.created_at).toLocaleDateString('en-IN', { day: 'numeric', month: 'long', year: 'numeric' })} · ${escapeHtml(order.channel === 'whatsapp' ? 'via WhatsApp' : 'on the website')}</p>
      <div class="lookup-lines">
        ${order.items.map((item) => `<div><span>${escapeHtml(item.product_name)} × ${item.quantity}</span><strong>${money(item.line_total_inr)}</strong></div>`).join('')}
        <div class="total"><span>Total</span><strong>${money(order.total_inr)}</strong></div>
      </div>
      <p class="lookup-payment">Payment: ${escapeHtml(paymentLabel(order.payment_status))}</p>`;
  } catch (error) {
    output.textContent = error.message;
  }
}

function statusLabel(status) {
  return {
    new: 'Order received',
    confirmed: 'Confirmed, being packed',
    packed: 'Packed and ready',
    shipped: 'On its way',
    delivered: 'Delivered',
    cancelled: 'Cancelled',
  }[status] || status;
}

function paymentLabel(status) {
  return {
    pending: 'awaiting your UPI payment',
    paid: 'received, thank you',
    cod: 'cash on delivery',
    failed: 'failed, please retry',
    refunded: 'refunded',
  }[status] || status;
}

async function init() {
  wireEvents();
  saveCart();

  try {
    const [products, settings] = await Promise.all([api('/products'), api('/settings')]);
    state.products = products.products || [];
    state.settings = settings.settings || {};
  } catch (error) {
    $('#product-grid').innerHTML = `<p class="cart-empty">We could not load the pantry just now. Please refresh in a moment.</p>`;
    return;
  }

  renderProducts();
  renderCart();
  $('#order-lookup-form').addEventListener('submit', loadTrackOrder);
}

init();
if('serviceWorker' in navigator)window.addEventListener('load',()=>navigator.serviceWorker.register('/sw.js'));

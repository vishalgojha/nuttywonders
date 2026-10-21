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
  moment: 'all',
  query: '',
};

const BUNDLES = {
  breakfast: ['chocolate-granola', 'berries-granola', 'apple-cinnamon-granola'],
  chai: ['cocoa-ragi-bites', 'oats-bites', 'crunchy-munchy-snack'],
  gifting: ['berries-granola', 'dates-nuts-bites', 'cashew-truffles'],
};

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

const productById = (id) => state.products.find((p) => String(p.id) === String(id));

function productImages(product) {
  const paths = Array.isArray(product?.image_paths) ? product.image_paths.filter(Boolean) : [];
  if (product?.image_path && !paths.includes(product.image_path)) paths.unshift(product.image_path);
  return paths.slice(0, 5);
}

function visualFor(product, imageAttrs = 'loading="lazy" decoding="async"') {
  const image = productImages(product)[0];
  if (image) {
    return `<div class="product-image photo"><img src="${escapeHtml(image)}" alt="${escapeHtml(product.name)}" ${imageAttrs}></div>`;
  }
  return `<div class="product-image ${product.category}"><div class="shape"></div></div>`;
}

function productGallery(product) {
  const images = productImages(product);
  if (!images.length) return visualFor(product);
  return `<div class="product-gallery"><div class="product-gallery-main" id="product-gallery-main"><img src="${escapeHtml(images[0])}" alt="${escapeHtml(product.name)}" /></div>${images.length > 1 ? `<div class="product-gallery-thumbs">${images.map((image, index) => `<button type="button" class="product-gallery-thumb${index === 0 ? ' active' : ''}" data-gallery-image="${escapeHtml(image)}"><img src="${escapeHtml(image)}" alt="${escapeHtml(product.name)} image ${index + 1}" /></button>`).join('')}</div>` : ''}</div>`;
}

function escapeHtml(value) {
  return String(value ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[c]);
}

function renderProducts() {
  const categoryList = state.filter === 'all'
    ? state.products
    : state.products.filter((p) => p.category === state.filter);
  const momentList = state.moment === 'all'
    ? categoryList
    : categoryList.filter((product) => productMatchesMoment(product, state.moment));
  const list = state.query
    ? momentList.filter((product) => productMatchesQuery(product, state.query))
    : momentList;

  $('#product-count').textContent = `${list.length} snack${list.length === 1 ? '' : 's'}`;
  $('#product-grid').innerHTML = list.map((product, index) => `
    <article class="product-card" data-id="${product.id}">
      <div class="product-visual-wrap">${visualFor(product, index < 4 ? 'loading="eager" fetchpriority="high" decoding="async"' : 'loading="lazy" fetchpriority="low" decoding="async"')}<button class="quick-add" type="button" aria-label="Add ${escapeHtml(product.name)} to bag">+</button></div>
      <div class="product-info">
        <div>
          <h3>${escapeHtml(product.name)}</h3>
          <p>${packLabel(product.pack_size_g)} · ${escapeHtml(product.category)}</p>
          <div class="benefit-tags">${productBenefits(product).map((tag) => `<span>${escapeHtml(tag)}</span>`).join('')}</div>
        </div>
        <span class="product-price">${money(product.price_inr)}</span>
      </div>
    </article>`).join('');

  $$('.product-card').forEach((card) => card.addEventListener('click', () => {
    openModal(productById(card.dataset.id));
  }));
  $$('.quick-add').forEach((button) => button.addEventListener('click', (event) => {
    event.stopPropagation();
    addToCart(productById(button.closest('.product-card').dataset.id));
  }));
}

function renderJournal(articles) {
  const grid = $('#journal-grid');
  if (!grid) return;
  grid.innerHTML = articles.length ? articles.slice(0, 3).map((article) => {
    const image = article.image_path
      ? `<img src="${escapeHtml(article.image_path)}" alt="${escapeHtml(article.title)}" loading="lazy" decoding="async" />`
      : '<div class="journal-art cocoa"></div>';
    return `<article><a href="/journal/${encodeURIComponent(article.slug)}"><div class="journal-img">${image}</div><p class="eyebrow">${escapeHtml(article.eyebrow)}</p><h3>${escapeHtml(article.title)}</h3><span class="text-link">Read more →</span></a></article>`;
  }).join('') : '<p class="cart-empty">New snack notes are coming soon.</p>';
}

async function loadJournal() {
  try {
    const result = await api('/journal');
    renderJournal(result.articles || []);
  } catch {
    // The journal is supplementary; keep the page usable if it is unavailable.
  }
}

function updateProductStructuredData() {
  const script = $('#product-structured-data');
  if (!script) return;
  script.textContent = JSON.stringify({
    '@context': 'https://schema.org',
    '@type': 'ItemList',
    name: 'NuttyWonders snack pantry',
    itemListElement: state.products.map((product, index) => ({
      '@type': 'ListItem',
      position: index + 1,
      item: {
        '@type': 'Product',
        name: product.name,
        description: product.description || product.tagline || '',
        image: productImages(product).map((image) => new URL(image, location.origin).href),
        category: product.category,
        offers: {
          '@type': 'Offer',
          priceCurrency: 'INR',
          price: Number(product.price_inr || 0).toFixed(2),
          availability: Number(product.stock_packs) > 0 ? 'https://schema.org/InStock' : 'https://schema.org/OutOfStock',
          url: `${location.origin}/#shop`,
        },
      },
    })),
  });
}

function productBenefits(product) {
  const name = `${product.name || ''} ${(product.ingredients || []).join(' ')}`.toLowerCase();
  const tags = [];
  if (product.category === 'granola') tags.push('Breakfast-ready');
  if (name.includes('ragi') || name.includes('millet')) tags.push('Millet goodness');
  if (name.includes('date')) tags.push('Naturally sweet');
  if (name.includes('cocoa') || name.includes('chocolate')) tags.push('Chocolate comfort');
  if (product.category === 'bars') tags.push('Chai-time crunch');
  if (!tags.length) tags.push('Small-batch made');
  return tags.slice(0, 2);
}

function productMatchesMoment(product, moment) {
  const text = `${product.name || ''} ${product.description || ''} ${(product.ingredients || []).join(' ')}`.toLowerCase();
  if (moment === 'breakfast') return product.category === 'granola' || text.includes('oats');
  if (moment === 'chai') return product.category === 'bars' || product.category === 'bites' || text.includes('cocoa') || text.includes('chocolate');
  if (moment === 'gifting') return product.category === 'granola' || product.category === 'bars' || text.includes('truffle');
  return true;
}

function productMatchesQuery(product, query) {
  const text = `${product.name || ''} ${product.tagline || ''} ${product.description || ''} ${product.category || ''} ${(product.ingredients || []).join(' ')}`.toLowerCase();
  return text.includes(query.trim().toLowerCase());
}

function openModal(product) {
  if (!product) return;
  state.current = product;

  $('#modal-category').textContent = product.tagline || product.category;
  $('#modal-title').textContent = product.name;
  $('#modal-description').textContent = product.description || '';
  $('#modal-visual').innerHTML = productGallery(product);
  $$('.product-gallery-thumb').forEach((thumb) => thumb.addEventListener('click', () => {
    $('#product-gallery-main img').src = thumb.dataset.galleryImage;
    $$('.product-gallery-thumb').forEach((item) => item.classList.toggle('active', item === thumb));
  }));
  $('#modal-price').textContent = money(product.price_inr);
  $('#modal-pack').textContent = `${packLabel(product.pack_size_g)} pack`;
  $('#modal-qty').value = '1';
  $('#modal-qty').textContent = '1';
  $('#modal-details').innerHTML = [
    ...productBenefits(product).map((tag) => `<span>${escapeHtml(tag)}</span>`),
    product.shelf_life_days ? `<span>Best enjoyed within ${product.shelf_life_days} days</span>` : '',
  ].join('');

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

function openHamperLightbox(card) {
  const image = card.querySelector('.hamper-image img');
  const art = card.querySelector('.hamper-text-art');
  const title = card.querySelector('h3')?.textContent?.trim() || 'NuttyWonders hamper';
  const description = card.querySelector('.hamper-card-copy > p:not(.eyebrow)')?.textContent?.trim() || '';
  const eyebrow = card.querySelector('.eyebrow')?.textContent?.trim() || 'NuttyWonders gifting';
  const price = card.querySelector('.hamper-meta strong')?.textContent?.trim() || '';
  const enquire = card.querySelector('.hamper-meta a')?.href || 'https://wa.me/918108398025';

  $('#hamper-lightbox-eyebrow').textContent = eyebrow;
  $('#hamper-lightbox-title').textContent = title;
  $('#hamper-lightbox-description').textContent = description;
  $('#hamper-lightbox-price').textContent = price;
  $('#hamper-lightbox-enquire').href = enquire;
  $('#hamper-lightbox-visual').innerHTML = image
    ? `<img src="${escapeHtml(image.src)}" alt="${escapeHtml(image.alt || title)}" />`
    : art?.outerHTML || '';
  $('#hamper-lightbox').classList.add('open');
  document.body.classList.add('lightbox-open');
}

function closeHamperLightbox() {
  $('#hamper-lightbox').classList.remove('open');
  document.body.classList.remove('lightbox-open');
}

function addAssistantMessage(text, role = 'bot') {
  const message = document.createElement('div');
  message.className = `assistant-message assistant-message-${role}`;
  message.textContent = text;
  $('#assistant-messages').appendChild(message);
  $('#assistant-messages').scrollTop = $('#assistant-messages').scrollHeight;
}

function assistantLocalFallback(message) {
  const lower = message.toLowerCase();
  if (/(birthday|anniversary|milestone|return gift|thank[- ]you)/.test(lower)) {
    return { reply: 'That sounds like a lovely gifting moment. Our curated hampers work beautifully for birthdays, anniversaries and milestones, and we can customise the treats, budget and packaging too.', actions: [{ type: 'navigate', target: '#gift' }] };
  }
  if (/(diwali|hamper|gift|gifting|corporate)/.test(lower)) {
    return { reply: 'I’ll take you to our Diwali hampers and gifting options.', actions: [{ type: 'navigate', target: '#gift' }] };
  }
  if (/(bag|cart|checkout)/.test(lower)) {
    return { reply: 'Opening your bag now.', actions: [{ type: 'open_cart' }] };
  }
  const product = state.products.find((item) => `${item.name} ${item.slug} ${item.category}`.toLowerCase().includes(lower.replace(/add|buy|please|to my cart|in my bag/g, '').trim()))
    || state.products.find((item) => lower.includes(item.name.toLowerCase()) || lower.includes(item.slug.toLowerCase()));
  if (product && /(add|buy|bag|cart|order)/.test(lower)) {
    return { reply: `${product.name} is ready to add to your bag.`, actions: [{ type: 'add_to_cart', slug: product.slug, quantity: 1 }] };
  }
  if (/(chai|snack|granola|brownie|bite)/.test(lower)) {
    return { reply: 'I can help you choose a snack. Try asking for a chai-time pick, granola, brownies, or gifting.' };
  }
  return { reply: 'I can help with snacks, gifting, page navigation, or adding products to your bag. What are you looking for?' };
}

function runAssistantActions(actions = []) {
  actions.forEach((action) => {
    if (action.type === 'navigate' && /^#(top|shop|gift|our-story|journal|track)$/.test(action.target || '')) {
      document.querySelector(action.target)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
    if (action.type === 'add_to_cart') {
      const product = state.products.find((item) => item.slug === action.slug);
      if (product) addToCart(product, Math.max(1, Math.min(5, Number(action.quantity) || 1)));
    }
    if (action.type === 'open_cart') {
      renderCart();
      $('#cart-drawer').classList.add('open');
    }
  });
}

async function sendAssistantMessage(message) {
  addAssistantMessage(message, 'user');
  const loading = document.createElement('div');
  loading.className = 'assistant-message assistant-message-bot assistant-loading';
  loading.textContent = 'Thinking…';
  $('#assistant-messages').appendChild(loading);
  try {
    const result = await fetch('/api/assistant', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message }),
    });
    if (!result.ok) throw new Error('assistant unavailable');
    const reply = await result.json();
    loading.remove();
    addAssistantMessage(reply.reply || 'I’m here to help.');
    runAssistantActions(reply.actions);
  } catch {
    loading.remove();
    const reply = assistantLocalFallback(message);
    addAssistantMessage(reply.reply);
    runAssistantActions(reply.actions);
  }
}

function addToCart(product, quantity = 1) {
  if (!product) return;
  const line = state.cart.find((item) => String(item.id) === String(product.id));
  const safeQuantity = Math.max(1, Math.min(20, Number(quantity) || 1));
  if (line) line.qty += safeQuantity;
  else state.cart.push({ id: product.id, qty: safeQuantity });
  saveCart();
  renderCart();
  loadJournal();
  toast(`${product.name} added to your bag`);
}

function addBundle(key) {
  const selected = (BUNDLES[key] || []).map((slug) => state.products.find((product) => product.slug === slug)).filter(Boolean);
  selected.forEach((product) => addToCart(product));
  if (selected.length) {
    renderCart();
    $('#cart-drawer').classList.add('open');
  }
}

function renderBundleTotals() {
  Object.entries(BUNDLES).forEach(([key, slugs]) => {
    const total = slugs.reduce((sum, slug) => {
      const product = state.products.find((item) => item.slug === slug);
      return sum + Number(product?.price_inr || 0);
    }, 0);
    const element = document.querySelector(`[data-bundle-total="${key}"]`);
    if (element && total) element.textContent = `${slugs.length} snacks · ${money(total)}`;
  });
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

  const freeOver = Number(state.settings.free_shipping_over_inr || 1000);
  const fee = Number(state.settings.delivery_fee_inr || 60);
  const safeFreeOver = isNaN(freeOver) ? 1000 : freeOver;
  const safeFee = isNaN(fee) ? 60 : fee;
  const shipping = subtotal >= safeFreeOver ? 0 : safeFee;

  return { subtotal, shipping, total: subtotal + shipping };
}

function renderCart() {
  const area = $('#cart-items');

  if (!state.cart.length) {
    area.innerHTML = `<p class="cart-empty">Your bag is waiting for something delicious.<br><br>Explore the pantry and add your favourites here.</p>`;
    $('#cart-subtotal').textContent = '₹0.00';
    $('#cart-total').textContent = '₹0';
    $('#cart-shipping').textContent = '₹0.00';
    updateShippingProgress(0);
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
  updateShippingProgress(totals.subtotal);

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

function updateShippingProgress(subtotal) {
  const note = $('#shipping-progress-note');
  const bar = $('#shipping-progress-bar');
  if (!note || !bar) return;
  const threshold = Number(state.settings.free_shipping_over_inr || 1000);
  const safeThreshold = Number.isFinite(threshold) && threshold > 0 ? threshold : 1000;
  const progress = Math.min(100, Math.round((Number(subtotal || 0) / safeThreshold) * 100));
  bar.style.width = `${progress}%`;
  note.textContent = subtotal >= safeThreshold
    ? 'You unlocked free delivery ✦'
    : `Add ${money(safeThreshold - Number(subtotal || 0))} more for free delivery`;
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
      landmark: $('#checkout-landmark') ? $('#checkout-landmark').value.trim() : '',
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

  const drawer = $('#checkout-drawer');
  drawer.hidden = false;
  drawer.scrollIntoView({ behavior: 'smooth', block: 'center' });

  $('#checkout-form-panel').hidden = true;
  $('#checkout-done-panel').hidden = false;

  $('#order-code').textContent = order.code;
  $('#order-total').textContent = money(order.total_inr);
  $('#order-shipping').textContent = Number(order.shipping_inr) === 0 ? 'Free' : money(order.shipping_inr);

  const statusLine = $('#order-pay-status');
  const paymentRetry = $('#order-payment-retry');
  $('#order-track-link').href = payment.track_url || `/order.html?code=${order.code}`;

  const showPaymentUnavailable = (message, retry) => {
    statusLine.textContent = message || 'Payment was not completed. Please retry the secure Razorpay payment.';
    paymentRetry.hidden = !retry;
    paymentRetry.onclick = retry || null;
  };

  const showPaid = () => {
    statusLine.textContent = 'Payment received — we are packing your order.';
    paymentRetry.hidden = true;
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
    paymentRetry.hidden = true;
    openRazorpay(result, payment.razorpay, showPaymentUnavailable, showPaid);
    return;
  }
  showPaymentUnavailable('Online payment is temporarily unavailable. Please try placing your order again.', false);
}

function loadRazorpaySdk() {
  if (window.Razorpay) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const script = document.createElement('script');
    script.src = 'https://checkout.razorpay.com/v1/checkout.js';
    script.async = true;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error('Could not load Razorpay Checkout. Please check your connection and retry.'));
    document.head.appendChild(script);
  });
}

function openRazorpay(result, rz, showPaymentUnavailable, showPaid) {
  const order = result.order;
  const retry = () => openRazorpay(result, rz, showPaymentUnavailable, showPaid);
  const launch = () => {
    try {
      const rzp = new window.Razorpay({
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
        retry: { enabled: true },
        theme: { color: '#d7824b' },
        handler(response) {
          verifyPayment(result, response, showPaymentUnavailable, showPaid);
        },
        modal: { ondismiss() { showPaymentUnavailable(undefined, retry); } },
      });
      rzp.on('payment.failed', () => {
        showPaymentUnavailable('Payment was not completed. Please retry the secure Razorpay payment.', retry);
      });
      rzp.open();
    } catch (error) {
      showPaymentUnavailable(error.message, retry);
    }
  };

  // A retry happens after the Checkout SDK is already loaded. Open it
  // synchronously from the click handler to preserve the browser's user
  // activation; awaiting even an already-resolved Promise can block the modal.
  if (window.Razorpay) {
    launch();
    return;
  }
  loadRazorpaySdk().then(launch).catch((error) => showPaymentUnavailable(error.message, retry));
}

async function verifyPayment(result, response, showPaymentUnavailable, showPaid) {
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
    showPaymentUnavailable('Payment could not be confirmed yet: ' + error.message + ' Please retry the Razorpay payment.', () => openRazorpay(result, result.payment?.razorpay, showPaymentUnavailable, showPaid));
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
  $$('.moment').forEach((button) => button.addEventListener('click', () => {
    $$('.moment').forEach((other) => other.classList.remove('active'));
    button.classList.add('active');
    state.moment = button.dataset.moment;
    renderProducts();
  }));
  $$('.bundle-add').forEach((button) => button.addEventListener('click', () => addBundle(button.dataset.bundle)));
  $('.search-toggle').addEventListener('click', () => {
    const panel = $('#search-panel');
    panel.hidden = false;
    $('#site-search').focus();
  });
  $('#search-close').addEventListener('click', () => {
    $('#search-panel').hidden = true;
    state.query = '';
    $('#site-search').value = '';
    renderProducts();
  });
  $('#site-search').addEventListener('input', (event) => {
    state.query = event.target.value;
    renderProducts();
    $('#search-note').textContent = state.query ? `${$('#product-count').textContent} matching snacks` : 'Search by snack, ingredient or category.';
  });
  $('#site-search').addEventListener('keydown', (event) => {
    if (event.key === 'Escape') $('#search-close').click();
  });

  $('#assistant-launcher').addEventListener('click', () => {
    const panel = $('#assistant-panel');
    const open = !panel.hidden;
    panel.hidden = open;
    $('#assistant-launcher').setAttribute('aria-expanded', String(!open));
    if (!open) $('#assistant-input').focus();
  });
  $('#assistant-close').addEventListener('click', () => {
    $('#assistant-panel').hidden = true;
    $('#assistant-launcher').setAttribute('aria-expanded', 'false');
  });
  $('#assistant-form').addEventListener('submit', (event) => {
    event.preventDefault();
    const input = $('#assistant-input');
    const message = input.value.trim();
    if (!message) return;
    input.value = '';
    sendAssistantMessage(message);
  });
  $$('[data-assistant-prompt]').forEach((button) => button.addEventListener('click', () => sendAssistantMessage(button.dataset.assistantPrompt)));

  $$('.hamper-card').forEach((card) => {
    card.setAttribute('tabindex', '0');
    card.setAttribute('role', 'button');
    card.addEventListener('click', (event) => {
      if (event.target.closest('a, button')) return;
      openHamperLightbox(card);
    });
    card.addEventListener('keydown', (event) => {
      if ((event.key === 'Enter' || event.key === ' ') && !event.target.closest('a, button')) {
        event.preventDefault();
        openHamperLightbox(card);
      }
    });
  });

  $('#modal-close').addEventListener('click', () => $('#modal-backdrop').classList.remove('open'));
  $('#modal-backdrop').addEventListener('click', (event) => {
    if (event.target.id === 'modal-backdrop') event.currentTarget.classList.remove('open');
  });
  $('#hamper-lightbox-close').addEventListener('click', closeHamperLightbox);
  $('#hamper-lightbox').addEventListener('click', (event) => {
    if (event.target.id === 'hamper-lightbox') closeHamperLightbox();
  });
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && $('#hamper-lightbox').classList.contains('open')) closeHamperLightbox();
  });
  $('#modal-add').addEventListener('click', () => {
    addToCart(state.current, Number($('#modal-qty').textContent));
    $('#modal-backdrop').classList.remove('open');
    $('#cart-drawer').classList.add('open');
  });
  $('#modal-qty-minus').addEventListener('click', () => {
    const output = $('#modal-qty');
    output.textContent = String(Math.max(1, Number(output.textContent) - 1));
  });
  $('#modal-qty-plus').addEventListener('click', () => {
    const output = $('#modal-qty');
    output.textContent = String(Math.min(20, Number(output.textContent) + 1));
  });

  $('#cart-open').addEventListener('click', () => {
    renderCart();
    $('#cart-drawer').classList.add('open');
  });
  $('#cart-close').addEventListener('click', () => $('#cart-drawer').classList.remove('open'));
  $('#checkout').addEventListener('click', openCheckout);
  $('#checkout-close').addEventListener('click', () => $('#checkout-drawer').classList.remove('open'));
  $('#checkout-form').addEventListener('submit', submitCheckout);
  $('#checkout-form-panel').addEventListener('focusin', (event) => {
    if (!event.target.matches('input, textarea, select')) return;
    // Keep the active field visible when a mobile keyboard reduces the viewport.
    requestAnimationFrame(() => event.target.scrollIntoView({ block: 'nearest', behavior: 'smooth' }));
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
    pending: 'awaiting your online payment',
    paid: 'received, thank you',
    cod: 'cash on delivery',
    failed: 'failed, please retry',
    refunded: 'refunded',
  }[status] || status;
}

function decodeSetting(val) {
  if (val === null || val === undefined) return val;
  if (typeof val === 'number') return val;
  if (typeof val !== 'string') return val;
  // try numeric parse
  const num = Number(val);
  if (!isNaN(num)) return num;
  // try base64 decode, common for settings coming as base64
  try {
    const decoded = atob(val);
    const decodedNum = Number(decoded);
    if (!isNaN(decodedNum)) return decodedNum;
    // if decoded looks like a number string
    return decoded;
  } catch {}
  return val;
}

function normalizeSettings(raw = {}) {
  const out = {};
  for (const k in raw) {
    out[k] = decodeSetting(raw[k]);
  }
  return out;
}

async function init() {
  wireEvents();
  saveCart();

  try {
    const [products, settings] = await Promise.all([api('/products'), api('/settings')]);
    state.products = products.products || [];
    state.settings = normalizeSettings(settings.settings || {});
  } catch (error) {
    $('#product-grid').innerHTML = `<p class="cart-empty">We could not load the pantry just now. Please refresh in a moment.</p>`;
    return;
  }

  renderProducts();
  renderBundleTotals();
  updateProductStructuredData();
  renderCart();
  $('#order-lookup-form').addEventListener('submit', loadTrackOrder);
}

init();
if('serviceWorker' in navigator)window.addEventListener('load',()=>navigator.serviceWorker.register('/sw.js'));

// PWA install prompt
let deferredPrompt = null;
window.addEventListener('beforeinstallprompt', (e) => {
  e.preventDefault();
  deferredPrompt = e;
  const btn = $('#pwa-install');
  if (btn) btn.hidden = false;
});

document.addEventListener('click', (e) => {
  if (e.target && e.target.id === 'pwa-install') {
    const btn = e.target;
    btn.disabled = true;
    btn.textContent = 'Installing…';
    deferredPrompt.prompt();
    deferredPrompt.userChoice.finally(() => {
      deferredPrompt = null;
      btn.hidden = true;
    });
  }
});

// Close drawers on outside tap for mobile
document.addEventListener('touchstart', () => {
  // noop to keep pointer events responsive
});
// Improve mobile menu
const menuBtn = $('#menu-toggle');
const desktopNav = $('.desktop-nav');
if (menuBtn && desktopNav) {
  let navOpen = false;
  menuBtn.addEventListener('click', () => {
    navOpen = !navOpen;
    desktopNav.classList.toggle('open', navOpen);
    menuBtn.setAttribute('aria-expanded', navOpen);
    if (navOpen) {
      desktopNav.style.position = 'absolute';
      desktopNav.style.top = '78px';
      desktopNav.style.left = '0';
      desktopNav.style.right = '0';
      desktopNav.style.padding = '20px 5vw';
      desktopNav.style.background = 'var(--cream)';
      desktopNav.style.flexDirection = 'column';
      desktopNav.style.display = 'flex';
    } else {
      desktopNav.style.display = '';
    }
  });
  // close on outside tap
  document.addEventListener('click', (e) => {
    if (!navOpen) return;
    if (e.target.closest('.site-header')) return;
    navOpen = false;
    desktopNav.classList.remove('open');
    desktopNav.style.display = '';
  });
}

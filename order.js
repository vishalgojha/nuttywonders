const $ = (sel) => document.querySelector(sel);

function money(amount) {
  return '₹' + Number(amount || 0).toLocaleString('en-IN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

function escapeHtml(value) {
  return String(value ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[c]);
}

const statusSteps = ['new', 'confirmed', 'packed', 'shipped', 'delivered'];

const statusCopy = {
  new: 'Order received',
  confirmed: 'Confirmed, being packed',
  packed: 'Packed and ready',
  shipped: 'On its way',
  delivered: 'Delivered',
  cancelled: 'Cancelled',
};

const paymentCopy = {
  pending: 'Awaiting your UPI payment',
  paid: 'Payment received, thank you',
  cod: 'Cash on delivery',
  failed: 'Payment failed, please retry',
  refunded: 'Refunded',
};

async function track(event) {
  event.preventDefault();
  const output = $('#order-lookup-result');
  output.hidden = false;
  output.textContent = 'Looking up your order…';

  const code = $('#order-lookup-code').value.trim();
  const phone = $('#order-lookup-phone').value.trim();

  try {
    const response = await fetch(`/api/orders/track?code=${encodeURIComponent(code)}&phone=${encodeURIComponent(phone)}`, {
      credentials: 'same-origin',
    });
    const payload = await response.json();
    if (!response.ok) throw new Error(payload.error || 'We could not find that order.');

    render(payload.order);
  } catch (error) {
    output.innerHTML = `<p>${escapeHtml(error.message)}</p>`;
  }
}

function render(order) {
  const output = $('#order-lookup-result');
  const stepIndex = statusSteps.indexOf(order.status);
  const address = [order.address_line1, order.address_line2, order.city, order.pincode].filter(Boolean).join(', ');

  output.innerHTML = `
    <p class="eyebrow">${escapeHtml(order.code)}</p>
    <h3>${escapeHtml(statusCopy[order.status] || order.status)}</h3>
    <p>${new Date(order.created_at).toLocaleDateString('en-IN', { day: 'numeric', month: 'long', year: 'numeric' })}
       · ${escapeHtml(order.channel === 'whatsapp' ? 'ordered on WhatsApp' : 'ordered on the website')}</p>

    ${order.status !== 'cancelled' ? `
      <div class="track-steps">
        ${statusSteps.map((step, index) => `
          <div class="track-step ${index <= stepIndex ? 'done' : ''} ${order.status === 'cancelled' ? 'void' : ''}">
            <span></span><small>${escapeHtml(statusCopy[step])}</small>
          </div>`).join('')}
      </div>` : ''}

    <div class="lookup-lines">
      ${order.items.map((item) => `
        <div><span>${escapeHtml(item.product_name)} × ${item.quantity}</span><strong>${money(item.line_total_inr)}</strong></div>`).join('')}
      <div><span>Delivery</span><strong>${Number(order.shipping_inr) === 0 ? 'Free' : money(order.shipping_inr)}</strong></div>
      <div class="total"><span>Total</span><strong>${money(order.total_inr)}</strong></div>
    </div>

    <p class="lookup-payment">${escapeHtml(paymentCopy[order.payment_status] || order.payment_status)}</p>
    ${address ? `<p class="lookup-payment">Delivering to ${escapeHtml(address)}</p>` : ''}
    ${order.payment_status === 'pending' && order.status !== 'cancelled'
      ? '<p class="lookup-payment">Pay by UPI and send the screenshot on WhatsApp so we can pack right away.</p>' : ''}`;
}

const params = new URLSearchParams(location.search);
if (params.get('code')) {
  $('#order-lookup-code').value = params.get('code');
  $('#order-lookup-phone').focus();
} else {
  $('#order-lookup-code').focus();
}

$('#order-lookup-form').addEventListener('submit', track);
if (params.get('code') && params.get('phone')) $('#order-lookup-form').requestSubmit();

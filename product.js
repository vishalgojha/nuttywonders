const escapeProductHtml = (value) => String(value ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const moneyProduct = (amount) => '₹' + Number(amount || 0).toLocaleString('en-IN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });

async function loadProductPage() {
  const slug = location.pathname.split('/').filter(Boolean).pop();
  try {
    const response = await fetch('/api/products');
    const payload = await response.json();
    const product = (payload.products || []).find((item) => item.slug === slug);
    if (!product) throw new Error('Product not found');

    const url = `${location.origin}/products/${product.slug}`;
    const description = product.description || product.tagline || `${product.name} from NuttyWonders.`;
    document.title = `${product.name} | NuttyWonders`;
    document.querySelector('meta[name="description"]').content = description;
    document.querySelector('#product-canonical').href = url;
    document.querySelector('#product-og-title').content = `${product.name} | NuttyWonders`;
    document.querySelector('#product-og-description').content = description;
    if (product.image_path) document.querySelector('#product-og-image').content = `${location.origin}${product.image_path}`;

    document.querySelector('#product-category').textContent = product.tagline || product.category;
    document.querySelector('#product-name').textContent = product.name;
    document.querySelector('#product-description').textContent = description;
    document.querySelector('#product-price').textContent = `${moneyProduct(product.price_inr)} · ${product.pack_size_g || 200} g pack`;
    document.querySelector('#product-image').innerHTML = product.image_path
      ? `<img src="${escapeProductHtml(product.image_path)}" alt="${escapeProductHtml(product.name)}" />`
      : `<div class="shape ${escapeProductHtml(product.category)}"></div>`;
    document.querySelector('#product-meta').innerHTML = [
      ...(product.ingredients || []).slice(0, 4).map((item) => `<span>${escapeProductHtml(item)}</span>`),
      ...(product.allergens || []).map((item) => `<span>Contains: ${escapeProductHtml(item)}</span>`),
      product.shelf_life_days ? `<span>Best enjoyed within ${product.shelf_life_days} days</span>` : '',
    ].join('');

    document.querySelector('#product-structured-data')?.remove();
    const schema = document.createElement('script');
    schema.id = 'product-structured-data';
    schema.type = 'application/ld+json';
    schema.textContent = JSON.stringify({
      '@context': 'https://schema.org', '@type': 'Product', name: product.name, description,
      image: product.image_path ? `${location.origin}${product.image_path}` : undefined,
      category: product.category, brand: { '@type': 'Brand', name: 'NuttyWonders' },
      offers: { '@type': 'Offer', url, priceCurrency: 'INR', price: Number(product.price_inr || 0).toFixed(2), availability: Number(product.stock_packs) > 0 ? 'https://schema.org/InStock' : 'https://schema.org/OutOfStock' },
    });
    document.head.appendChild(schema);
    document.querySelector('#product-loading').hidden = true;
    document.querySelector('#product-detail').hidden = false;
  } catch {
    document.querySelector('#product-loading').hidden = true;
    document.querySelector('#product-error').hidden = false;
  }
}

loadProductPage();

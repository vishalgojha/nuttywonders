const slug = decodeURIComponent(location.pathname.replace(/^\/journal\/?/, '').replace(/\/$/, ''));
const escapeHtml = (value) => String(value ?? '').replace(/[&<>"']/g, (c) => ({ '&':'&amp;', '<':'&lt;', '>':'&gt;', '"':'&quot;', "'":'&#39;' }[c]));
async function loadArticle() {
  const response = await fetch(`/api/journal/${encodeURIComponent(slug)}`);
  const payload = await response.json();
  if (!response.ok) throw new Error(payload.error || 'Article not found');
  const article = payload.article;
  document.title = `${article.title} — NuttyWonders Journal`;
  document.querySelector('meta[name="description"]').content = article.excerpt || article.title;
  document.querySelector('link[rel="canonical"]').href = `${location.origin}/journal/${encodeURIComponent(article.slug)}`;
  const media = article.image_path ? `<img src="${escapeHtml(article.image_path)}" alt="${escapeHtml(article.title)}" />` : '<div class="article-art"></div>';
  const paragraphs = String(article.body || '').split(/\n\s*\n/).map((p) => `<p>${escapeHtml(p).replace(/\n/g, '<br>')}</p>`).join('');
  document.querySelector('#article-page').innerHTML = `<article class="journal-article"><p class="eyebrow">${escapeHtml(article.eyebrow)}</p><h1>${escapeHtml(article.title)}</h1><p class="article-excerpt">${escapeHtml(article.excerpt)}</p><div class="article-hero">${media}</div><div class="article-body">${paragraphs}</div><a class="text-link" href="/#journal">← Back to the journal</a></article>`;
  const structured = document.createElement('script'); structured.type = 'application/ld+json'; structured.textContent = JSON.stringify({ '@context':'https://schema.org', '@type':'Article', headline:article.title, description:article.excerpt, image:article.image_path ? new URL(article.image_path, location.origin).href : undefined, datePublished:article.published_at, dateModified:article.updated_at, publisher:{ '@type':'Organization', name:'NuttyWonders', url:location.origin } }); document.head.appendChild(structured);
}
loadArticle().catch(() => { document.querySelector('#article-page').innerHTML = '<p class="cart-empty">This article is not available.</p>'; });
